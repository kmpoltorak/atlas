package ml

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Role identifies which of the two independent optional models (see UseCase) a trained model
// file belongs to.
type Role string

const (
	RoleCorrection Role = "correction"
	RoleRecovery   Role = "recovery"
)

// ModelInfo describes one trained model file discovered on disk, built entirely from its real
// metadata sidecar (see training/train.py's metadata dict) — every number here is what that
// training run actually produced, never a fabricated or hand-edited value.
type ModelInfo struct {
	// Filename is the .onnx file's base name (e.g. "atlas-ml-v20260808120000.onnx"), suitable
	// for joining with a models directory or passing back from a form submission.
	Filename        string
	Version         string
	TrainedAt       time.Time
	RowCount        int
	HoldoutRowCount int
	HoldoutAUC      float64
	HoldoutAccuracy float64
	LabelDefinition string

	// SymbolBaseRateAUC is the holdout AUC of a predictor using no features at all, only each
	// symbol's training-set positive rate (see training/train.py's symbol_base_rate_auc). Nil
	// for models trained before that baseline existed — unknown, which is not the same as
	// passing, so Description says so rather than staying silent.
	SymbolBaseRateAUC *float64

	// BeatsSymbolBaseRate is train.py's own verdict on HoldoutAUC vs SymbolBaseRateAUC. Nil
	// alongside SymbolBaseRateAUC for pre-baseline models.
	BeatsSymbolBaseRate *bool
}

// Description renders a short, human-readable summary for a dropdown/list UI — every figure in
// it comes straight from the metadata sidecar train.py wrote at training time.
//
// Accuracy is deliberately not the headline. On a target where roughly 6% of bars precede a
// correction, "always predict no correction" scores ~94%, so a high accuracy here says almost
// nothing and reads as reassurance — this listing is the exact screen where a human decides
// whether to promote a model, so it leads with AUC against the symbol-base-rate baseline, the
// number that says whether the model learned price dynamics or just memorised which symbols
// tend to fall.
func (m ModelInfo) Description() string {
	verdict := "baseline unknown (trained before the baseline check existed)"
	if m.SymbolBaseRateAUC != nil {
		verdict = fmt.Sprintf("vs symbol-base-rate baseline %.2f", *m.SymbolBaseRateAUC)
		if m.BeatsSymbolBaseRate != nil && !*m.BeatsSymbolBaseRate {
			verdict += " — DOES NOT BEAT BASELINE"
		}
	}
	return fmt.Sprintf("%s — trained %s — holdout AUC %.2f %s (accuracy %.2f; train n=%d, holdout n=%d)",
		m.Version, m.TrainedAt.Format("2006-01-02 15:04"), m.HoldoutAUC, verdict,
		m.HoldoutAccuracy, m.RowCount, m.HoldoutRowCount)
}

// trainingMetadata mirrors training/train.py's full metadata dict — a superset of the
// modelMetadata struct onnx.go uses to validate feature_order at load time; this one is only
// used for display, never to decide whether a model is safe to load.
type trainingMetadata struct {
	Version         string  `json:"version"`
	TrainedAt       float64 `json:"trained_at"`
	RowCount        int     `json:"row_count"`
	HoldoutRowCount int     `json:"holdout_row_count"`
	HoldoutAUC      float64 `json:"holdout_auc"`
	HoldoutAccuracy float64 `json:"holdout_accuracy"`
	LabelDefinition string  `json:"label_definition"`

	// Pointers, not values: these two are absent from sidecars written before the baseline
	// check existed, and a zero-valued 0.0 AUC would read as "the baseline was hopeless, this
	// model is great" — the exact opposite of the truth. Absent must stay distinguishable.
	SymbolBaseRateAUC   *float64 `json:"symbol_base_rate_auc"`
	BeatsSymbolBaseRate *bool    `json:"beats_symbol_base_rate"`
}

// ListModels scans dir for metadata sidecars (training/train.py's atlas-ml[-recovery]-<version>
// .json naming) belonging to role, returning them newest-trained-first. A directory that
// doesn't exist yet returns an empty, non-error result — ML_MODELS_DIR is itself an optional
// mount, matching every other optional-dependency pattern in this package. A single unreadable
// or malformed sidecar is skipped rather than failing the whole listing, since one bad file
// shouldn't hide every other valid model from the picker.
func ListModels(dir string, role Role) ([]ModelInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read models dir %s: %w", dir, err)
	}

	var infos []ModelInfo
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		// Both prefixes start with "atlas-ml-", so recovery files are excluded from the
		// correction role explicitly rather than by prefix match alone.
		isRecovery := strings.HasPrefix(name, "atlas-ml-recovery-")
		isCorrection := strings.HasPrefix(name, "atlas-ml-") && !isRecovery
		if (role == RoleRecovery && !isRecovery) || (role == RoleCorrection && !isCorrection) {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var meta trainingMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}

		infos = append(infos, ModelInfo{
			Filename:            strings.TrimSuffix(name, ".json") + ".onnx",
			Version:             meta.Version,
			TrainedAt:           time.Unix(int64(meta.TrainedAt), 0),
			RowCount:            meta.RowCount,
			HoldoutRowCount:     meta.HoldoutRowCount,
			HoldoutAUC:          meta.HoldoutAUC,
			HoldoutAccuracy:     meta.HoldoutAccuracy,
			LabelDefinition:     meta.LabelDefinition,
			SymbolBaseRateAUC:   meta.SymbolBaseRateAUC,
			BeatsSymbolBaseRate: meta.BeatsSymbolBaseRate,
		})
	}

	sort.Slice(infos, func(i, j int) bool { return infos[i].TrainedAt.After(infos[j].TrainedAt) })
	return infos, nil
}
