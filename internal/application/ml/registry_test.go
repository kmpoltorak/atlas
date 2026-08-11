package ml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTrainingMetadataFixture(t *testing.T, dir, name string, meta trainingMetadata) {
	t.Helper()
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal fixture metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

func TestListModelsReturnsEmptyForMissingDirectory(t *testing.T) {
	infos, err := ListModels(filepath.Join(t.TempDir(), "does-not-exist"), RoleCorrection)
	if err != nil {
		t.Fatalf("expected no error for a missing directory, got %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("expected no models, got %d", len(infos))
	}
}

func TestListModelsFiltersByRoleAndSortsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	writeTrainingMetadataFixture(t, dir, "atlas-ml-v1.json", trainingMetadata{Version: "v1", TrainedAt: 1000, HoldoutAUC: 0.7})
	writeTrainingMetadataFixture(t, dir, "atlas-ml-v2.json", trainingMetadata{Version: "v2", TrainedAt: 2000, HoldoutAUC: 0.8})
	writeTrainingMetadataFixture(t, dir, "atlas-ml-recovery-v1.json", trainingMetadata{Version: "v1", TrainedAt: 1500, HoldoutAUC: 0.6})

	correction, err := ListModels(dir, RoleCorrection)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(correction) != 2 {
		t.Fatalf("expected 2 correction models, got %d: %+v", len(correction), correction)
	}
	if correction[0].Filename != "atlas-ml-v2.onnx" || correction[1].Filename != "atlas-ml-v1.onnx" {
		t.Fatalf("expected newest-first order, got %+v", correction)
	}

	recovery, err := ListModels(dir, RoleRecovery)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(recovery) != 1 || recovery[0].Filename != "atlas-ml-recovery-v1.onnx" {
		t.Fatalf("expected exactly the recovery model, got %+v", recovery)
	}
}

func TestListModelsSkipsMalformedSidecarsWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	writeTrainingMetadataFixture(t, dir, "atlas-ml-v1.json", trainingMetadata{Version: "v1", TrainedAt: 1000})
	if err := os.WriteFile(filepath.Join(dir, "atlas-ml-broken.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write broken fixture: %v", err)
	}

	infos, err := ListModels(dir, RoleCorrection)
	if err != nil {
		t.Fatalf("expected malformed sidecars to be skipped, not fail the listing: %v", err)
	}
	if len(infos) != 1 || infos[0].Version != "v1" {
		t.Fatalf("expected only the valid model, got %+v", infos)
	}
}

// ShortLabel is what actually renders as a <select> option's visible text (see
// web.ModelOption.Label) — it has to stay short and it has to omit the metrics Description
// carries, since those moved to the option's title attribute instead (a real bug report: the
// full Description as the option text overflowed the dropdown).
func TestModelInfoShortLabelOmitsMetrics(t *testing.T) {
	trainedAt := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	info := ModelInfo{Version: "v20260808120000", TrainedAt: trainedAt, HoldoutAUC: 0.83, HoldoutAveragePrecision: 0.65}
	label := info.ShortLabel()
	if !strings.Contains(label, "v20260808120000") || !strings.Contains(label, "2026-08-08") {
		t.Fatalf("expected label %q to contain the version and trained date", label)
	}
	if strings.Contains(label, "0.83") || strings.Contains(label, "PR AUC") {
		t.Fatalf("expected label %q to omit the metrics Description carries", label)
	}
}

func TestModelInfoDescriptionIncludesRealMetrics(t *testing.T) {
	info := ModelInfo{Version: "v20260808120000", HoldoutAUC: 0.83, HoldoutAveragePrecision: 0.65, RowCount: 222, HoldoutRowCount: 46}
	desc := info.Description()
	for _, want := range []string{"v20260808120000", "0.83", "0.65", "222", "46"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("expected description %q to contain %q", desc, want)
		}
	}
}

// A model that loses to the "just memorise each symbol's base rate" predictor must say so on the
// very screen where a human promotes it — see ModelInfo.Description's doc comment for why
// accuracy alone was actively reassuring on this target.
func TestModelInfoDescriptionFlagsModelsThatLoseToTheBaseline(t *testing.T) {
	baseline, beats := 0.76, false
	info := ModelInfo{
		Version: "v1", HoldoutAUC: 0.707, HoldoutAveragePrecision: 0.12,
		SymbolBaseRateAveragePrecision: &baseline, BeatsSymbolBaseRate: &beats,
	}
	desc := info.Description()
	if !strings.Contains(desc, "0.76") {
		t.Errorf("expected description %q to report the baseline AUC", desc)
	}
	if !strings.Contains(desc, "DOES NOT BEAT BASELINE") {
		t.Errorf("expected description %q to flag that the model loses to the baseline", desc)
	}
}

func TestModelInfoDescriptionMarksPreBaselineModelsUnknown(t *testing.T) {
	desc := ModelInfo{Version: "v1", HoldoutAUC: 0.71}.Description()
	if !strings.Contains(desc, "baseline unknown") {
		t.Errorf("expected description %q to say the baseline is unknown, not imply it passed", desc)
	}
	if strings.Contains(desc, "DOES NOT BEAT BASELINE") {
		t.Errorf("description %q must not claim a verdict it doesn't have", desc)
	}
}

func TestModelInfoDescriptionCreditsAModelThatBeatsTheBaseline(t *testing.T) {
	baseline, beats := 0.55, true
	desc := ModelInfo{Version: "v1", HoldoutAUC: 0.71, SymbolBaseRateAveragePrecision: &baseline, BeatsSymbolBaseRate: &beats}.Description()
	if strings.Contains(desc, "DOES NOT BEAT BASELINE") {
		t.Errorf("description %q wrongly flags a model that does beat the baseline", desc)
	}
	if !strings.Contains(desc, "0.55") {
		t.Errorf("expected description %q to still report the baseline for comparison", desc)
	}
}
