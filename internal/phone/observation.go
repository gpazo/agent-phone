package phone

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

const ax = "XC_kAXXCAttribute"

type Frame struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (f Frame) valid() bool {
	return f.Width > 0 && f.Height > 0 && !math.IsNaN(f.X+f.Y+f.Width+f.Height) && !math.IsInf(f.X+f.Y+f.Width+f.Height, 0)
}
func (f Frame) contains(x, y float64) bool {
	return f.valid() && x >= f.X && y >= f.Y && x < f.X+f.Width && y < f.Y+f.Height
}

type Element struct {
	NativeHitAt   *time.Time `json:"nativeHitAt,omitempty"`
	Ref           string     `json:"ref"`
	Role          string     `json:"role"`
	Label         string     `json:"label"`
	Identifier    string     `json:"identifier,omitempty"`
	Value         any        `json:"value,omitempty"`
	Placeholder   string     `json:"placeholder,omitempty"`
	Context       string     `json:"context,omitempty"`
	Frame         Frame      `json:"frame"`
	Actionable    bool       `json:"actionable"`
	Actions       []string   `json:"actions,omitempty"`
	probeEligible bool
	nativeToken   string
	automation    float64
	identity      targetIdentity
}
type Observation struct {
	ID           string     `json:"id"`
	Device       string     `json:"device"`
	App          string     `json:"app"`
	PID          float64    `json:"pid"`
	CapturedAt   time.Time  `json:"capturedAt"`
	ScreenshotAt *time.Time `json:"screenshotAt,omitempty"`
	Screenshot   string     `json:"screenshot,omitempty"`
	DurationMS   int64      `json:"durationMs"`
	Elements     []Element  `json:"elements"`
}

func stringValue(v any) string { s, _ := v.(string); return s }
func number(v any) float64     { n, _ := v.(float64); return n }
func frame(v any) Frame {
	m, _ := v.(map[string]any)
	x, xOK := m["X"].(float64)
	y, yOK := m["Y"].(float64)
	w, wOK := m["Width"].(float64)
	h, hOK := m["Height"].(float64)
	if !xOK || !yOK || !wOK || !hOK {
		return Frame{}
	}
	return Frame{x, y, w, h}
}
func role(n float64) string {
	switch int(n) {
	case 9, 10, 20, 44:
		return "button"
	case 40:
		return "switch"
	case 42:
		return "link"
	case 45, 49, 50, 52:
		return "textfield"
	case 39:
		return "picker"
	case 33:
		return "slider"
	case 43:
		return "image"
	case 48, 51:
		return "text"
	case 46:
		return "scrollview"
	case 75:
		return "cell"
	default:
		return "group"
	}
}

type targetIdentity struct {
	Automation                                            float64
	Class, Identifier, Label, Placeholder, Value, Context string
	IdentifierPresent                                     bool
}

func identity(node map[string]any, context string) targetIdentity {
	_, present := node[ax+"Identifier"]
	value, _ := json.Marshal(node[ax+"Value"])
	identifier, _ := json.Marshal(node[ax+"Identifier"])
	return targetIdentity{
		Automation: number(node[ax+"AutomationType"]), Class: stringValue(node[ax+"ElementType"]),
		Identifier: string(identifier), IdentifierPresent: present,
		Label: stringValue(node[ax+"Label"]), Placeholder: stringValue(node[ax+"PlaceholderValue"]),
		Value: string(value), Context: context,
	}
}

func nativeContextLabel(node map[string]any) (string, bool) {
	n := number(node[ax+"AutomationType"])
	label := stringValue(node[ax+"Label"])
	if n == 19 {
		return "Keyboard", true
	}
	if n != 75 || label != "" {
		return label, true
	}
	children, ok := node[ax+"Children"].([]any)
	if !ok || len(children) > 32 {
		return "", false
	}
	var labels []string
	for _, child := range children {
		c, ok := child.(map[string]any)
		if !ok {
			return "", false
		}
		if label := stringValue(c[ax+"Label"]); number(c[ax+"AutomationType"]) == 48 && label != "" && len(labels) < 3 {
			labels = append(labels, label)
		}
	}
	return strings.Join(labels, " / "), true
}

func truncateNativeLabel(label string) string {
	units := utf16.Encode([]rune(label))
	if len(units) > 160 {
		units = units[:160]
	}
	return string(utf16.Decode(units))
}

type treeEvidence struct {
	elements []Element
	complete bool
}

func flatten(tree map[string]any, id string) []Element { return decodeTree(tree, id).elements }

func decodeTree(tree map[string]any, id string) treeEvidence {
	bounds := frame(tree[ax+"Frame"])
	evidence := treeEvidence{complete: bounds.valid()}
	seen := map[string]bool{}
	counts := map[targetIdentity]int{}
	var visit func(map[string]any, []string, bool, int)
	visit = func(node map[string]any, ancestors []string, local bool, depth int) {
		n := number(node[ax+"AutomationType"])
		r := role(n)
		f := frame(node[ax+"Frame"])
		label := stringValue(node[ax+"Label"])
		placeholder := stringValue(node[ax+"PlaceholderValue"])
		class := stringValue(node[ax+"ElementType"])
		children, ok := node[ax+"Children"].([]any)
		if raw := node[ax+"Children"]; raw != nil && !ok {
			evidence.complete = false
		}
		remote := class == "AXRemoteElement" || class == "WebAccessibilityObjectWrapper"
		if remote {
			local = false
			intersects := !f.valid() || !bounds.valid() || (f.X < bounds.X+bounds.Width && f.X+f.Width > bounds.X && f.Y < bounds.Y+bounds.Height && f.Y+f.Height > bounds.Y)
			if intersects && (class == "WebAccessibilityObjectWrapper" || len(children) == 0) {
				evidence.complete = false
			}
		}
		context := strings.Join(ancestors, " > ")
		if class == "UIAccessibilityBackButtonElement" {
			context = strings.Join(append(append([]string{}, ancestors...), "Back navigation"), " > ")
		}
		semantics := identity(node, context)
		counts[semantics]++
		if label == "" && r == "textfield" {
			label = placeholder
		}
		actionable := r == "button" || r == "switch" || r == "link" || r == "textfield" || r == "picker" || r == "scrollview" || r == "cell"
		if node[ax+"IsEnabled"] == false || !local || depth >= 32 {
			actionable = false
		}
		if f.valid() && (!bounds.valid() || bounds.contains(f.X+f.Width/2, f.Y+f.Height/2)) && (label != "" || actionable) {
			key := fmt.Sprintf("%s|%s|%v|%g|%g", r, label, node[ax+"Value"], f.X, f.Y)
			if !seen[key] {
				seen[key] = true
				evidence.elements = append(evidence.elements, Element{Ref: fmt.Sprintf("%s:e%d", id, len(evidence.elements)+1), Role: r, Label: label, Identifier: stringValue(node[ax+"Identifier"]), Value: node[ax+"Value"], Placeholder: placeholder, Context: context, Frame: f, Actionable: actionable, probeEligible: local && depth < 32, automation: n, identity: semantics})
			}
		}
		contextLabel, complete := nativeContextLabel(node)
		if !complete {
			evidence.complete = false
		}
		next := ancestors
		if depth > 0 && contextLabel != "" && (len(ancestors) == 0 || ancestors[len(ancestors)-1] != contextLabel) {
			next = append(append([]string{}, ancestors...), truncateNativeLabel(contextLabel))
			if len(next) > 2 {
				next = next[len(next)-2:]
			}
		}
		for _, child := range children {
			if c, ok := child.(map[string]any); ok {
				visit(c, next, local, depth+1)
			} else {
				evidence.complete = false
			}
		}
	}
	visit(tree, nil, true, 0)
	for i := range evidence.elements {
		e := &evidence.elements[i]
		e.probeEligible = e.probeEligible && evidence.complete && counts[e.identity] == 1
		e.Actionable = e.Actionable && e.probeEligible
	}
	return evidence
}

func sameTarget(e Element, hit map[string]any) bool {
	return hit != nil && e.identity == identity(hit, e.Context)
}

func supportedActions(e Element) []string {
	if !e.Actionable {
		return nil
	}
	switch e.Role {
	case "textfield":
		return []string{"type"}
	case "picker":
		return []string{"increment", "decrement"}
	case "scrollview":
		return []string{"scroll-up", "scroll-down"}
	default:
		return []string{"press"}
	}
}

func (s *Service) capture(ctx context.Context, p *Phone, query *waitQuery) (*Observation, error) {
	started := time.Now()
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if p.bridge == nil {
		b, err := NewBridge(ctx, p.UDID, s.root)
		if err != nil {
			return nil, err
		}
		p.bridge = b
	}
	tree, err := p.bridge.Tree(ctx)
	if err != nil {
		p.closeBridge()
		return nil, err
	}
	if err = contextError(ctx); err != nil {
		p.closeBridge()
		return nil, err
	}
	root, ok := tree["tree"].(map[string]any)
	if !ok {
		p.closeBridge()
		return nil, fmt.Errorf("reader returned no application tree")
	}
	o := &Observation{ID: randomID(), Device: p.UDID, App: stringValue(root[ax+"Label"]), PID: number(tree["pid"]), CapturedAt: time.Now(), Elements: []Element{}}
	o.Elements = flatten(root, o.ID)
	for i := range o.Elements {
		if tree["truncated"] == true {
			o.Elements[i].Actionable = false
			o.Elements[i].probeEligible = false
		}
	}
	indices := []int{}
	selected := make(map[int]bool)
	if query != nil {
		for i, e := range o.Elements {
			if !e.probeEligible {
				continue
			}
			for _, condition := range query.conditions {
				if condition.Evidence == "native-hit" && condition.Where.matches(e, supportedActions(e)) {
					indices = append(indices, i)
					selected[i] = true
					break
				}
			}
		}
		if len(indices) > 250 {
			return nil, fail("selector_too_broad", "native-hit selectors require more than 250 probe candidates")
		}
	}
	for i, e := range o.Elements {
		if e.Actionable && !selected[i] && len(indices) < 250 {
			indices = append(indices, i)
		}
	}
	points := make([]map[string]float64, 0, len(indices))
	actions := make(map[int][]string, len(indices))
	for _, i := range indices {
		e := o.Elements[i]
		actions[i] = supportedActions(e)
		points = append(points, map[string]float64{"x": e.Frame.X + e.Frame.Width/2, "y": e.Frame.Y + e.Frame.Height/2})
	}
	for i := range o.Elements {
		o.Elements[i].Actionable = false
	}
	if len(points) > 0 {
		result, err := p.bridge.Action(ctx, map[string]any{"action": "capture-targets", "points": points})
		if err != nil {
			p.closeBridge()
			return nil, err
		}
		received := time.Now()
		if err = contextError(ctx); err != nil {
			p.closeBridge()
			return nil, err
		}
		targets, _ := result["targets"].([]any)
		if len(targets) != len(indices) {
			p.closeBridge()
			return nil, fmt.Errorf("invalid native target batch")
		}
		for j, index := range indices {
			target, _ := targets[j].(map[string]any)
			hit, _ := target["tree"].(map[string]any)
			e := &o.Elements[index]
			app, appOK := target["app"].(string)
			targetContext, contextOK := target["context"].(string)
			token := stringValue(target["token"])
			if !appOK || app != o.App || !contextOK || targetContext != e.Context || token == "" || !sameTarget(*e, hit) {
				continue
			}
			e.NativeHitAt = &received
			if len(actions[index]) > 0 {
				e.nativeToken = token
				e.Actionable = true
				e.Actions = actions[index]
			}
		}
	}
	o.DurationMS = time.Since(started).Milliseconds()
	return o, nil
}

func (s *Service) screenshot(ctx context.Context, p *Phone, o *Observation) error {
	path := filepath.Join(s.data, "artifacts", p.UDID, o.ID+".jpg")
	if err := mkdir(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := s.command(ctx, "/usr/bin/xcrun", "simctl", "io", p.UDID, "screenshot", "--type=jpeg", path); err != nil {
		return err
	}
	now := time.Now()
	o.ScreenshotAt = &now
	o.Screenshot = path
	return contextError(ctx)
}

func (s *Service) publish(ctx context.Context, p *Phone, o *Observation) error {
	s.event(p.UDID, "observe", map[string]any{"observation": o.ID, "durationMs": o.DurationMS, "screenshot": o.Screenshot})
	if err := contextError(ctx); err != nil {
		return err
	}
	if p.lease != nil {
		if err := validateLease(p.lease, p.lease.Token, time.Now()); err != nil {
			return err
		}
	}
	p.observation = o
	p.lastObservation = o
	return nil
}

func (s *Service) observe(ctx context.Context, p *Phone, screenshot bool) (*Observation, error) {
	started := time.Now()
	p.observation = nil
	o, err := s.capture(ctx, p, nil)
	if err != nil {
		return nil, err
	}
	if screenshot {
		if err = s.screenshot(ctx, p, o); err != nil {
			return nil, err
		}
	}
	o.DurationMS = time.Since(started).Milliseconds()
	if err = s.publish(ctx, p, o); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Service) act(ctx context.Context, p *Phone, a Params) (any, error) {
	o := p.observation
	if o == nil || a.Observation != o.ID || time.Since(o.CapturedAt) > 30*time.Second {
		return nil, fail("stale_observation", "Observe this phone again before acting (observations expire after 30 seconds).")
	}
	var target *Element
	for i := range o.Elements {
		if o.Elements[i].Ref == a.Ref {
			target = &o.Elements[i]
			break
		}
	}
	if target == nil || !target.Actionable || target.nativeToken == "" {
		return nil, fail("invalid_target", "The element is not a verified actionable target.")
	}
	allowed := false
	for _, action := range target.Actions {
		if action == a.Action {
			allowed = true
		}
	}
	if !allowed {
		return nil, fail("invalid_action", "Action is not supported by this element.")
	}
	if len(a.Text) > 65536 {
		return nil, fmt.Errorf("text exceeds 64 KiB")
	}
	// Consume the observation even if input fails: repeating a mutation is never
	// an automatic recovery strategy.
	p.observation = nil
	result, err := p.bridge.Action(ctx, map[string]any{"action": "validate-target", "token": target.nativeToken})
	if err != nil {
		p.closeBridge()
		return nil, err
	}
	f := frame(result["frame"])
	if !f.valid() {
		p.closeBridge()
		return nil, fmt.Errorf("target no longer has a visible frame")
	}
	action := a.Action
	if action == "type" {
		action = "replace-text"
	}
	request := map[string]any{"action": action, "x": f.X + f.Width/2, "y": f.Y + f.Height/2, "expectedToken": target.nativeToken, "expectedAutomationType": target.automation}
	if target.Placeholder != "" {
		request["expectedPlaceholderValue"] = target.Placeholder
	} else {
		request["expectedLabel"] = target.Label
	}
	if target.Value != nil {
		request["expectedValue"] = target.Value
	}
	if a.Action == "type" {
		request["text"] = a.Text
	}
	if _, err = p.bridge.Action(ctx, request); err != nil {
		p.closeBridge()
		return nil, err
	}
	s.event(p.UDID, "act", map[string]any{"action": a.Action, "target": target.Label, "observation": o.ID})
	// Capture after the transition without claiming that native acknowledgment
	// proves the caller's semantic goal. The agent evaluates the returned state.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(180 * time.Millisecond):
	}
	after, err := s.observe(ctx, p, a.Screenshot)
	if err != nil {
		return map[string]any{"acknowledged": true, "observationError": err.Error()}, nil
	}
	return map[string]any{"acknowledged": true, "observation": after}, nil
}

func shortName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
