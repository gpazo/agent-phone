package phone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func strptr(s string) *string { return &s }
func intptr(n int) *int       { return &n }
func treeCondition(label string) Condition {
	return Condition{Where: Selector{Label: strptr(label)}, Evidence: "tree"}
}
func waitFixture(t *testing.T) (*Service, *Phone, *fixtureBridge, Params, map[string]any, map[string]any) {
	t.Helper()
	s, p, b, root, target := nativeFixture(t)
	p.lease = &Lease{Token: "lease", Expires: time.Now().Add(time.Minute)}
	s.phones = map[string]*Phone{p.UDID: p}
	return s, p, b, Params{Device: p.UDID, Lease: "lease", Conditions: []Condition{treeCondition("Search")}}, root, target
}
func errorCode(err error) string {
	var e *APIError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

type scriptedBridge struct {
	fixtureBridge
	read func(context.Context) (map[string]any, error)
}

func (b *scriptedBridge) Tree(ctx context.Context) (map[string]any, error) { return b.read(ctx) }

func TestWaitAllAnyUseOneSample(t *testing.T) {
	for _, mode := range []string{"all", "any"} {
		t.Run(mode, func(t *testing.T) {
			s, p, _, a, root, _ := waitFixture(t)
			a.Conditions = []Condition{treeCondition("first"), treeCondition("second")}
			a.Mode = strptr(mode)
			a.TimeoutMs = intptr(350)
			reads := 0
			b := &scriptedBridge{read: func(context.Context) (map[string]any, error) {
				reads++
				label := "first"
				if reads%2 == 0 {
					label = "second"
				}
				root[ax+"Children"] = []any{nativeNode(48, label)}
				return map[string]any{"tree": root}, nil
			}}
			p.bridge = b
			value, err := s.Call(context.Background(), Request{"wait", a})
			r := value.(*WaitResult)
			if mode == "any" {
				if err != nil || r.Observation == nil || r.Attempts != 1 || len(r.Satisfied) != 1 || r.Satisfied[0] != 0 || p.observation != r.Observation {
					t.Fatalf("any: %+v %v", r, err)
				}
			} else {
				if errorCode(err) != "wait_timeout" || r.Observation != nil || r.LastObservation == nil || len(r.Satisfied) != 1 || r.Satisfied[0] != 1 || r.Attempts != 2 || p.observation != nil || p.lastObservation != nil {
					t.Fatalf("all: %+v %v", r, err)
				}
			}
			if b.inputs != 0 {
				t.Fatal("wait dispatched input")
			}
		})
	}
}
func TestWaitAdmissionTimeoutAndCancelDoNotMutate(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			s, p, b, a, _, _ := waitFixture(t)
			old := &Observation{ID: "old"}
			p.observation = old
			expiry := p.lease.Expires
			p.mu.Lock()
			a.TimeoutMs = intptr(20)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			}
			value, err := s.Call(ctx, Request{"wait", a})
			if errorCode(err) != "wait_timeout" && (!cancelled || errorCode(err) != "cancelled") {
				t.Fatal(err)
			}
			if cancelled && errorCode(err) != "cancelled" {
				t.Fatal(err)
			}
			r := value.(*WaitResult)
			if r.Attempts != 0 || p.observation != old || p.bridge != b || !p.lease.Expires.Equal(expiry) {
				t.Fatalf("admission mutated phone: %+v", r)
			}
			p.mu.Unlock()
			if !p.mu.TryLock() {
				t.Fatal("abandoned waiter owns phone")
			}
			p.mu.Unlock()
		})
	}
}
func TestWaitRejectsLateBridgeAndScreenshotSuccess(t *testing.T) {
	for _, screenshot := range []bool{false, true} {
		t.Run(fmt.Sprint(screenshot), func(t *testing.T) {
			s, p, b, a, _, _ := waitFixture(t)
			a.TimeoutMs = intptr(15)
			a.Screenshot = screenshot
			if screenshot {
				s.runCommand = func(context.Context, string, ...string) ([]byte, error) {
					time.Sleep(25 * time.Millisecond)
					return nil, nil
				}
			} else {
				p.bridge = &scriptedBridge{read: func(context.Context) (map[string]any, error) { time.Sleep(25 * time.Millisecond); return b.tree, nil }}
			}
			value, err := s.Call(context.Background(), Request{"wait", a})
			r := value.(*WaitResult)
			if errorCode(err) != "wait_timeout" || r.Observation != nil || (r.LastObservation != nil) != screenshot || p.observation != nil || p.lastObservation != nil {
				t.Fatalf("accepted late sample: %+v %v", r, err)
			}
		})
	}
}
func TestWaitParentDeadlineAndTerminalTransport(t *testing.T) {
	for _, transportError := range []bool{false, true} {
		t.Run(fmt.Sprint(transportError), func(t *testing.T) {
			s, p, _, a, root, _ := waitFixture(t)
			a.Conditions = []Condition{treeCondition("absent")}
			reads := 0
			b := &scriptedBridge{read: func(context.Context) (map[string]any, error) {
				reads++
				if transportError {
					return nil, errors.New("broken native protocol")
				}
				return map[string]any{"tree": root}, nil
			}}
			p.bridge = b
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			value, err := s.Call(ctx, Request{"wait", a})
			r := value.(*WaitResult)
			if err == nil || reads != 1 || r.Attempts != 1 || p.observation != nil {
				t.Fatalf("unexpected result: %+v %v", r, err)
			}
			if transportError {
				if errorCode(err) == "wait_timeout" || !b.closed || r.LastObservation != nil {
					t.Fatalf("transport retried: %+v %v", r, err)
				}
			} else if errorCode(err) != "wait_timeout" || r.LastObservation == nil || b.closed {
				t.Fatalf("parent cutoff: %+v %v", r, err)
			}
		})
	}
}
func TestPassiveNativeHitNeverEnablesInput(t *testing.T) {
	s, p, b, a, _, target := waitFixture(t)
	target[ax+"AutomationType"] = float64(48)
	b.receipt["tree"].(map[string]any)[ax+"AutomationType"] = float64(48)
	a.Conditions[0].Evidence = "native-hit"
	value, err := s.Call(context.Background(), Request{"wait", a})
	if err != nil {
		t.Fatal(err)
	}
	o := value.(*WaitResult).Observation
	e := searchTarget(t, o)
	if e.NativeHitAt == nil || e.NativeHitAt.Before(o.CapturedAt) || e.Actionable || len(e.Actions) > 0 || e.nativeToken != "" || b.captures != 1 {
		t.Fatalf("passive evidence: %+v", e)
	}
	a.Observation = o.ID
	a.Ref = e.Ref
	a.Action = "press"
	if _, err = s.Call(context.Background(), Request{"act", a}); errorCode(err) != "invalid_target" || b.inputs != 0 {
		t.Fatalf("passive input allowed: %v", err)
	}
	if p.observation != o {
		t.Fatal("invalid input consumed observation")
	}
}
func TestWaitNativeHitRequiresMatchingReceiptAndCompleteUniqueLocalTree(t *testing.T) {
	changes := map[string]func(*fixtureBridge, map[string]any, map[string]any){
		"app":      func(b *fixtureBridge, _, _ map[string]any) { b.receipt["app"] = "Other" },
		"context":  func(b *fixtureBridge, _, _ map[string]any) { b.receipt["context"] = "Other" },
		"identity": func(b *fixtureBridge, _, _ map[string]any) { b.receipt["tree"].(map[string]any)[ax+"Label"] = "Other" },
		"token":    func(b *fixtureBridge, _, _ map[string]any) { b.receipt["token"] = "" },
		"incomplete": func(_ *fixtureBridge, root, _ map[string]any) {
			root[ax+"Children"] = append(root[ax+"Children"].([]any), 42)
		},
		"remote": func(_ *fixtureBridge, _, target map[string]any) { target[ax+"ElementType"] = "AXRemoteElement" },
		"duplicate": func(_ *fixtureBridge, root, target map[string]any) {
			copy := map[string]any{}
			for k, v := range target {
				copy[k] = v
			}
			copy[ax+"Frame"] = nativeFrame(120, 30)
			root[ax+"Children"] = append(root[ax+"Children"].([]any), copy)
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			s, _, b, a, root, target := waitFixture(t)
			change(b, root, target)
			a.Conditions[0].Evidence = "native-hit"
			a.TimeoutMs = intptr(10)
			value, err := s.Call(context.Background(), Request{"wait", a})
			r := value.(*WaitResult)
			if errorCode(err) != "wait_timeout" || r.LastObservation == nil || len(r.Satisfied) != 0 || searchTarget(t, r.LastObservation).NativeHitAt != nil {
				t.Fatalf("invalid native evidence: %+v %v", r, err)
			}
		})
	}
}
func TestWaitNativeCandidateBudgetAndPriority(t *testing.T) {
	for _, count := range []int{250, 251} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, p, b, a, root, target := waitFixture(t)
			children := []any{}
			for i := 0; i < count; i++ {
				node := nativeNode(48, fmt.Sprintf("passive-%d", i))
				children = append(children, node)
			}
			children = append(children, target)
			root[ax+"Children"] = children
			a.Conditions = []Condition{{Where: Selector{Role: strptr("text")}, Evidence: "native-hit"}}
			a.TimeoutMs = intptr(10)
			if count == 250 {
				a.Conditions = []Condition{{Where: Selector{Label: strptr("Search"), Action: strptr("press")}, Evidence: "native-hit"}}
			}
			value, err := s.Call(context.Background(), Request{"wait", a})
			r := value.(*WaitResult)
			if count == 251 {
				if errorCode(err) != "selector_too_broad" || b.captures != 0 || p.observation != nil {
					t.Fatalf("broad selector silently starved: %+v %v", r, err)
				}
			} else if err != nil || b.captures != 1 {
				t.Fatalf("target starved: %+v %v", r, err)
			}
		})
	}
}
func TestWaitValidationAndExactValues(t *testing.T) {
	invalid := []string{`{}`, `{"conditions":[]}`, `{"conditions":[{"where":{"label":"x"},"evidence":"tree"}],"mode":""}`, `{"conditions":[{"where":{},"evidence":"tree"}]}`, `{"conditions":[{"where":{"label":""},"evidence":"visible"}]}`, `{"conditions":[{"where":{"role":"bogus"},"evidence":"tree"}]}`, `{"conditions":[{"where":{"action":"press"},"evidence":"tree"}]}`, `{"conditions":[{"where":{"action":"delete"},"evidence":"native-hit"}]}`, `{"conditions":[{"where":{"label":""},"evidence":"tree"}],"mode":"none"}`, `{"conditions":[{"where":{"label":""},"evidence":"tree"}],"timeoutMs":0}`, `{"conditions":[{"where":{"label":""},"evidence":"tree"}],"timeoutMs":30001}`}
	for _, raw := range invalid {
		var a Params
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			t.Fatal(err)
		}
		if _, err := parseWait(a); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`{"conditions":[{"where":{"unknown":"x"},"evidence":"tree"}]}`, `{"conditions":[{"where":{"value":12},"evidence":"tree"}]}`, `{"conditions":[{"where":{"label":null},"evidence":"tree"}]}`, `{"conditions":[{"where":{"label":"x"},"evidence":"tree","unknown":true}]}`} {
		var a Params
		if err := json.Unmarshal([]byte(raw), &a); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, value := range []any{nil, 0, false, ""} {
		sel := Selector{Value: strptr("")}
		if sel.matches(Element{Value: value}, nil) != (value == "") {
			t.Fatalf("coerced %#v", value)
		}
	}
	a := Params{Conditions: make([]Condition, 17)}
	if _, err := parseWait(a); err == nil {
		t.Fatal("too many conditions")
	}
	if err := validateLease(&Lease{Token: "x", Expires: time.Now().Add(-time.Second)}, "x", time.Now()); err == nil {
		t.Fatal("expired lease")
	}
	now := time.Now()
	if err := validateLease(&Lease{Token: "x", Expires: now}, "x", now); err == nil {
		t.Fatal("expiry equality accepted")
	}
}

func TestWaitInternalHelperDeadlineRemainsTerminalFailure(t *testing.T) {
	s, p, _, a, _, _ := waitFixture(t)
	p.bridge = &scriptedBridge{read: func(context.Context) (map[string]any, error) { return nil, context.DeadlineExceeded }}
	value, err := s.Call(context.Background(), Request{"wait", a})
	response := Result(value, err)
	if response.OK || response.Error.Code != "operation_failed" || value.(*WaitResult).Attempts != 1 {
		t.Fatalf("internal deadline mislabeled: %+v", response)
	}
}
func TestWaitDurationIncludesScreenshot(t *testing.T) {
	s, _, _, a, _, _ := waitFixture(t)
	a.Screenshot = true
	s.runCommand = func(context.Context, string, ...string) ([]byte, error) {
		time.Sleep(15 * time.Millisecond)
		return nil, nil
	}
	value, err := s.Call(context.Background(), Request{"wait", a})
	if err != nil {
		t.Fatal(err)
	}
	o := value.(*WaitResult).Observation
	if o.DurationMS < 15 || o.ScreenshotAt == nil || !o.ScreenshotAt.After(o.CapturedAt) {
		t.Fatalf("screenshot not measured: %+v", o)
	}
}

type delayedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c delayedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }
func TestWaitRejectsLateSuccessBeforeContextTimerFires(t *testing.T) {
	s, p, b, a, _, _ := waitFixture(t)
	p.bridge = &scriptedBridge{read: func(context.Context) (map[string]any, error) { time.Sleep(20 * time.Millisecond); return b.tree, nil }}
	ctx := delayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(10 * time.Millisecond)}
	value, err := s.Call(ctx, Request{"wait", a})
	if errorCode(err) != "wait_timeout" || value.(*WaitResult).Observation != nil || p.observation != nil {
		t.Fatalf("accepted late success with delayed timer: %+v %v", value, err)
	}
}
func TestWaitKeepsLastCompleteSampleWhenLaterTransportFails(t *testing.T) {
	s, p, _, a, root, _ := waitFixture(t)
	a.Conditions = []Condition{treeCondition("missing")}
	calls := 0
	p.bridge = &scriptedBridge{read: func(context.Context) (map[string]any, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("protocol failed")
		}
		return map[string]any{"tree": root}, nil
	}}
	value, err := s.Call(context.Background(), Request{"wait", a})
	r := value.(*WaitResult)
	if err == nil || errorCode(err) == "wait_timeout" || r.LastObservation == nil || r.Attempts != 2 || p.observation != nil {
		t.Fatalf("lost partial failure: %+v %v", r, err)
	}
}
func TestNativeHitQueryPrioritizesPassiveTargetBeyondOrdinaryLimit(t *testing.T) {
	s, _, b, a, root, target := waitFixture(t)
	children := []any{}
	for i := 0; i < 300; i++ {
		children = append(children, nativeNode(9, fmt.Sprintf("ordinary-%d", i)))
	}
	target[ax+"AutomationType"] = float64(48)
	target[ax+"Frame"] = nativeFrame(220, 330)
	b.receipt["tree"].(map[string]any)[ax+"AutomationType"] = float64(48)
	children = append(children, target)
	root[ax+"Children"] = children
	a.Conditions = []Condition{{Where: Selector{Label: strptr("Search")}, Evidence: "native-hit"}, {Where: Selector{Label: strptr("Search")}, Evidence: "native-hit"}}
	value, err := s.Call(context.Background(), Request{"wait", a})
	if err != nil {
		t.Fatal(err)
	}
	r := value.(*WaitResult)
	if b.captures != 1 || len(r.Satisfied) != 2 || searchTarget(t, r.Observation).NativeHitAt == nil || searchTarget(t, r.Observation).Actionable {
		t.Fatalf("target lost behind ordinary candidates: %+v", r)
	}
}
