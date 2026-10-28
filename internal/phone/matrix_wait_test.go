package phone

import (
	"context"
	"strings"
	"testing"
)

func TestMatrixWaitUsesSharedServiceAndKeepsDiagnostics(t *testing.T) {
	for _, match := range []bool{false, true} {
		s, p, _, a, _, _ := waitFixture(t)
		label := "Search"
		if !match {
			label = "missing"
		}
		r := VariantResult{Name: "fixture", Observation: &Observation{ID: "old"}}
		err := s.runSteps(context.Background(), a, []Step{{Action: "wait", Conditions: []Condition{treeCondition(label)}, TimeoutMs: intptr(10)}}, &r)
		if match {
			if err != nil || r.Status != "passed" || r.Wait == nil || r.Observation != r.Wait.Observation || p.observation != r.Observation {
				t.Fatalf("wait did not assert: %+v %v", r, err)
			}
		} else {
			if errorCode(err) != "wait_timeout" || r.Status != "failed" || r.Wait == nil || r.Wait.LastObservation == nil || p.observation != nil {
				t.Fatalf("failure diagnostics lost: %+v %v", r, err)
			}
			r.Error = err.Error()
			report, reportErr := s.finishMatrix(MatrixReport{ID: "failed-wait", Results: []VariantResult{r}})
			if errorCode(reportErr) != "matrix_failed" || report.(MatrixReport).Results[0].Wait.LastObservation == nil {
				t.Fatal("failed wait became passed report")
			}
		}
	}
}
func TestMatrixLegacyExpectIsExactTreePresence(t *testing.T) {
	s := &Service{}
	for _, value := range []any{"result", 12, nil} {
		r := VariantResult{Observation: &Observation{Elements: []Element{{Label: "covered label", Value: value}}}}
		err := s.runSteps(context.Background(), Params{}, []Step{{Action: "assert", Expect: "result"}}, &r)
		if value == "result" {
			if err != nil || r.Status != "passed" {
				t.Fatal(err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "tree label or string value") || strings.Contains(err.Error(), "visible") || r.Status != "failed" {
			t.Fatalf("misleading assertion: %+v %v", r, err)
		}
	}
	r := VariantResult{Observation: &Observation{Elements: []Element{{Label: "covered label"}}}}
	if err := s.runSteps(context.Background(), Params{}, []Step{{Action: "assert", Expect: "covered label"}}, &r); err != nil {
		t.Fatal("tree presence unexpectedly requires native hit", err)
	}
	if err := s.runSteps(context.Background(), Params{}, []Step{{Action: "assert"}}, &r); err != nil || r.Status != "observed" {
		t.Fatal("empty assertion became pass")
	}
}
