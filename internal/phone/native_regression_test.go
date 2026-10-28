package phone

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

type fixtureBridge struct {
	tree        map[string]any
	receipt     map[string]any
	validate    map[string]any
	validateErr error
	closed      bool
	inputs      int
	captures    int
}

func (b *fixtureBridge) Tree() (map[string]any, error) { return b.tree, nil }
func (b *fixtureBridge) Close()                        { b.closed = true }
func (b *fixtureBridge) Action(request map[string]any) (map[string]any, error) {
	switch request["action"] {
	case "capture-targets":
		b.captures++
		targets := make([]any, len(request["points"].([]map[string]float64)))
		for i := range targets {
			targets[i] = b.receipt
		}
		return map[string]any{"targets": targets}, nil
	case "validate-target":
		return b.validate, b.validateErr
	default:
		b.inputs++
		return map[string]any{"ok": true}, nil
	}
}
func nativeFrame(x, y float64) map[string]any {
	return map[string]any{"X": x, "Y": y, "Width": float64(100), "Height": float64(40)}
}
func nativeNode(n float64, label string, children ...any) map[string]any {
	return map[string]any{ax + "AutomationType": n, ax + "ElementType": "UIView", ax + "Label": label, ax + "Frame": nativeFrame(20, 30), ax + "Children": children}
}
func nativeFixture(t *testing.T) (*Service, *Phone, *fixtureBridge, map[string]any, map[string]any) {
	t.Helper()
	target := nativeNode(9, "Search")
	target[ax+"Identifier"] = "search"
	target[ax+"Value"] = "old"
	root := nativeNode(1, "Wikipedia", target)
	root[ax+"Frame"] = map[string]any{"X": float64(0), "Y": float64(0), "Width": float64(400), "Height": float64(800)}
	hit := map[string]any{}
	for k, v := range target {
		hit[k] = v
	}
	b := &fixtureBridge{tree: map[string]any{"tree": root, "pid": float64(42)}, receipt: map[string]any{"app": "Wikipedia", "context": "", "token": "capture:0", "tree": hit}, validate: map[string]any{"frame": nativeFrame(20, 30)}}
	return &Service{data: t.TempDir()}, &Phone{UDID: "fixture-device", bridge: b}, b, root, target
}
func searchTarget(t *testing.T, o *Observation) Element {
	t.Helper()
	for _, e := range o.Elements {
		if e.Label == "Search" {
			return e
		}
	}
	t.Fatal("passive Search evidence was lost")
	return Element{}
}
func TestObserveNativeReceiptJoinRejectsChangedEvidence(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fixtureBridge, map[string]any, map[string]any)
	}{
		{"app", func(b *fixtureBridge, _, _ map[string]any) { b.receipt["app"] = "Other app" }},
		{"missing app", func(b *fixtureBridge, _, _ map[string]any) { delete(b.receipt, "app") }},
		{"context", func(b *fixtureBridge, _, _ map[string]any) { b.receipt["context"] = "Different row" }},
		{"missing context", func(b *fixtureBridge, _, _ map[string]any) { delete(b.receipt, "context") }},
		{"value", func(b *fixtureBridge, _, _ map[string]any) { b.receipt["tree"].(map[string]any)[ax+"Value"] = "new" }},
		{"identifier added", func(b *fixtureBridge, _, target map[string]any) { delete(target, ax+"Identifier") }},
		{"identifier missing versus empty", func(b *fixtureBridge, _, target map[string]any) {
			target[ax+"Identifier"] = ""
			delete(b.receipt["tree"].(map[string]any), ax+"Identifier")
		}},

		{"identifier null versus empty", func(b *fixtureBridge, _, target map[string]any) {
			target[ax+"Identifier"] = ""
			b.receipt["tree"].(map[string]any)[ax+"Identifier"] = nil
		}},
		{"textfield label changed with same placeholder", func(b *fixtureBridge, _, target map[string]any) {
			target[ax+"AutomationType"] = float64(49)
			target[ax+"PlaceholderValue"] = "Search"
			hit := b.receipt["tree"].(map[string]any)
			hit[ax+"AutomationType"] = float64(49)
			hit[ax+"PlaceholderValue"] = "Search"
			hit[ax+"Label"] = "Replaced field"
		}},
		{"class", func(b *fixtureBridge, _, _ map[string]any) {
			b.receipt["tree"].(map[string]any)[ax+"ElementType"] = "OtherButton"
		}},
		{"placeholder", func(b *fixtureBridge, _, _ map[string]any) {
			b.receipt["tree"].(map[string]any)[ax+"PlaceholderValue"] = "New placeholder"
		}},
		{"empty token", func(b *fixtureBridge, _, _ map[string]any) { b.receipt["token"] = "" }},
		{"duplicate before dedup", func(_ *fixtureBridge, root, target map[string]any) { root[ax+"Children"] = []any{target, target} }},
		{"duplicate elsewhere", func(_ *fixtureBridge, root, target map[string]any) {
			dup := map[string]any{}
			for k, v := range target {
				dup[k] = v
			}
			dup[ax+"Frame"] = nativeFrame(150, 300)
			root[ax+"Children"] = []any{target, dup}
		}},
		{"visible unresolved remote", func(_ *fixtureBridge, root, target map[string]any) {
			remote := nativeNode(0, "")
			remote[ax+"ElementType"] = "AXRemoteElement"
			root[ax+"Children"] = []any{target, remote}
		}},
		{"visible web", func(_ *fixtureBridge, root, target map[string]any) {
			web := nativeNode(0, "Article")
			web[ax+"ElementType"] = "WebAccessibilityObjectWrapper"
			root[ax+"Children"] = []any{target, web}
		}},
		{"malformed children", func(_ *fixtureBridge, root, target map[string]any) {
			root[ax+"Children"] = []any{target, "unreadable subtree"}
		}},
		{"truncated tree", func(b *fixtureBridge, _, _ map[string]any) { b.tree["truncated"] = true }},
		{"duplicate after capture limit", func(_ *fixtureBridge, root, target map[string]any) {
			children := []any{target}
			for i := 0; i < 250; i++ {
				children = append(children, nativeNode(9, fmt.Sprintf("Unique %d", i)))
			}
			children = append(children, target)
			root[ax+"Children"] = children
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, p, b, root, target := nativeFixture(t)
			tc.change(b, root, target)
			o, err := s.observe(context.Background(), p, false)
			if err != nil {
				return
			}
			e := searchTarget(t, o)
			if e.Actionable || e.nativeToken != "" {
				t.Errorf("changed or incomplete evidence received an actionable token: %#v", e)
			}
			_, err = s.act(context.Background(), p, Params{Observation: o.ID, Ref: e.Ref, Action: "press"})
			if err == nil || b.inputs != 0 {
				t.Errorf("unsafe input accepted: error=%v inputs=%d", err, b.inputs)
			}
		})
	}
}
func TestObserveNativeReceiptJoinAcceptsUnchangedTarget(t *testing.T) {
	for _, contextName := range []string{"", "Row", "Keyboard", "Title / Subtitle", "Back navigation", strings.Repeat("😀", 80)} {
		t.Run(contextName, func(t *testing.T) {
			s, p, b, root, target := nativeFixture(t)
			switch contextName {
			case "Keyboard":
				root[ax+"Children"] = []any{nativeNode(19, "", target)}
			case "Title / Subtitle":
				root[ax+"Children"] = []any{nativeNode(75, "", nativeNode(48, "Title"), nativeNode(48, "Subtitle"), target)}
			case "Back navigation":
				target[ax+"ElementType"] = "UIAccessibilityBackButtonElement"
				b.receipt["tree"].(map[string]any)[ax+"ElementType"] = "UIAccessibilityBackButtonElement"
			case "":
			default:
				root[ax+"Children"] = []any{nativeNode(0, contextName, target)}
			}
			b.receipt["context"] = contextName
			timer := nativeNode(48, "2 seconds ago")
			root[ax+"Children"] = append(root[ax+"Children"].([]any), timer)
			o, err := s.observe(context.Background(), p, false)
			if err != nil {
				t.Fatal(err)
			}
			e := searchTarget(t, o)
			if !e.Actionable {
				t.Fatal("unchanged native target is not actionable")
			}
			timer[ax+"Label"] = "3 seconds ago"
			_, err = s.act(context.Background(), p, Params{Observation: o.ID, Ref: e.Ref, Action: "press"})
			if err != nil || b.inputs != 1 || b.closed {
				t.Fatalf("selected input failed after unrelated timer changed: error=%v inputs=%d closed=%v", err, b.inputs, b.closed)
			}
		})
	}
}
func TestActNativeValidationFailureDiscardsBridge(t *testing.T) {
	for _, name := range []string{"timeout", "malformed reply", "oversized reply", "semantic rejection", "invalid frame", "missing frame coordinate"} {
		t.Run(name, func(t *testing.T) {
			s, p, b, _, _ := nativeFixture(t)
			o, err := s.observe(context.Background(), p, false)
			if err != nil {
				t.Fatal(err)
			}
			e := searchTarget(t, o)
			if name == "invalid frame" {
				b.validate = map[string]any{"frame": nativeFrame(0, 0)}
				b.validate["frame"].(map[string]any)["Width"] = float64(0)
			} else if name == "missing frame coordinate" {
				delete(b.validate["frame"].(map[string]any), "X")
			} else {
				b.validateErr = errors.New(name)
			}
			_, err = s.act(context.Background(), p, Params{Observation: o.ID, Ref: e.Ref, Action: "press"})
			if err == nil {
				t.Fatal("validation failure accepted")
			}
			if !b.closed || p.bridge != nil || p.observation != nil || b.inputs != 0 {
				t.Fatalf("failed validation retained helper or sent input: closed=%v bridge=%v observation=%v inputs=%d", b.closed, p.bridge != nil, p.observation != nil, b.inputs)
			}
			_, err = s.act(context.Background(), p, Params{Observation: o.ID, Ref: e.Ref, Action: "press"})
			if err == nil || b.inputs != 0 {
				t.Fatal("consumed observation replayed")
			}
		})
	}
}

func TestActNativeProtocolFailureClosesStreams(t *testing.T) {
	for _, tc := range []struct{ name, response string }{
		{"malformed JSON", "{broken\n"},
		{"oversized reply", strings.Repeat("x", 2*1024*1024+1) + "\n"},
		{"closed output", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p, _, _, _ := nativeFixture(t)
			o, err := s.observe(context.Background(), p, false)
			if err != nil {
				t.Fatal(err)
			}
			e := searchTarget(t, o)
			requestReader, requestWriter, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			responseReader, responseWriter, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { requestReader.Close(); requestWriter.Close(); responseReader.Close(); responseWriter.Close() })
			b := &Bridge{input: requestWriter, actionOutput: responseReader, output: bufio.NewReader(responseReader)}
			p.bridge = b
			requests := make(chan []byte, 1)
			go func() { data, _ := io.ReadAll(requestReader); requests <- data }()
			go func() { _, _ = io.WriteString(responseWriter, tc.response); responseWriter.Close() }()
			_, err = s.act(context.Background(), p, Params{Observation: o.ID, Ref: e.Ref, Action: "press"})
			if err == nil {
				t.Fatal("invalid native response accepted")
			}
			if p.bridge != nil || p.observation != nil {
				t.Error("corrupt helper remains attached")
			}
			if _, err := requestWriter.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Errorf("helper input is still open: %v", err)
			}
			requestWriter.Close()
			data := <-requests
			if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "validate-target") {
				t.Fatalf("input was dispatched after failed validation: %s", data)
			}
			if _, err := responseReader.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
				t.Errorf("helper output is still open: %v", err)
			}
		})
	}
}

func TestObserveNativeContextMatchesAncestorRules(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ancestors []string
		expected  string
	}{
		{"last two", []string{"Outer", "Middle", "Inner"}, "Middle > Inner"},
		{"adjacent duplicate", []string{"Outer", "Row", "Row"}, "Outer > Row"},
		{"UTF16 truncation", []string{strings.Repeat("😀", 81)}, strings.Repeat("😀", 80)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p, b, root, target := nativeFixture(t)
			var nested any = target
			for i := len(tc.ancestors) - 1; i >= 0; i-- {
				nested = nativeNode(0, tc.ancestors[i], nested)
			}
			root[ax+"Children"] = []any{nested}
			b.receipt["context"] = tc.expected
			o, err := s.observe(context.Background(), p, false)
			if err != nil {
				t.Fatal(err)
			}
			e := searchTarget(t, o)
			if !e.Actionable || e.Context != tc.expected {
				t.Fatalf("native context not retained: %+v", e)
			}
		})
	}
}

func TestObserveCaptureLimitKeepsOverflowPassive(t *testing.T) {
	s, p, b, root, _ := nativeFixture(t)
	children := make([]any, 251)
	for i := range children {
		children[i] = nativeNode(9, fmt.Sprintf("Control %d", i))
	}
	root[ax+"Children"] = children
	b.receipt["tree"] = children[0]
	o, err := s.observe(context.Background(), p, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range o.Elements {
		if e.Label == "Control 0" && !e.Actionable {
			t.Error("first unique target was not captured")
		}
		if e.Label == "Control 250" && (e.Actionable || e.nativeToken != "") {
			t.Error("uncaptured overflow control is actionable")
		}
	}
	if b.captures != 1 {
		t.Fatalf("expected one bounded batch, got %d", b.captures)
	}
}

func TestObserveOffscreenRemoteDoesNotDisableLocalTarget(t *testing.T) {
	s, p, _, root, target := nativeFixture(t)
	remote := nativeNode(0, "")
	remote[ax+"ElementType"] = "AXRemoteElement"
	remote[ax+"Frame"] = nativeFrame(20, 900)
	root[ax+"Children"] = []any{target, remote}
	o, err := s.observe(context.Background(), p, false)
	if err != nil {
		t.Fatal(err)
	}
	if !searchTarget(t, o).Actionable {
		t.Fatal("offscreen remote stub disabled local target")
	}
}
