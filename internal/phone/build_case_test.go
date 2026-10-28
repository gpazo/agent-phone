package phone

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func caseAliasFixture(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	actual := filepath.Join(parent, "ExistingAncestor")
	if err := os.Mkdir(actual, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "existingancestor")
	a, err := os.Stat(actual)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(alias)
	if os.IsNotExist(err) {
		t.Skip("fixture volume uses case-sensitive names")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, b) {
		t.Fatal("fixture aliases do not identify the same directory")
	}
	return actual, alias
}

func TestMatrixRejectsCaseAliasedOutputClaimsBeforeBuild(t *testing.T) {
	for _, name := range []string{"existing", "missing", "nested"} {
		t.Run(name, func(t *testing.T) {
			actual, alias := caseAliasFixture(t)
			firstOutput := filepath.Join(actual, "Output")
			secondOutput := filepath.Join(alias, "output")
			if name == "existing" {
				if err := os.Mkdir(firstOutput, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if name == "nested" {
				secondOutput = filepath.Join(secondOutput, "Nested")
			}
			s := fixtureService(t)
			first := Params{Project: fixtureProject(t), Scheme: "App", DerivedData: firstOutput}
			second := first
			second.DerivedData = secondOutput
			second.BuildSettings = map[string]string{"FEATURE": "B"}
			calls := 0
			s.buildCommand = func(*exec.Cmd) error { calls++; return errors.New("fixture refuses Xcode") }
			m := Matrix{Name: "test", Runtime: "runtime-A", DeviceType: "type-A", Build: first, Variants: []Variant{{Name: "one"}, {Name: "two", Build: &second}}}
			value, err := s.matrix(context.Background(), fixtureManifest(t, m))
			if err == nil || calls != 0 {
				t.Fatalf("case aliases reached build: calls=%d err=%v", calls, err)
			}
			report := value.(MatrixReport)
			if !strings.Contains(report.Results[0].Error, "overlapping derivedData") {
				t.Fatalf("expected output collision, got %+v", report.Results)
			}
		})
	}
}

func TestBuildCanonicalizesCaseAliasedProjectIdentity(t *testing.T) {
	actual, alias := caseAliasFixture(t)
	project := filepath.Join(actual, "App.xcodeproj")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	s := fixtureService(t)
	a, err := s.normalizeBuild(Params{Project: project, Scheme: "App"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.normalizeBuild(Params{Project: filepath.Join(alias, "app.xcodeproj"), Scheme: "App"})
	if err != nil {
		t.Fatal(err)
	}
	if buildKey(a) != buildKey(b) || a.Project != b.Project || a.DerivedData != b.DerivedData {
		t.Fatalf("same project got distinct identities: %+v %+v", a, b)
	}
}

func TestCaseInsensitivePathsPreserveExistingUnicodeAndRejectMissingUnicode(t *testing.T) {
	actual, _ := caseAliasFixture(t)
	project := filepath.Join(actual, "Écran.xcodeproj")
	output := filepath.Join(actual, "Résultats")
	for _, path := range []string{project, output} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s := fixtureService(t)
	a, err := s.normalizeBuild(Params{Project: project, Scheme: "App", DerivedData: output})
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{project, a.Project}, {output, a.DerivedData}} {
		first, err := os.Stat(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.Stat(pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(first, second) {
			t.Fatalf("existing Unicode path changed: %q %q", pair[0], pair[1])
		}
	}
	a.DerivedData = filepath.Join(output, "NewASCII")
	if _, err = s.normalizeBuild(a); err != nil {
		t.Fatalf("ASCII child of existing Unicode directory rejected: %v", err)
	}
	a.DerivedData = filepath.Join(actual, "Résultats-Neufs")
	calls := 0
	s.buildCommand = func(*exec.Cmd) error { calls++; return errors.New("fixture refuses Xcode") }
	_, err = s.build(context.Background(), a)
	if err == nil || !strings.Contains(err.Error(), "create non-ASCII output directory before use") || calls != 0 {
		t.Fatalf("standalone missing Unicode output: calls=%d err=%v", calls, err)
	}
	m := Matrix{Name: "test", Runtime: "runtime-A", DeviceType: "type-A", Build: a, Variants: []Variant{{Name: "one"}}}
	value, err := s.matrix(context.Background(), fixtureManifest(t, m))
	if err == nil || calls != 0 {
		t.Fatalf("matrix missing Unicode output: calls=%d err=%v", calls, err)
	}
	report := value.(MatrixReport)
	if !strings.Contains(report.Results[0].Error, "create non-ASCII output directory before use") {
		t.Fatalf("missing actionable path error: %+v", report.Results)
	}
}

func TestCaseSensitiveOutputPathsRemainDistinct(t *testing.T) {
	parent := t.TempDir()
	upper := filepath.Join(parent, "Output")
	lower := filepath.Join(parent, "output")
	if err := os.Mkdir(upper, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lower); err == nil {
		t.Skip("fixture volume uses case-insensitive names")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	s := fixtureService(t)
	project := fixtureProject(t)
	a, err := s.normalizeBuild(Params{Project: project, Scheme: "App", DerivedData: upper})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.normalizeBuild(Params{Project: project, Scheme: "App", DerivedData: lower, BuildSettings: map[string]string{"FEATURE": "B"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.DerivedData == b.DerivedData {
		t.Fatal("case-sensitive output names collapsed")
	}
	if err = validateBuildClaims([]Params{a, b}); err != nil {
		t.Fatalf("distinct case-sensitive outputs conflicted: %v", err)
	}
}
