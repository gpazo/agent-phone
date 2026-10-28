package phone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Matrix struct {
	Name        string    `json:"name"`
	Build       Params    `json:"build"`
	DeviceType  string    `json:"deviceType"`
	Runtime     string    `json:"runtime"`
	Concurrency int       `json:"concurrency"`
	KeepRunning bool      `json:"keepRunning"`
	Variants    []Variant `json:"variants"`
}
type Variant struct {
	Name        string   `json:"name"`
	Appearance  string   `json:"appearance"`
	ContentSize string   `json:"contentSize"`
	Arguments   []string `json:"arguments"`
	Build       *Params  `json:"build,omitempty"`
	Steps       []Step   `json:"steps"`
}
type Step struct {
	Action     string `json:"action"`
	Label      string `json:"label,omitempty"`
	Identifier string `json:"identifier,omitempty"`
	Text       string `json:"text,omitempty"`
	Expect     string `json:"expect,omitempty"`
}
type VariantResult struct {
	Name        string       `json:"name"`
	Device      string       `json:"device,omitempty"`
	Status      string       `json:"status"`
	Error       string       `json:"error,omitempty"`
	Build       *BuildResult `json:"build,omitempty"`
	Observation *Observation `json:"observation,omitempty"`
	ElapsedMS   int64        `json:"elapsedMs"`
}

type MatrixCounts struct {
	Passed   int `json:"passed"`
	Observed int `json:"observed"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`
}
type MatrixReport struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Status      string          `json:"status"`
	Counts      MatrixCounts    `json:"counts"`
	CompletedAt time.Time       `json:"completedAt"`
	Results     []VariantResult `json:"results"`
	Report      string          `json:"report"`
}
type matrixTarget struct{ Name, Runtime, DeviceType string }

func (s *Service) matchingMatrixTarget(ctx context.Context, target matrixTarget) (string, error) {
	s.mu.Lock()
	var matches []string
	for _, p := range s.phones {
		if p.Name == target.Name {
			matches = append(matches, p.UDID)
		}
	}
	s.mu.Unlock()
	if len(matches) == 0 {
		return "", nil
	}
	if len(matches) != 1 {
		return "", fail("matrix_target", "matrix target has multiple registered devices: "+target.Name)
	}
	raw, err := s.command(ctx, "/usr/bin/xcrun", "simctl", "list", "devices", "--json")
	if err != nil {
		return "", fmt.Errorf("matrix target catalog: %w", err)
	}
	var catalog struct {
		Devices map[string][]struct {
			UDID       string `json:"udid"`
			Name       string `json:"name"`
			DeviceType string `json:"deviceTypeIdentifier"`
			Available  bool   `json:"isAvailable"`
		} `json:"devices"`
	}
	if err = json.Unmarshal(raw, &catalog); err != nil {
		return "", fmt.Errorf("matrix target catalog: %w", err)
	}
	found := 0
	for runtime, devices := range catalog.Devices {
		for _, device := range devices {
			if device.UDID != matches[0] {
				continue
			}
			found++
			if !device.Available || device.Name != "agent-phone · "+target.Name || runtime != target.Runtime || device.DeviceType != target.DeviceType {
				return "", fail("matrix_target", "matrix target is unavailable or its live name, runtime, or device type differs: "+target.Name)
			}
		}
	}
	if found != 1 {
		return "", fail("matrix_target", "matrix target is missing or ambiguous in the live catalog: "+target.Name)
	}
	return matches[0], nil
}
func (s *Service) reserveMatrixTarget(ctx context.Context, target matrixTarget, owner string) (Params, error) {
	s.matrixMu.Lock()
	defer s.matrixMu.Unlock()
	id, err := s.matchingMatrixTarget(ctx, target)
	if err != nil {
		return Params{}, err
	}
	if id == "" {
		value, err := s.create(ctx, Params{Name: target.Name, Runtime: target.Runtime, DeviceType: target.DeviceType})
		if err != nil {
			return Params{}, err
		}
		id = value.(*Phone).UDID
		if _, err = s.matchingMatrixTarget(ctx, target); err != nil {
			return Params{}, err
		}
	}
	value, err := s.Call(ctx, Request{"acquire", Params{Device: id, Owner: owner}})
	if err != nil {
		return Params{}, err
	}
	return Params{Device: id, Lease: value.(*Lease).Token}, nil
}
func (s *Service) finishMatrix(report MatrixReport) (any, error) {
	report.CompletedAt = time.Now()
	report.Status = "passed"
	for _, r := range report.Results {
		switch r.Status {
		case "passed":
			report.Counts.Passed++
		case "observed":
			report.Counts.Observed++
		case "failed":
			report.Counts.Failed++
		case "skipped":
			report.Counts.Skipped++
		}
	}
	if report.Counts.Failed > 0 || report.Counts.Skipped > 0 {
		report.Status = "failed"
	}
	report.Report = filepath.Join(s.data, "runs", report.ID+".json")
	if err := mkdir(filepath.Dir(report.Report)); err != nil {
		return report, err
	}
	if err := atomicJSON(report.Report, report); err != nil {
		return report, err
	}
	if report.Status == "failed" {
		return report, fail("matrix_failed", fmt.Sprintf("matrix failed: %d failed, %d skipped; report: %s", report.Counts.Failed, report.Counts.Skipped, report.Report))
	}
	return report, nil
}
func (s *Service) matrix(ctx context.Context, path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Matrix
	if err = json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if len(m.Variants) == 0 || len(m.Variants) > 32 {
		return nil, fmt.Errorf("matrix requires 1–32 variants")
	}
	names := map[string]bool{}
	for _, v := range m.Variants {
		if v.Name == "" || names[v.Name] {
			return nil, fmt.Errorf("variant names must be nonempty and unique")
		}
		names[v.Name] = true
	}
	if m.Concurrency == 0 {
		m.Concurrency = 2
	}
	if m.Concurrency < 1 || m.Concurrency > 4 {
		return nil, fmt.Errorf("concurrency must be 1–4")
	}
	report := MatrixReport{ID: randomID(), Name: m.Name, Results: make([]VariantResult, len(m.Variants))}
	for i, v := range m.Variants {
		report.Results[i] = VariantResult{Name: v.Name, Status: "skipped", Error: "matrix preflight failed"}
	}
	specs := make([]Params, len(m.Variants))
	preflightFailed := false
	for i, v := range m.Variants {
		a := m.Build
		if v.Build != nil {
			a = *v.Build
		}
		if a.Project != "" && !filepath.IsAbs(a.Project) {
			a.Project = filepath.Join(filepath.Dir(path), a.Project)
		}
		if a.DerivedData != "" && !filepath.IsAbs(a.DerivedData) {
			a.DerivedData = filepath.Join(filepath.Dir(path), a.DerivedData)
		}
		specs[i], err = s.normalizeBuild(a)
		if err != nil {
			report.Results[i].Status = "failed"
			report.Results[i].Error = err.Error()
			preflightFailed = true
		}
	}
	if preflightFailed {
		return s.finishMatrix(report)
	}
	if err = validateBuildClaims(specs); err != nil {
		report.Results[0].Status = "failed"
		report.Results[0].Error = err.Error()
		return s.finishMatrix(report)
	}
	s.matrixMu.Lock()
	for i, v := range m.Variants {
		target := matrixTarget{m.Name + " / " + v.Name, m.Runtime, m.DeviceType}
		if len(target.Name) > 80 || m.Runtime == "" || m.DeviceType == "" {
			err = fmt.Errorf("matrix target name must fit 80 characters; runtime and deviceType are required")
		} else {
			_, err = s.matchingMatrixTarget(ctx, target)
		}
		if err != nil {
			report.Results[i].Status = "failed"
			report.Results[i].Error = err.Error()
			preflightFailed = true
		}
	}
	s.matrixMu.Unlock()
	if preflightFailed {
		return s.finishMatrix(report)
	}
	type buildOutcome struct {
		result BuildResult
		err    error
	}
	cache := map[string]buildOutcome{}
	builds := make([]BuildResult, len(m.Variants))
	var runnable []int
	for i, a := range specs {
		key := buildKey(a)
		outcome, cached := cache[key]
		if !cached {
			value, err := s.build(ctx, a)
			outcome.err = err
			if value != nil {
				outcome.result = value.(BuildResult)
			}
			cache[key] = outcome
		}
		builds[i] = outcome.result
		if outcome.err != nil {
			report.Results[i].Status = "failed"
			if cached {
				report.Results[i].Status = "skipped"
			}
			report.Results[i].Error = outcome.err.Error()
			report.Results[i].Build = &builds[i]
			continue
		}
		runnable = append(runnable, i)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range m.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				report.Results[i] = s.runVariant(ctx, report.ID, m, m.Variants[i], builds[i])
			}
		}()
	}
	for _, i := range runnable {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return s.finishMatrix(report)
}
func (s *Service) runVariant(ctx context.Context, runID string, m Matrix, v Variant, b BuildResult) (r VariantResult) {
	start := time.Now()
	r = VariantResult{Name: v.Name, Status: "failed", Build: &b}
	defer func() { r.ElapsedMS = time.Since(start).Milliseconds() }()
	bad := func(err error) VariantResult { r.Error = err.Error(); return r }
	if len(b.Apps) != 1 {
		return bad(fmt.Errorf("demo matrix requires exactly one app product, got %d", len(b.Apps)))
	}
	a, err := s.reserveMatrixTarget(ctx, matrixTarget{m.Name + " / " + v.Name, m.Runtime, m.DeviceType}, "matrix:"+runID+":"+v.Name)
	if err != nil {
		return bad(err)
	}
	r.Device = a.Device
	var value any
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if !m.KeepRunning {
			if _, err := s.Call(cleanup, Request{"shutdown", a}); err != nil {
				r.Status = "failed"
				r.Error += " cleanup shutdown: " + err.Error()
			}
		}
		if _, err := s.Call(cleanup, Request{"release", a}); err != nil {
			r.Status = "failed"
			r.Error += " cleanup release: " + err.Error()
		}
	}()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		_, err = s.Call(ctx, Request{"boot", a})
		if err == nil {
			break
		}
		var api *APIError
		if !errors.As(err, &api) || (api.Code != "capacity" && api.Code != "memory_pressure") || m.KeepRunning || time.Now().After(deadline) {
			return bad(err)
		}
		s.event(r.Device, "queued", err.Error())
		select {
		case <-ctx.Done():
			return bad(ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	config := a
	config.Appearance = v.Appearance
	config.ContentSize = v.ContentSize
	if _, err = s.Call(ctx, Request{"configure", config}); err != nil {
		return bad(err)
	}
	install := a
	install.App = b.Apps[0].Path
	if _, err = s.Call(ctx, Request{"install", install}); err != nil {
		return bad(err)
	}
	launch := a
	launch.BundleID = b.Apps[0].BundleID
	launch.Arguments = v.Arguments
	if _, err = s.Call(ctx, Request{"launch", launch}); err != nil {
		return bad(err)
	}
	observe := a
	observe.Screenshot = true
	ready, stopWaiting := context.WithTimeout(ctx, 30*time.Second)
	defer stopWaiting()
	var obs *Observation
	for {
		value, err = s.Call(ready, Request{"observe", observe})
		if err == nil {
			obs = value.(*Observation)
			break
		}
		select {
		case <-ready.Done():
			return bad(fmt.Errorf("app accessibility did not become ready: %w (last observation: %v)", ready.Err(), err))
		case <-time.After(500 * time.Millisecond):
		}
	}
	r.Observation = obs
	for _, step := range v.Steps {
		if step.Action == "observe" {
			value, err = s.Call(ctx, Request{"observe", observe})
			if err != nil {
				return bad(err)
			}
			obs = value.(*Observation)
		}
		if step.Action != "observe" && step.Action != "assert" {
			var matches []Element
			for _, e := range obs.Elements {
				if e.Actionable && ((step.Identifier != "" && e.Identifier == step.Identifier) || (step.Identifier == "" && step.Label != "" && e.Label == step.Label)) {
					matches = append(matches, e)
				}
			}
			if len(matches) != 1 {
				return bad(fmt.Errorf("step %s: expected one target %q, got %d", step.Action, step.Label, len(matches)))
			}
			action := a
			action.Action = step.Action
			action.Text = step.Text
			action.Ref = matches[0].Ref
			action.Observation = obs.ID
			action.Screenshot = true
			reply, err := s.Call(ctx, Request{"act", action})
			if err != nil {
				return bad(err)
			}
			state := reply.(map[string]any)
			obs, _ = state["observation"].(*Observation)
			if obs == nil {
				return bad(fmt.Errorf("input acknowledged but observation failed: %v", state["observationError"]))
			}
			r.Observation = obs
		}
		if step.Expect != "" {
			found := false
			for _, e := range obs.Elements {
				if e.Label == step.Expect || e.Value == step.Expect {
					found = true
					break
				}
			}
			if !found {
				return bad(fmt.Errorf("expected visible text %q", step.Expect))
			}
		}
	}
	r.Status = "observed"
	if len(v.Steps) > 0 {
		r.Status = "passed"
	}
	r.Observation = obs
	return r
}
