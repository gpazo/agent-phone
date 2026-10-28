package phone

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestWaitRetainsMatchedCaptureWhenScreenshotFails(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprint(late), func(t *testing.T) {
			s, p, b, a, _, _ := waitFixture(t)
			a.Conditions[0].Evidence = "native-hit"
			a.Screenshot = true
			a.TimeoutMs = intptr(25)
			s.runCommand = func(context.Context, string, ...string) ([]byte, error) {
				if late {
					time.Sleep(40 * time.Millisecond)
					return nil, nil
				}
				return nil, errors.New("screenshot failed")
			}
			value, err := s.Call(context.Background(), Request{"wait", a})
			r := value.(*WaitResult)
			envelope := Result(value, err)
			if err == nil || envelope.OK || r.Observation != nil || r.LastObservation == nil || len(r.Satisfied) != 1 || r.Satisfied[0] != 0 {
				t.Fatalf("matched capture lost: %+v %v", r, err)
			}
			o := r.LastObservation
			e := searchTarget(t, o)
			if e.NativeHitAt == nil || e.Actionable || len(e.Actions) != 0 || e.nativeToken != "" || o.Screenshot != "" || o.ScreenshotAt != nil || p.observation != nil || p.lastObservation != nil || b.inputs != 0 || b.captures != 1 {
				t.Fatalf("diagnostic has authority or late screenshot: %+v", r)
			}
			if late && errorCode(err) != "wait_timeout" {
				t.Fatal(err)
			}
			a.Observation, a.Ref, a.Action = o.ID, e.Ref, "press"
			if _, err = s.Call(context.Background(), Request{"act", a}); errorCode(err) != "stale_observation" || b.inputs != 0 {
				t.Fatalf("diagnostic permitted input: %v", err)
			}
		})
	}
}

type targetBatchBridge struct {
	fixtureBridge
	target any
}

func (b *targetBatchBridge) Action(ctx context.Context, r map[string]any) (map[string]any, error) {
	if r["action"] == "capture-targets" {
		b.captures++
		return map[string]any{"targets": []any{b.target}}, nil
	}
	return b.fixtureBridge.Action(ctx, r)
}
func TestWaitRejectsNonObjectNativeTargetAndAcceptsEmptyMiss(t *testing.T) {
	for _, target := range []any{nil, true, float64(7), "bad", []any{}, map[string]any{}} {
		t.Run(fmt.Sprintf("%T", target), func(t *testing.T) {
			s, p, _, a, root, _ := waitFixture(t)
			b := &targetBatchBridge{fixtureBridge: fixtureBridge{tree: map[string]any{"tree": root}}, target: target}
			p.bridge = b
			a.TimeoutMs = intptr(15)
			a.Conditions[0].Evidence = "native-hit"
			value, err := s.Call(context.Background(), Request{"wait", a})
			r := value.(*WaitResult)
			_, emptyObject := target.(map[string]any)
			if emptyObject {
				if errorCode(err) != "wait_timeout" || b.closed || r.LastObservation == nil || len(r.Satisfied) != 0 {
					t.Fatalf("empty miss became malformed: %+v %v", r, err)
				}
			} else if err == nil || errorCode(err) == "wait_timeout" || !b.closed || p.bridge != nil || r.LastObservation != nil {
				t.Fatalf("malformed target accepted: %+v %v", r, err)
			}
			if r.Attempts != 1 || b.captures != 1 || b.inputs != 0 || p.observation != nil {
				t.Fatalf("bad sampling/input: %+v", r)
			}
		})
	}
}

func readerReplies(t *testing.T, replies ...any) *Bridge {
	t.Helper()
	client, peer := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			header := make([]byte, 4)
			if _, err := io.ReadFull(peer, header); err != nil {
				return
			}
			data := make([]byte, binary.BigEndian.Uint32(header))
			if _, err := io.ReadFull(peer, data); err != nil {
				return
			}
			j := i
			if j >= len(replies) {
				j = len(replies) - 1
			}
			reply, _ := json.Marshal(replies[j])
			binary.BigEndian.PutUint32(header, uint32(len(reply)))
			if _, err := peer.Write(append(header, reply...)); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { client.Close(); peer.Close(); <-done })
	return &Bridge{conn: client}
}

type protocolFixtureBridge struct {
	fixtureBridge
	reader *Bridge
	reads  int
}

func (b *protocolFixtureBridge) Tree(ctx context.Context) (map[string]any, error) {
	b.reads++
	return b.reader.Tree(ctx)
}
func (b *protocolFixtureBridge) Close() { b.fixtureBridge.Close(); b.reader.Close() }

func TestWaitReaderUnavailableThenReadyReusesFramedStream(t *testing.T) {
	for _, message := range []string{"window-server frontmost returned no application object", "Error kAXErrorServerNotFound getting snapshot for element 0x123"} {
		t.Run(message, func(t *testing.T) {
			s, p, fixture, a, _, _ := waitFixture(t)
			ready := map[string]any{"ok": true, "tree": fixture.tree["tree"], "pid": float64(42)}
			b := &protocolFixtureBridge{fixtureBridge: *fixture, reader: readerReplies(t, map[string]any{"ok": false, "error": message}, ready)}
			p.bridge = b
			a.TimeoutMs = intptr(700)
			value, err := s.Call(context.Background(), Request{"wait", a})
			r := value.(*WaitResult)
			if err != nil || r.Observation == nil || r.Attempts != 2 || b.reads != 2 || b.closed || p.bridge != b || b.captures != 1 || b.inputs != 0 {
				t.Fatalf("readiness did not reuse bridge: %+v %v", r, err)
			}
		})
	}
}
func TestWaitReaderUnavailableTimesOutWithoutInput(t *testing.T) {
	s, p, fixture, a, _, _ := waitFixture(t)
	b := &protocolFixtureBridge{fixtureBridge: *fixture, reader: readerReplies(t, map[string]any{"ok": false, "error": "window-server frontmost returned no application object"})}
	p.bridge = b
	a.TimeoutMs = intptr(300)
	value, err := s.Call(context.Background(), Request{"wait", a})
	r := value.(*WaitResult)
	if errorCode(err) != "wait_timeout" || r.Attempts != 2 || r.LastObservation != nil || r.Observation != nil || b.closed || p.bridge != b || b.inputs != 0 || b.captures != 0 || p.observation != nil {
		t.Fatalf("unavailable sampling: %+v %v", r, err)
	}
}
func TestWaitOtherReaderResponsesAreTerminal(t *testing.T) {
	for _, reply := range []any{
		map[string]any{"ok": false, "error": "other failure"},
		map[string]any{"error": "window-server frontmost returned no application object"},
		map[string]any{"ok": "false", "error": "window-server frontmost returned no application object"},
		map[string]any{"ok": false, "error": "window-server frontmost returned no application object extra"},
		map[string]any{"ok": false, "error": "Error kAXErrorServerNotFound getting snapshot for element"},
		map[string]any{"ok": true},
		map[string]any{"ok": true, "tree": map[string]any{}, "truncated": true},
		[]any{},
	} {
		t.Run(fmt.Sprint(reply), func(t *testing.T) {
			s, p, fixture, a, _, _ := waitFixture(t)
			b := &protocolFixtureBridge{fixtureBridge: *fixture, reader: readerReplies(t, reply)}
			p.bridge = b
			value, err := s.Call(context.Background(), Request{"wait", a})
			r := value.(*WaitResult)
			if err == nil || errorCode(err) == "wait_timeout" || r.Attempts != 1 || !b.closed || p.bridge != nil || r.LastObservation != nil || p.observation != nil || b.inputs != 0 {
				t.Fatalf("terminal protocol response retried: %+v %v", r, err)
			}
		})
	}
}

func TestStartupGoalUsesBoundedSamplingAndOwnedAdmission(t *testing.T) {
	for _, scenario := range []string{"ready", "timeout", "busy", "unmanaged", "wrong-lease", "empty-tree", "incomplete-tree", "missing-app-label", "not-application", "invalid-bounds", "truncated"} {
		t.Run(scenario, func(t *testing.T) {
			s, p, fixture, a, root, _ := waitFixture(t)
			a.Conditions = nil
			a.Screenshot = true
			a.TimeoutMs = intptr(1)
			screenshots := 0
			s.runCommand = func(context.Context, string, ...string) ([]byte, error) { screenshots++; return nil, nil }
			reply := map[string]any{"ok": true, "tree": root, "pid": float64(42)}
			if scenario == "empty-tree" {
				reply["tree"] = map[string]any{}
			}
			switch scenario {
			case "incomplete-tree":
				root[ax+"Children"] = []any{"bad child"}
			case "missing-app-label":
				delete(root, ax+"Label")
			case "not-application":
				root[ax+"AutomationType"] = float64(9)
			case "invalid-bounds":
				delete(root, ax+"Frame")
			case "truncated":
				reply["truncated"] = true
			}
			unavailable := map[string]any{"ok": false, "error": "window-server frontmost returned no application object"}
			replies := []any{unavailable, reply}
			if scenario == "timeout" {
				replies = []any{unavailable}
			}
			b := &protocolFixtureBridge{fixtureBridge: *fixture, reader: readerReplies(t, replies...)}
			p.bridge = b
			if scenario == "unmanaged" {
				a.Device = "other"
			}
			if scenario == "wrong-lease" {
				a.Lease = "wrong"
			}
			old := &Observation{ID: "old"}
			expiry := p.lease.Expires
			if scenario == "busy" {
				p.observation = old
				p.mu.Lock()
				defer p.mu.Unlock()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 340*time.Millisecond)
			defer cancel()
			r, err := s.waitForObservation(ctx, a)
			switch scenario {
			case "ready":
				if err != nil || r.Observation == nil || r.Attempts != 2 || b.reads != 2 || screenshots != 1 || p.observation != r.Observation || r.Observation.ScreenshotAt == nil {
					t.Fatalf("startup did not sample: %+v %v", r, err)
				}
			case "timeout":
				if errorCode(err) != "wait_timeout" || r.Attempts != 2 || b.closed || r.Observation != nil || r.LastObservation != nil || screenshots != 0 {
					t.Fatalf("startup timeout: %+v %v", r, err)
				}
			case "busy":
				if errorCode(err) != "wait_timeout" || r.Attempts != 0 || b.reads != 0 || p.observation != old || !p.lease.Expires.Equal(expiry) {
					t.Fatalf("startup admission mutated: %+v %v", r, err)
				}
			case "unmanaged", "wrong-lease":
				want := "unmanaged_device"
				if scenario == "wrong-lease" {
					want = "lease_required"
				}
				if errorCode(err) != want || r.Attempts != 0 || b.reads != 0 || screenshots != 0 {
					t.Fatalf("startup bypassed ownership: %+v %v", r, err)
				}
			default:
				if err == nil || errorCode(err) == "wait_timeout" || r.Attempts != 2 || b.reads != 2 || !b.closed || r.Observation != nil || r.LastObservation != nil || screenshots != 0 {
					t.Fatalf("invalid startup tree accepted: %+v %v", r, err)
				}
			}
			if b.inputs != 0 {
				t.Fatal("startup sent input")
			}
		})
	}
}

func TestWaitReadinessRequiresReaderProtocolType(t *testing.T) {
	s, p, _, a, _, _ := waitFixture(t)
	b := &scriptedBridge{read: func(context.Context) (map[string]any, error) {
		return nil, errors.New("accessibility: window-server frontmost returned no application object")
	}}
	p.bridge = b
	value, err := s.Call(context.Background(), Request{"wait", a})
	r := value.(*WaitResult)
	if err == nil || errorCode(err) == "wait_timeout" || r.Attempts != 1 || !b.closed || p.bridge != nil || r.LastObservation != nil {
		t.Fatalf("generic failure was treated as readiness: %+v %v", r, err)
	}
}

func TestWaitRetainsCompleteSampleAcrossReaderUnavailability(t *testing.T) {
	s, p, fixture, a, _, _ := waitFixture(t)
	a.Conditions = []Condition{treeCondition("Search"), treeCondition("missing")}
	a.TimeoutMs = intptr(350)
	b := &protocolFixtureBridge{fixtureBridge: *fixture, reader: readerReplies(t, map[string]any{"ok": true, "tree": fixture.tree["tree"]}, map[string]any{"ok": false, "error": "window-server frontmost returned no application object"})}
	p.bridge = b
	value, err := s.Call(context.Background(), Request{"wait", a})
	r := value.(*WaitResult)
	if errorCode(err) != "wait_timeout" || r.Attempts != 2 || r.LastObservation == nil || r.Observation != nil || len(r.Satisfied) != 1 || r.Satisfied[0] != 0 || b.closed || b.captures != 1 || b.inputs != 0 || p.observation != nil {
		t.Fatalf("unavailability lost completed evidence: %+v %v", r, err)
	}
	e := searchTarget(t, r.LastObservation)
	if e.NativeHitAt == nil || e.Actionable || e.nativeToken != "" || len(e.Actions) != 0 {
		t.Fatalf("invalid diagnostic: %+v", e)
	}
}
