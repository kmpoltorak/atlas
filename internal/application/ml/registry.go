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
}

// Description renders a short, human-readable summary for a dropdown/list UI — every figure in
// it comes straight from the metadata sidecar train.py wrote at training time.
func (m ModelInfo) Description() string {
	return fmt.Sprintf("%s — trained %s — holdout AUC %.2f, accuracy %.2f (train n=%d, holdout n=%d)",
		m.Version, m.TrainedAt.Format("2006-01-02 15:04"), m.HoldoutAUC, m.HoldoutAccuracy, m.RowCount, m.HoldoutRowCount)
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
			Filename:        strings.TrimSuffix(name, ".json") + ".onnx",
			Version:         meta.Version,
			TrainedAt:       time.Unix(int64(meta.TrainedAt), 0),
			RowCount:        meta.RowCount,
			HoldoutRowCount: meta.HoldoutRowCount,
			HoldoutAUC:      meta.HoldoutAUC,
			HoldoutAccuracy: meta.HoldoutAccuracy,
			LabelDefinition: meta.LabelDefinition,
		})
	}

	sort.Slice(infos, func(i, j int) bool { return infos[i].TrainedAt.After(infos[j].TrainedAt) })
	return infos, nil
}
