package phone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

var SelectorRoles = []string{"button", "switch", "link", "textfield", "picker", "slider", "image", "text", "scrollview", "cell", "group"}
var ElementActions = []string{"press", "type", "increment", "decrement", "scroll-up", "scroll-down"}

type Selector struct {
	Role       *string `json:"role,omitempty"`
	Label      *string `json:"label,omitempty"`
	Identifier *string `json:"identifier,omitempty"`
	Value      *string `json:"value,omitempty"`
	Context    *string `json:"context,omitempty"`
	Action     *string `json:"action,omitempty"`
}

type Condition struct {
	Where    Selector `json:"where"`
	Evidence string   `json:"evidence"`
}

type WaitResult struct {
	Observation     *Observation `json:"observation,omitempty"`
	LastObservation *Observation `json:"lastObservation,omitempty"`
	Satisfied       []int        `json:"satisfied"`
	Attempts        int          `json:"attempts"`
	ElapsedMS       int64        `json:"elapsedMs"`
}

type waitQuery struct {
	conditions []Condition
	any        bool
	timeout    time.Duration
	screenshot bool
}

func (s *Selector) UnmarshalJSON(data []byte) error {
	type plain Selector
	var v plain
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("selector %s must be a string", key)
		}
	}
	*s = Selector(v)
	return nil
}
func (c *Condition) UnmarshalJSON(data []byte) error {
	type plain Condition
	var v plain
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return err
	}
	*c = Condition(v)
	return nil
}
func parseWait(a Params) (waitQuery, error) {
	q := waitQuery{conditions: a.Conditions, timeout: 30 * time.Second, screenshot: a.Screenshot}
	bad := func(message string) (waitQuery, error) { return waitQuery{}, fail("invalid_wait", message) }
	if len(q.conditions) < 1 || len(q.conditions) > 16 {
		return bad("conditions requires 1..16 entries")
	}
	if a.Mode != nil {
		if *a.Mode != "all" && *a.Mode != "any" {
			return bad("mode must be all or any")
		}
		q.any = *a.Mode == "any"
	}
	if a.TimeoutMs != nil {
		if *a.TimeoutMs < 1 || *a.TimeoutMs > 30000 {
			return bad("timeoutMs must be 1..30000")
		}
		q.timeout = time.Duration(*a.TimeoutMs) * time.Millisecond
	}
	for _, c := range q.conditions {
		if c.Evidence != "tree" && c.Evidence != "native-hit" {
			return bad("evidence must be tree or native-hit; visual visibility is not verified")
		}
		w := c.Where
		if w.Role == nil && w.Label == nil && w.Identifier == nil && w.Value == nil && w.Context == nil && w.Action == nil {
			return bad("where requires at least one exact string selector")
		}
		if w.Role != nil && !slices.Contains(SelectorRoles, *w.Role) {
			return bad("unknown selector role")
		}
		if w.Action != nil && (!slices.Contains(ElementActions, *w.Action) || c.Evidence != "native-hit") {
			return bad("action requires a supported action and native-hit evidence")
		}
	}
	return q, nil
}
func (s Selector) matches(e Element, actions []string) bool {
	if s.Role != nil && *s.Role != e.Role || s.Label != nil && *s.Label != e.Label || s.Identifier != nil && *s.Identifier != e.Identifier || s.Context != nil && *s.Context != e.Context {
		return false
	}
	if s.Value != nil {
		v, ok := e.Value.(string)
		if !ok || v != *s.Value {
			return false
		}
	}
	return s.Action == nil || slices.Contains(actions, *s.Action)
}
func (q waitQuery) evaluate(o *Observation) ([]int, bool) {
	satisfied := []int{}
	for i, c := range q.conditions {
		for _, e := range o.Elements {
			if c.Where.matches(e, e.Actions) && (c.Evidence == "tree" || e.NativeHitAt != nil) {
				satisfied = append(satisfied, i)
				break
			}
		}
	}
	return satisfied, (q.any && len(satisfied) > 0) || len(satisfied) == len(q.conditions)
}
func waitError(err error) error {
	if errors.Is(err, context.Canceled) {
		return fail("cancelled", "UI wait cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fail("wait_timeout", "UI conditions were not satisfied before the deadline")
	}
	return err
}
func waitDiagnostics(result *WaitResult, start time.Time) {
	result.ElapsedMS = time.Since(start).Milliseconds()
}
func diagnosticObservation(o *Observation) *Observation {
	copy := *o
	copy.Elements = append([]Element{}, o.Elements...)
	for i := range copy.Elements {
		e := &copy.Elements[i]
		e.Actionable = false
		e.Actions = nil
		e.nativeToken = ""
	}
	return &copy
}
func (s *Service) wait(ctx context.Context, p *Phone, a Params, q waitQuery, start time.Time, result *WaitResult) (*WaitResult, error) {
	defer waitDiagnostics(result, start)
	for {
		if err := contextError(ctx); err != nil {
			return result, waitError(err)
		}
		if err := validateLease(p.lease, a.Lease, time.Now()); err != nil {
			return result, err
		}
		sampleStarted := time.Now()
		result.Attempts++
		p.observation = nil
		o, err := s.capture(ctx, p, &q)
		if cutoff := contextError(ctx); cutoff != nil {
			return result, waitError(cutoff)
		}
		if err != nil {
			return result, err
		}
		satisfied, matched := q.evaluate(o)
		if matched && q.screenshot {
			if err = s.screenshot(ctx, p, o); err != nil {
				if cutoff := contextError(ctx); cutoff != nil {
					err = waitError(cutoff)
				}
				return result, err
			}
		}
		if err = contextError(ctx); err != nil {
			return result, waitError(err)
		}
		if err = validateLease(p.lease, a.Lease, time.Now()); err != nil {
			return result, err
		}
		if matched {
			o.DurationMS = time.Since(sampleStarted).Milliseconds()
			if err = s.publish(ctx, p, o); err != nil {
				return result, waitError(err)
			}
			result.Observation = o
			result.LastObservation = nil
			result.Satisfied = satisfied
			return result, nil
		}
		result.LastObservation = diagnosticObservation(o)
		result.Satisfied = satisfied
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, waitError(ctx.Err())
		case <-timer.C:
		}
	}
}
