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

	// HoldoutAveragePrecision (PR AUC) and SymbolBaseRateAveragePrecision are the pair the
	// promotion decision actually turns on. Atlas alerts on the top of the ranking — a handful
	// of "a correction may be coming" calls — whereas ROC AUC is dominated by how well the vast
	// uneventful middle is ordered, which no alert ever touches. The two genuinely disagree
	// here: the shipped model trails the baseline on ROC AUC while beating it on PR AUC, and at
	// the 25 highest-scoring bars it was right 68% of the time against a 6.5% base rate.
	HoldoutAveragePrecision        float64
	SymbolBaseRateAveragePrecision *float64

	// SymbolBaseRateAUC is the holdout ROC AUC of a predictor using no features at all, only
	// each symbol's training-set positive rate. Nil for models trained before that baseline
	// existed — unknown, which is not the same as passing, so Description says so rather than
	// staying silent.
	SymbolBaseRateAUC *float64

	// BeatsSymbolBaseRate is train.py's verdict, judged on PR AUC. Nil for pre-baseline models.
	BeatsSymbolBaseRate *bool

	// UnseenSymbolAUC is holdout ROC AUC on instruments the model never trained on. The
	// base-rate lookup scores exactly 0.5 here by construction, having no history for a symbol
	// it has never met — so this is what a newly-watchlisted ticker actually gets.
	UnseenSymbolAUC *float64
}

// Description renders a short, human-readable summary for a dropdown/list UI — every figure in
// it comes straight from the metadata sidecar train.py wrote at training time.
//
// Accuracy is deliberately absent. On a target where roughly 6% of bars precede a correction,
// "always predict no correction" scores ~94%, so a high accuracy says almost nothing and reads
// as reassurance — it was the figure that made a model which had learned nothing look excellent.
// This listing is the exact screen where a human decides to promote a model into live
// recommendations, so it leads with PR AUC against the symbol-base-rate baseline: Atlas alerts on
// the top of the ranking, and PR AUC is what measures that, while ROC AUC is dominated by the
// uneventful middle no alert ever touches.
func (m ModelInfo) Description() string {
	verdict := "baseline unknown (trained before the baseline check existed)"
	if m.SymbolBaseRateAveragePrecision != nil {
		verdict = fmt.Sprintf("vs symbol-base-rate baseline %.2f", *m.SymbolBaseRateAveragePrecision)
		if m.BeatsSymbolBaseRate != nil && !*m.BeatsSymbolBaseRate {
			verdict += " — DOES NOT BEAT BASELINE"
		}
	} else if m.SymbolBaseRateAUC != nil {
		verdict = fmt.Sprintf("vs symbol-base-rate ROC baseline %.2f", *m.SymbolBaseRateAUC)
		if m.BeatsSymbolBaseRate != nil && !*m.BeatsSymbolBaseRate {
			verdict += " — DOES NOT BEAT BASELINE"
		}
	}
	unseen := ""
	if m.UnseenSymbolAUC != nil {
		unseen = fmt.Sprintf(", %.2f on unseen symbols", *m.UnseenSymbolAUC)
	}
	return fmt.Sprintf("%s — trained %s — PR AUC %.2f %s (ROC AUC %.2f%s; train n=%d, holdout n=%d)",
		m.Version, m.TrainedAt.Format("2006-01-02 15:04"), m.HoldoutAveragePrecision, verdict,
		m.HoldoutAUC, unseen, m.RowCount, m.HoldoutRowCount)
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
	SymbolBaseRateAUC              *float64 `json:"symbol_base_rate_auc"`
	SymbolBaseRateAveragePrecision *float64 `json:"symbol_base_rate_average_precision"`
	BeatsSymbolBaseRate            *bool    `json:"beats_symbol_base_rate"`
	UnseenSymbolAUC                *float64 `json:"unseen_symbol_auc"`
	HoldoutAveragePrecision        float64  `json:"holdout_average_precision"`
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
			Filename:                       strings.TrimSuffix(name, ".json") + ".onnx",
			Version:                        meta.Version,
			TrainedAt:                      time.Unix(int64(meta.TrainedAt), 0),
			RowCount:                       meta.RowCount,
			HoldoutRowCount:                meta.HoldoutRowCount,
			HoldoutAUC:                     meta.HoldoutAUC,
			HoldoutAccuracy:                meta.HoldoutAccuracy,
			LabelDefinition:                meta.LabelDefinition,
			SymbolBaseRateAUC:              meta.SymbolBaseRateAUC,
			SymbolBaseRateAveragePrecision: meta.SymbolBaseRateAveragePrecision,
			BeatsSymbolBaseRate:            meta.BeatsSymbolBaseRate,
			UnseenSymbolAUC:                meta.UnseenSymbolAUC,
			HoldoutAveragePrecision:        meta.HoldoutAveragePrecision,
		})
	}

	sort.Slice(infos, func(i, j int) bool { return infos[i].TrainedAt.After(infos[j].TrainedAt) })
	return infos, nil
}
