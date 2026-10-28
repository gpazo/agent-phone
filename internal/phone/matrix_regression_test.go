package phone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureService(t *testing.T) *Service {
	t.Helper()
	s := &Service{data: t.TempDir(), phones: map[string]*Phone{}}
	s.runCommand = func(_ context.Context, program string, args ...string) ([]byte, error) {
		if program == "/usr/bin/git" {
			return []byte("fixture-revision"), nil
		}
		if program == "/usr/bin/plutil" {
			return []byte("org.example.fixture"), nil
		}
		return nil, fmt.Errorf("unexpected external command: %s %v", program, args)
	}
	s.buildCommand = func(*exec.Cmd) error { return errors.New("fixture build failure") }
	return s
}
func fixtureProject(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "App.xcodeproj")
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func fixtureManifest(t *testing.T, m Matrix) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "matrix.json")
	if err := atomicJSON(p, m); err != nil {
		t.Fatal(err)
	}
	return p
}
func reportJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err = json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	return report
}
func TestMatrixRejectsIncompatibleManagedTarget(t *testing.T) {
	for _, reason := range []string{"runtime", "type", "missing", "duplicate", "renamed"} {
		t.Run(reason, func(t *testing.T) {
			s := fixtureService(t)
			m := Matrix{Name: "test", Runtime: "runtime-A", DeviceType: "type-A"}
			p := &Phone{UDID: "managed", Name: "test / one", lease: &Lease{Owner: "sentinel", Expires: time.Now().Add(time.Hour)}}
			s.phones[p.UDID] = p
			runtime, typ, name := "runtime-A", "type-A", "agent-phone · test / one"
			if reason == "runtime" {
				runtime = "runtime-B"
			}
			if reason == "type" {
				typ = "type-B"
			}
			if reason == "renamed" {
				name = "someone else's phone"
			}
			devices := []map[string]any{{"udid": p.UDID, "name": name, "deviceTypeIdentifier": typ, "isAvailable": true}}
			if reason == "missing" {
				devices = nil
			}
			if reason == "duplicate" {
				s.phones["second"] = &Phone{UDID: "second", Name: p.Name, lease: p.lease}
			}
			s.runCommand = func(_ context.Context, program string, args ...string) ([]byte, error) {
				if program != "/usr/bin/xcrun" || strings.Join(args, " ") != "simctl list devices --json" {
					t.Errorf("unexpected mutation: %s %v", program, args)
					return nil, errors.New("mutation forbidden")
				}
				return json.Marshal(map[string]any{"devices": map[string]any{runtime: devices}})
			}
			r := s.runVariant(context.Background(), "run", m, Variant{Name: "one"}, BuildResult{Apps: []AppBuild{{Path: "/fixture.app"}}})
			if r.Status != "failed" || !strings.Contains(r.Error, "matrix target") {
				t.Fatalf("expected matrix target identity rejection, got %+v", r)
			}
		})
	}
}
func TestResultRetainsFailedMatrixReport(t *testing.T) {
	value := map[string]any{"report": "/run.json", "results": []string{"failed"}}
	r := Result(value, fail("matrix_failed", "one variant failed"))
	if r.OK || r.Error == nil || r.Result == nil {
		t.Fatalf("partial report lost: %+v", r)
	}
}
func TestMatrixBuildFailureRetainsEveryRow(t *testing.T) {
	s := fixtureService(t)
	m := Matrix{Name: "test", Runtime: "runtime-A", DeviceType: "type-A", Build: Params{Project: fixtureProject(t), Scheme: "App"}, Variants: []Variant{{Name: "one"}, {Name: "two"}}}
	value, err := s.matrix(context.Background(), fixtureManifest(t, m))
	var api *APIError
	if !errors.As(err, &api) || api.Code != "matrix_failed" {
		t.Errorf("expected matrix_failed, got %v", err)
	}
	report := reportJSON(t, value)
	rows, _ := report["results"].([]any)
	if len(rows) != 2 {
		t.Fatalf("failed build lost rows: %#v", report)
	}
	if rows[0].(map[string]any)["status"] != "failed" || rows[1].(map[string]any)["status"] != "skipped" {
		t.Fatalf("wrong dependency outcomes: %#v", rows)
	}
	for _, row := range rows {
		build, ok := row.(map[string]any)["build"].(map[string]any)
		if !ok {
			t.Fatal("missing build failure evidence")
		}
		if _, err := os.Stat(build["log"].(string)); err != nil {
			t.Fatal(err)
		}
	}
	if report["status"] != "failed" {
		t.Errorf("missing failed aggregate: %#v", report)
	}
	path, _ := report["report"].(string)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("missing persisted report: %v", err)
	}
}
func TestMatrixRejectsAliasedAndNestedOutputClaimsBeforeBuild(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprint(nested), func(t *testing.T) {
			s := fixtureService(t)
			base := t.TempDir()
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(base, alias); err != nil {
				t.Fatal(err)
			}
			first := Params{Project: fixtureProject(t), Scheme: "App", DerivedData: filepath.Join(base, "output")}
			second := first
			second.BuildSettings = map[string]string{"FEATURE": "B"}
			second.DerivedData = filepath.Join(alias, "output")
			if nested {
				second.DerivedData = filepath.Join(second.DerivedData, "nested")
			}
			calls := 0
			s.buildCommand = func(*exec.Cmd) error { calls++; return errors.New("build should not run") }
			_, err := s.matrix(context.Background(), fixtureManifest(t, Matrix{Name: "test", Build: first, Variants: []Variant{{Name: "one"}, {Name: "two", Build: &second}}}))
			if err == nil || calls != 0 {
				t.Fatalf("conflicting claims reached build: calls=%d err=%v", calls, err)
			}
		})
	}
}
func TestBuildProductsSurviveLaterBuild(t *testing.T) {
	s := fixtureService(t)
	a := Params{Project: fixtureProject(t), Scheme: "App", DerivedData: filepath.Join(t.TempDir(), "output")}
	marker := "A"
	s.buildCommand = func(cmd *exec.Cmd) error {
		dir := ""
		configuration := ""
		for i, arg := range cmd.Args {
			if arg == "-derivedDataPath" {
				dir = cmd.Args[i+1]
			}
			if arg == "-configuration" {
				configuration = cmd.Args[i+1]
			}
		}
		app := filepath.Join(dir, "Build", "Products", configuration+"-iphonesimulator", "App.app")
		if err := os.MkdirAll(app, 0700); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(cmd.Stdout, "build "+marker); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(app, "marker"), []byte(marker), 0600)
	}
	first, err := s.build(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	marker = "B"
	a.BuildSettings = map[string]string{"FEATURE": "B"}
	second, err := s.build(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range []any{first, second} {
		b := item.(BuildResult)
		got, err := os.ReadFile(filepath.Join(b.Apps[0].Path, "marker"))
		want := []string{"A", "B"}[i]
		if err != nil || string(got) != want {
			t.Errorf("build %s changed: marker=%q err=%v", want, got, err)
		}
		log, err := os.ReadFile(b.Log)
		if err != nil || !strings.Contains(string(log), "build "+want) {
			t.Errorf("build %s log changed: %q %v", want, log, err)
		}
	}
}

func TestConcurrentMatrixReservationsCreateOnlyOneManagedPhone(t *testing.T) {
	s := fixtureService(t)
	var creates atomic.Int32
	s.runCommand = func(_ context.Context, program string, args ...string) ([]byte, error) {
		if program != "/usr/bin/xcrun" {
			return nil, fmt.Errorf("unexpected command %s", program)
		}
		switch strings.Join(args[:2], " ") {
		case "simctl create":
			creates.Add(1)
			return []byte("managed"), nil
		case "simctl list":
			return []byte(`{"devices":{"runtime-A":[{"udid":"managed","name":"agent-phone · test / one","deviceTypeIdentifier":"type-A","isAvailable":true}]}}`), nil
		default:
			return nil, fmt.Errorf("unexpected command %v", args)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			a, err := s.reserveMatrixTarget(context.Background(), matrixTarget{"test / one", "runtime-A", "type-A"}, "fixture")
			if err == nil && a.Device != "managed" {
				err = fmt.Errorf("wrong reserved target %q", a.Device)
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success, busy := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		var api *APIError
		if errors.As(err, &api) && api.Code == "device_busy" {
			busy++
		} else {
			t.Errorf("unexpected reservation error: %v", err)
		}
	}
	if creates.Load() != 1 || success != 1 || busy != 1 {
		t.Fatalf("creates=%d success=%d busy=%d", creates.Load(), success, busy)
	}
}
func TestBuildCanonicalSpecsAndOutputSettings(t *testing.T) {
	s := fixtureService(t)
	project := fixtureProject(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(project), alias); err != nil {
		t.Fatal(err)
	}
	a, err := s.normalizeBuild(Params{Project: project, Scheme: "App"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.normalizeBuild(Params{Project: filepath.Join(alias, filepath.Base(project)), Scheme: "App", Configuration: "Debug", BuildSettings: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if buildKey(a) != buildKey(b) || a.DerivedData != b.DerivedData {
		t.Fatalf("equivalent build specs diverged: %+v %+v", a, b)
	}
	for _, key := range []string{"SYMROOT", "CONFIGURATION_BUILD_DIR", "OBJROOT", "BUILD_DIR", "TARGET_BUILD_DIR", "WRAPPER_NAME"} {
		a.BuildSettings = map[string]string{key: "/elsewhere"}
		if _, err := s.build(context.Background(), a); err == nil {
			t.Fatalf("accepted output redirect %s", key)
		}
	}
}
func TestHostBuildLockSerializesServiceHomesAndCancelsWait(t *testing.T) {
	first, second := fixtureService(t), fixtureService(t)
	a := Params{Project: fixtureProject(t), Scheme: "App"}
	entered := make(chan struct{})
	release := make(chan struct{})
	first.buildCommand = func(*exec.Cmd) error { close(entered); <-release; return errors.New("first fixture finished") }
	var secondCalls atomic.Int32
	second.buildCommand = func(*exec.Cmd) error { secondCalls.Add(1); return errors.New("second fixture finished") }
	done := make(chan error, 1)
	go func() { _, err := first.build(context.Background(), a); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first build never entered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := second.build(ctx, a)
	close(release)
	<-done
	if !errors.Is(err, context.DeadlineExceeded) || secondCalls.Load() != 0 {
		t.Fatalf("host lock did not cancel waiting build: calls=%d err=%v", secondCalls.Load(), err)
	}
	_, err = second.build(context.Background(), a)
	if err == nil || secondCalls.Load() != 1 {
		t.Fatalf("host lock did not release: calls=%d err=%v", secondCalls.Load(), err)
	}
}
func TestMatrixMixedOutcomesPreserveCleanupFailure(t *testing.T) {
	s := fixtureService(t)
	report := MatrixReport{ID: "fixture", Name: "test", Results: []VariantResult{{Name: "passed", Status: "passed", Observation: &Observation{ID: "evidence"}}, {Name: "observed", Status: "observed"}, {Name: "cleanup", Status: "failed", Error: "cleanup shutdown: fixture failure"}, {Name: "dependency", Status: "skipped", Error: "build failed"}}}
	value, err := s.finishMatrix(report)
	var api *APIError
	if !errors.As(err, &api) || api.Code != "matrix_failed" {
		t.Fatalf("mixed matrix returned success: %v", err)
	}
	got := value.(MatrixReport)
	if got.Counts != (MatrixCounts{Passed: 1, Observed: 1, Failed: 1, Skipped: 1}) {
		t.Fatalf("counts=%+v", got.Counts)
	}
	raw, err := os.ReadFile(got.Report)
	if err != nil {
		t.Fatal(err)
	}
	var saved MatrixReport
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Results) != 4 || saved.Results[0].Observation.ID != "evidence" || saved.Results[2].Error != report.Results[2].Error {
		t.Fatalf("partial evidence lost: %+v", saved)
	}
}

func TestMatrixTargetPreflightStopsBeforeBuild(t *testing.T) {
	s := fixtureService(t)
	s.phones["managed"] = &Phone{UDID: "managed", Name: "test / one"}
	s.runCommand = func(_ context.Context, program string, args ...string) ([]byte, error) {
		if program != "/usr/bin/xcrun" || strings.Join(args, " ") != "simctl list devices --json" {
			return nil, fmt.Errorf("unexpected command %s %v", program, args)
		}
		return []byte(`{"devices":{"runtime-B":[{"udid":"managed","name":"agent-phone · test / one","deviceTypeIdentifier":"type-A","isAvailable":true}]}}`), nil
	}
	calls := 0
	s.buildCommand = func(*exec.Cmd) error { calls++; return errors.New("unexpected build") }
	m := Matrix{Name: "test", Runtime: "runtime-A", DeviceType: "type-A", Build: Params{Project: fixtureProject(t), Scheme: "App"}, Variants: []Variant{{Name: "one"}, {Name: "two"}}}
	value, err := s.matrix(context.Background(), fixtureManifest(t, m))
	if err == nil || calls != 0 {
		t.Fatalf("incompatible target reached build: calls=%d err=%v", calls, err)
	}
	report := value.(MatrixReport)
	if report.Results[0].Status != "failed" || report.Results[1].Status != "skipped" {
		t.Fatalf("preflight rows=%+v", report.Results)
	}
}
