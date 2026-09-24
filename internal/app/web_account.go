package app

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"atlas/internal/app/web"
	mlUC "atlas/internal/application/ml"
	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/infrastructure/persistence"

	"go.uber.org/zap"
)

// Account page: portfolios, the ML model picker and engine toggle, background-polling status, and
// the three whole-database operations (backup, restore, flush). Split out of web_handlers.go,
// which had grown past 1300 lines. Restore and flush are confirmed in the browser, and flush also
// refuses server-side without a fresh backup (persistence.FlushAll).

// accountPageHandler renders the Account page.
func (a *Application) accountPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	a.renderAccountPage(w, r, "", "")
}

func (a *Application) renderAccountPage(w http.ResponseWriter, r *http.Request, errMsg, successMsg string) {
	if successMsg == "" {
		successMsg = readFlash(w, r)
	}
	// The request-scoped portfolio view, like every other page that shows portfolio state. The
	// shared use case is loaded once at startup, so reading its list here showed a snapshot from
	// then: a portfolio created a moment earlier — by this very page's own form, which does use a
	// scoped view — was missing from the list the redirect landed on, and renames and deletions
	// could be just as invisible.
	if scoped, err := a.forPortfolio(r); err == nil {
		a = scoped
	} else {
		a.Logger.Warn("could not load the current portfolio list for the account page", zap.Error(err))
	}
	trainerConfigured := a.Trainer != nil
	var trainerStatus web.TrainerStatus
	var trainerErr string
	if trainerConfigured {
		if status, err := a.Trainer.Status(); err != nil {
			trainerErr = err.Error()
		} else {
			trainerStatus = web.TrainerStatus{
				Running: status.Running, StartedAt: status.StartedAt, FinishedAt: status.FinishedAt,
				ExitCode: status.ExitCode, Output: status.Output, Failed: status.Failed,
			}
		}
	}

	var backupExists bool
	var lastBackupAt time.Time
	if a.Storage != nil {
		if at, ok, err := a.Storage.LastBackupAt(); err != nil {
			a.Logger.Warn("failed to read last backup time", zap.Error(err))
		} else {
			backupExists, lastBackupAt = ok, at
		}
	}
	hasBackup := backupExists && time.Since(lastBackupAt) <= persistence.FlushBackupFreshness

	// Which replica is doing the background work. Best-effort: not knowing is a display gap, not
	// a reason to fail the whole page.
	leader := ""
	if a.Storage != nil {
		if holder, _, err := a.Storage.CurrentLeader(); err != nil {
			a.Logger.Warn("failed to read the current polling leader", zap.Error(err))
		} else {
			leader = holder
		}
	}

	web.Render(w, "account", web.AccountPage{
		Leader:                leader,
		LeaderIsSelf:          leader != "" && leader == a.Leader.Identity(),
		Lang:                  a.resolveLang(r),
		Error:                 errMsg,
		Success:               successMsg,
		CorrectionModels:      a.modelOptions(mlUC.RoleCorrection, a.ML.CurrentModel()),
		RecoveryModels:        a.modelOptions(mlUC.RoleRecovery, a.ML.CurrentRecoveryModel()),
		CorrectionModelActive: a.ML.CurrentModel() != nil,
		RecoveryModelActive:   a.ML.CurrentRecoveryModel() != nil,
		MLEnabled:             a.Orchestrator.MLEnabled(),
		TrainerConfigured:     trainerConfigured,
		TrainerStatus:         trainerStatus,
		TrainerError:          trainerErr,
		HasBackup:             hasBackup,
		BackupExists:          backupExists,
		LastBackupAt:          lastBackupAt,
		Portfolios:            a.Portfolio.PortfolioList(),
		MaxPortfolios:         portfolioUC.MaxPortfolios,
	})
}

// modelOptions lists every trained model file available for role (see ml.ListModels) as
// web.ModelOption values for the Account page's dropdown, marking whichever one matches
// active's Version as the currently-loaded one. active is nil when that role has no model
// loaded (the deterministic stub/zero default applies) — every option is then simply not
// active, not an error. A directory-read failure logs and renders an empty list rather than
// failing the whole Account page — the model picker is an enhancement, not a hard dependency.
func (a *Application) modelOptions(role mlUC.Role, active *mlUC.OnnxModel) []web.ModelOption {
	infos, err := mlUC.ListModels(a.Config.MLModelsDir, role)
	if err != nil {
		a.Logger.Warn("failed to list ML models", zap.String("role", string(role)), zap.Error(err))
		return nil
	}
	options := make([]web.ModelOption, len(infos))
	for i, info := range infos {
		options[i] = web.ModelOption{
			Filename:    info.Filename,
			Label:       info.ShortLabel(),
			Description: info.Description(),
			Active:      active != nil && info.Version == active.Version,
		}
	}
	return options
}

// mlEngineToggleHandler flips the decision engine's ML-blend toggle from the Account page — the
// switch takes effect immediately (decision.Orchestrator.SetMLEnabled hot-swaps a runtime flag,
// no restart needed) and is durably recorded (migrations/015) so it survives one. Model files
// stay loaded and usable via /predict either way; this only controls whether BuildRecommendation
// factors them into BUY/SELL/HOLD.
func (a *Application) mlEngineToggleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	enabled := r.FormValue("enabled") == "true"

	// Persist BEFORE changing the running engine, the same order mlModelActivateHandler uses and
	// for the same reason: the durable setting is what every replica reconciles against and what
	// survives a restart, so a write that failed while the toggle flipped anyway leaves the
	// operator told "done" and the background refresh free to flip it back. Failing first changes
	// nothing anywhere and says so.
	// No storage configured is a degraded-but-supported state elsewhere on this page, so the
	// toggle still works in memory there rather than refusing outright.
	if a.Storage != nil {
		if err := a.Storage.SetMLEnabled(enabled); err != nil {
			a.Logger.Error("failed to persist ML-enabled setting", zap.Bool("enabled", enabled), zap.Error(err))
			a.renderAccountPage(w, r, a.tr(r, "msg.ml_setting_failed", err), "")
			return
		}
	}
	a.Orchestrator.SetMLEnabled(enabled)

	if enabled {
		a.renderAccountPage(w, r, "", a.tr(r, "msg.ml_enabled"))
	} else {
		a.renderAccountPage(w, r, "", a.tr(r, "msg.ml_disabled"))
	}
}

// mlModelActivateHandler loads the model the Account page's dropdown submitted and, on success,
// hot-swaps it into the running ML use case (no restart needed — see ml.UseCase.SetModel) and
// durably records the choice (migrations/011) so it survives one. The submitted filename is
// re-validated against a fresh ml.ListModels scan rather than trusted as-is, so this can't be
// used to load an arbitrary path outside ML_MODELS_DIR.
func (a *Application) mlModelActivateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	roleValue := r.FormValue("role")
	filename := r.FormValue("filename")

	role, ok := parseModelRole(roleValue)
	if !ok {
		a.renderAccountPage(w, r, a.tr(r, "msg.unknown_model_role"), "")
		return
	}

	infos, err := mlUC.ListModels(a.Config.MLModelsDir, role)
	if err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.models_dir_failed", err), "")
		return
	}
	found := false
	for _, info := range infos {
		if info.Filename == filename {
			found = true
			break
		}
	}
	if !found {
		a.renderAccountPage(w, r, a.tr(r, "msg.model_gone"), "")
		return
	}

	path := a.Config.MLModelsDir + "/" + filename
	model, err := mlUC.LoadOnnxModel(path, a.Config.LibOnnxRuntimePath)
	if err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.model_load_failed", err), "")
		return
	}

	// Record the choice BEFORE swapping. The durable selection is what every replica reconciles
	// against (see reconcileModelSelection), so a swap that happened only here would leave this
	// replica serving a model no other replica knows about — and a failed write would make the
	// difference invisible. Persisting first means a failure is reported to the operator with
	// nothing changed anywhere, which is the recoverable direction.
	if err := a.Storage.SetActiveModelSelection(string(role), filename); err != nil {
		_ = model.Close()
		a.Logger.Error("failed to persist ML model selection",
			zap.String("role", string(role)), zap.Error(err))
		a.renderAccountPage(w, r, a.tr(r, "msg.model_selection_failed", err), "")
		return
	}

	var previous *mlUC.OnnxModel
	if role == mlUC.RoleCorrection {
		previous = a.ML.SetModel(model)
	} else {
		previous = a.ML.SetRecoveryModel(model)
	}
	if previous != nil {
		if err := previous.Close(); err != nil {
			a.Logger.Warn("failed to close previous ML model", zap.String("role", string(role)), zap.Error(err))
		}
	}

	a.renderAccountPage(w, r, "", a.tr(r, "msg.model_activated", model.Version, roleValue))
}

// parseModelRole validates a form-submitted role value against the two real roles ml.ListModels
// understands — used by both mlModelActivateHandler and mlModelDeleteHandler so an unrecognized
// value is rejected the same way in either place.
func parseModelRole(value string) (mlUC.Role, bool) {
	switch value {
	case string(mlUC.RoleCorrection):
		return mlUC.RoleCorrection, true
	case string(mlUC.RoleRecovery):
		return mlUC.RoleRecovery, true
	default:
		return "", false
	}
}

// mlModelDeleteHandler removes a trained model file the Account page's dropdown submitted —
// via the trainer sidecar (see internal/infrastructure/trainer), since atlas-api's own
// ./training/models mount is read-only by design (docker-compose.yml). Refuses to delete
// whichever model is currently active for that role: that would leave the running (in-memory)
// model's file gone from disk, breaking a future restart or re-activation without anything
// visibly wrong right now — the safer failure mode is refusing up front.
func (a *Application) mlModelDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.Trainer == nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.model_delete_unavailable"), "")
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	roleValue := r.FormValue("role")
	filename := r.FormValue("filename")

	role, ok := parseModelRole(roleValue)
	if !ok {
		a.renderAccountPage(w, r, a.tr(r, "msg.unknown_model_role"), "")
		return
	}

	active := a.ML.CurrentModel()
	if role == mlUC.RoleRecovery {
		active = a.ML.CurrentRecoveryModel()
	}
	found := false
	for _, info := range a.modelOptions(role, active) {
		if info.Filename != filename {
			continue
		}
		found = true
		if info.Active {
			a.renderAccountPage(w, r, a.tr(r, "msg.model_active_cannot_delete", roleValue), "")
			return
		}
	}
	if !found {
		a.renderAccountPage(w, r, a.tr(r, "msg.model_gone"), "")
		return
	}

	if err := a.Trainer.Delete(filename); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.model_delete_failed", err), "")
		return
	}
	a.renderAccountPage(w, r, "", a.tr(r, "msg.model_deleted", filename))
}

// mlTrainHandler triggers a new training run via the atlas-trainer sidecar (see cmd/trainer,
// TRAINER_URL) — the real action behind the Account dashboard's "Train new candidate" button.
// This only ever produces a new, uniquely-versioned candidate model file
// (training/retrain.sh never touches which model is active); promoting one is still the
// separate mlModelActivateHandler action above.
func (a *Application) mlTrainHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.Trainer == nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.training_unavailable"), "")
		return
	}
	if err := a.Trainer.Trigger(); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.training_failed", err), "")
		return
	}
	a.flashRedirect(w, r, "/dashboard/account",
		"Training started — this can take a few minutes (longer on the very first run). Refresh this page to check progress.")
}

// extendWriteDeadline lifts the connection's write deadline for the handlers above — best-effort:
// logs and continues if the underlying ResponseWriter doesn't support deadlines, the same
// "optional enhancement, never a hard failure" stance the rest of this file takes for
// non-critical paths.
func (a *Application) extendWriteDeadline(w http.ResponseWriter, d time.Duration) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d)); err != nil {
		a.Logger.Warn("failed to extend write deadline", zap.Error(err))
	}
}

// accountBackupHandler streams every table persistence.TimescaleDB.BackupAll covers as one
// downloadable JSON file — a portable snapshot for disaster recovery or seeding a fresh
// environment. Read-only, so unlike Restore/Flush below it needs no confirmation dialog.
func (a *Application) accountBackupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if a.Storage == nil {
		http.Error(w, "no database configured", http.StatusServiceUnavailable)
		return
	}
	a.extendWriteDeadline(w, backupOperationTimeout)
	filename := fmt.Sprintf("atlas-backup-%s.json", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	// Streamed straight to the client rather than assembled in memory first (see
	// WriteBackup): on a long-lived instance the database is mostly prices and recommendations,
	// and buffering the whole document was what would eventually hit the container memory limit.
	//
	// The cost of streaming is that the status line is already sent when a failure happens, so a
	// mid-stream error cannot become a 500 — it truncates instead. That is why the backup is only
	// *recorded* after a complete write below: a truncated file must never arm FlushAll.
	counter := &countingWriter{inner: w}
	if err := a.Storage.WriteBackup(counter); err != nil {
		a.Logger.Error("backup failed", zap.Int64("bytes_written", counter.written), zap.Error(err))
		if counter.written == 0 {
			a.renderError(w, r, http.StatusInternalServerError, err.Error())
		}
		return
	}
	// Only a fully written response counts as "a backup exists" for FlushAll's safety check —
	// a connection that dropped mid-download leaves the operator with a truncated file, and
	// marking that as a backup would arm the wipe button against a copy nobody can restore.
	if err := a.Storage.RecordBackup(); err != nil {
		a.Logger.Error("failed to record completed backup", zap.Error(err))
	}
}

// countingWriter tracks whether anything has reached the client yet, which is what decides
// between "render an error page" and "the response is already committed, truncate and log".
type countingWriter struct {
	inner   io.Writer
	written int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.inner.Write(p)
	c.written += int64(n)
	return n, err
}

// accountRestoreHandler replaces every row Atlas has with the contents of an uploaded
// accountBackupHandler file — the recovery path after accountFlushHandler, or how to seed a
// fresh environment from an existing one's backup.
func (a *Application) accountRestoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.Storage == nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.no_database"), "")
		return
	}
	a.extendWriteDeadline(w, backupOperationTimeout)
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupRestoreBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, _, err := r.FormFile("backup")
	if err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.restore_choose_file"), "")
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxBackupRestoreBytes+1))
	if err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.upload_read_failed", err), "")
		return
	}
	if len(payload) > maxBackupRestoreBytes {
		a.renderAccountPage(w, r, a.tr(r, "msg.backup_too_large"), "")
		return
	}
	if err := a.Storage.RestoreAll(payload); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.restore_failed", err), "")
		return
	}
	a.reloadCachedState()
	a.renderAccountPage(w, r, "", a.tr(r, "msg.restored"))
}

// accountFlushHandler wipes every table Atlas stores data in — positions, transactions,
// watchlist, price/indicator history, recommendations, ML settings — back to the state a freshly
// migrated database would be in (see persistence.TimescaleDB.FlushAll). Confirmed client-side
// (account.html), and refused server-side without a fresh backup, since there is no undo short of
// restoring one.
func (a *Application) accountFlushHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.Storage == nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.no_database"), "")
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	a.extendWriteDeadline(w, backupOperationTimeout)
	if err := a.Storage.FlushAll(); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.flush_failed", err), "")
		return
	}
	a.reloadCachedState()
	a.renderAccountPage(w, r, "", a.tr(r, "msg.flushed"))
}

// reloadCachedState resyncs every in-memory cache that mirrors a database table (see
// portfolio.UseCase.Reload/watchlist.UseCase.Reload) after a bulk out-of-band change
// (accountFlushHandler/accountRestoreHandler) that per-mutation methods like RecordTransaction
// can't express — the same class of staleness TestRecordTransactionDefaultsTimestampWhenEmpty's
// bug came from, just at bulk scale instead of per-transaction. Logs rather than fails outright:
// the underlying data is already committed either way, so a stale cache is a display bug that
// would sort itself out on the next restart, not silent data loss. ML model *selection* (which
// trained file is active) is intentionally left alone here — resolveMLEnabled/model files
// themselves aren't touched by Flush/Restore, matching the existing "disable without deleting"
// design (see account.ml_engine_heading) — only the enabled/disabled toggle is resynced.
func (a *Application) reloadCachedState() {
	if err := a.Portfolio.Reload(); err != nil {
		a.Logger.Warn("failed to reload portfolio state after bulk data change", zap.Error(err))
	}
	if err := a.Watchlist.Reload(); err != nil {
		a.Logger.Warn("failed to reload watchlist state after bulk data change", zap.Error(err))
	}
	a.Orchestrator.SetMLEnabled(resolveMLEnabled(a.Storage, a.Logger, a.Config.MLEnabled))
}
