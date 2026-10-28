package phone

import "testing"

func TestSelectedTargetIndependentOfUnrelatedTimer(t *testing.T) {
	hit := map[string]any{ax + "Label": "Search", ax + "Identifier": "search", ax + "AutomationType": float64(9), "unrelatedSibling": "2 seconds ago"}
	e := Element{Role: "button", Label: "Search", Identifier: "search", automation: 9, identity: identity(hit, "")}
	if !sameTarget(e, hit) {
		t.Fatal("correct target rejected")
	}
	hit["unrelatedSibling"] = "3 seconds ago"
	if !sameTarget(e, hit) {
		t.Fatal("unrelated timer invalidates target")
	}
	hit[ax+"Identifier"] = "delete"
	if sameTarget(e, hit) {
		t.Fatal("replaced target accepted")
	}
}
func TestBlankTextFieldsRemainObservable(t *testing.T) {
	tree := map[string]any{ax + "Frame": map[string]any{"X": float64(0), "Y": float64(0), "Width": float64(400), "Height": float64(800)}, ax + "Children": []any{map[string]any{ax + "AutomationType": float64(49), ax + "Identifier": "query", ax + "Frame": map[string]any{"X": float64(20), "Y": float64(30), "Width": float64(200), "Height": float64(40)}}}}
	elements := flatten(tree, "observation")
	if len(elements) != 1 || elements[0].Role != "textfield" || elements[0].Identifier != "query" {
		t.Fatalf("blank editable field lost: %#v", elements)
	}
}
func TestOffscreenControlsAreNotOffered(t *testing.T) {
	tree := map[string]any{ax + "Frame": map[string]any{"X": float64(0), "Y": float64(0), "Width": float64(400), "Height": float64(800)}, ax + "Children": []any{map[string]any{ax + "AutomationType": float64(9), ax + "Label": "Delete", ax + "Frame": map[string]any{"X": float64(20), "Y": float64(900), "Width": float64(100), "Height": float64(40)}}}}
	if len(flatten(tree, "observation")) != 0 {
		t.Fatal("offscreen button offered")
	}
}
