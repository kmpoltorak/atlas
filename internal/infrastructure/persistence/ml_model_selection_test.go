package persistence

import "testing"

func TestGetActiveModelSelectionReturnsNotOkWhenNoneSet(t *testing.T) {
	db := connectTestDB(t)

	_, ok, err := db.GetActiveModelSelection("correction-unused-role")
	if err != nil {
		t.Fatalf("GetActiveModelSelection failed: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a role that was never selected")
	}
}

func TestSetActiveModelSelectionThenGetRoundTrips(t *testing.T) {
	db := connectTestDB(t)

	if err := db.SetActiveModelSelection("correction-test", "atlas-ml-v1.onnx"); err != nil {
		t.Fatalf("SetActiveModelSelection failed: %v", err)
	}

	filename, ok, err := db.GetActiveModelSelection("correction-test")
	if err != nil {
		t.Fatalf("GetActiveModelSelection failed: %v", err)
	}
	if !ok || filename != "atlas-ml-v1.onnx" {
		t.Fatalf("expected (atlas-ml-v1.onnx, true), got (%q, %v)", filename, ok)
	}
}

func TestSetActiveModelSelectionOverwritesPreviousChoice(t *testing.T) {
	db := connectTestDB(t)

	if err := db.SetActiveModelSelection("recovery-test", "atlas-ml-recovery-v1.onnx"); err != nil {
		t.Fatalf("SetActiveModelSelection failed: %v", err)
	}
	if err := db.SetActiveModelSelection("recovery-test", "atlas-ml-recovery-v2.onnx"); err != nil {
		t.Fatalf("SetActiveModelSelection failed: %v", err)
	}

	filename, ok, err := db.GetActiveModelSelection("recovery-test")
	if err != nil {
		t.Fatalf("GetActiveModelSelection failed: %v", err)
	}
	if !ok || filename != "atlas-ml-recovery-v2.onnx" {
		t.Fatalf("expected the second selection to win, got (%q, %v)", filename, ok)
	}
}
