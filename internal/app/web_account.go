package app

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"atlas/internal/app/web"
	authUC "atlas/internal/application/auth"
	mlUC "atlas/internal/application/ml"
	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/infrastructure/persistence"

	qrcode "github.com/skip2/go-qrcode"
	"go.uber.org/zap"
)

// Account page: credentials and 2FA, the ML model picker and engine toggle, background-polling
// status, and the three whole-database operations (backup, restore, flush). Split out of
// web_handlers.go, which had grown past 1300 lines — this is the part with its own distinct
// concern, destructive operations that require re-authentication.

// accountPageHandler shows the current 2FA status and the enable/disable entry points — see
// docs/deployment.md's Implementation status note: 2FA is managed here, from the dashboard,
// not a static env-var secret.
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
	enabled, err := a.Auth.TOTPStatus()
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
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
		AuthEnabled:           a.Auth.Enabled(),
		Leader:                leader,
		LeaderIsSelf:          leader != "" && leader == a.Leader.Identity(),
		Lang:                  a.resolveLang(r),
		Username:              a.Auth.Username,
		TOTPEnabled:           enabled,
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
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	enabled := r.FormValue("enabled") == "true"

	a.Orchestrator.SetMLEnabled(enabled)
	if err := a.Storage.SetMLEnabled(enabled); err != nil {
		// The toggle already took effect in memory; only the durable record failed, so this
		// warns rather than rolling the toggle back — a restart would just re-read ML_ENABLED's
		// default in that case, not silently lose the running setting, same stance as
		// mlModelActivateHandler's SetActiveModelSelection failure below.
		a.Logger.Warn("failed to persist ML-enabled setting", zap.Bool("enabled", enabled), zap.Error(err))
	}

	if enabled {
		a.renderAccountPage(w, r, "", "ML blend enabled — recommendations now factor in ML/portfolio/risk again.")
	} else {
		a.renderAccountPage(w, r, "", "ML blend disabled — recommendations now score from the technical term alone.")
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
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	roleValue := r.FormValue("role")
	filename := r.FormValue("filename")

	role, ok := parseModelRole(roleValue)
	if !ok {
		a.renderAccountPage(w, r, "Unknown model role.", "")
		return
	}

	infos, err := mlUC.ListModels(a.Config.MLModelsDir, role)
	if err != nil {
		a.renderAccountPage(w, r, "Could not read the models directory: "+err.Error(), "")
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
		a.renderAccountPage(w, r, "That model is no longer available — refresh and try again.", "")
		return
	}

	path := a.Config.MLModelsDir + "/" + filename
	model, err := mlUC.LoadOnnxModel(path, a.Config.LibOnnxRuntimePath)
	if err != nil {
		a.renderAccountPage(w, r, "Failed to load that model: "+err.Error(), "")
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
		a.renderAccountPage(w, r, "Could not record that selection, so nothing was changed: "+err.Error(), "")
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

	a.renderAccountPage(w, r, "", "Activated model "+model.Version+" for "+roleValue+".")
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
		a.renderAccountPage(w, r, "Deleting models isn't available (TRAINER_URL is unset) — see training/README.md, or remove the file by hand from training/models/.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	roleValue := r.FormValue("role")
	filename := r.FormValue("filename")

	role, ok := parseModelRole(roleValue)
	if !ok {
		a.renderAccountPage(w, r, "Unknown model role.", "")
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
			a.renderAccountPage(w, r, "Can't remove the model currently active for "+roleValue+" — activate a different one first.", "")
			return
		}
	}
	if !found {
		a.renderAccountPage(w, r, "That model is no longer available — refresh and try again.", "")
		return
	}

	if err := a.Trainer.Delete(filename); err != nil {
		a.renderAccountPage(w, r, "Failed to delete that model: "+err.Error(), "")
		return
	}
	a.renderAccountPage(w, r, "", "Deleted "+filename+".")
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
		a.renderAccountPage(w, r, "Training trigger isn't configured (TRAINER_URL is unset) — see training/README.md to run it by hand, or start the atlas-trainer service to enable this button.", "")
		return
	}
	if err := a.Trainer.Trigger(); err != nil {
		a.renderAccountPage(w, r, "Failed to start training: "+err.Error(), "")
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
		a.renderAccountPage(w, r, "No database configured.", "")
		return
	}
	a.extendWriteDeadline(w, backupOperationTimeout)
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupRestoreBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	defer r.MultipartForm.RemoveAll()
	// Same re-authentication as the flush below: a restore replaces every row Atlas has, which
	// is destructive in exactly the same way even though it is framed as recovery.
	if err := a.Auth.Reauthenticate(r.FormValue("password")); err != nil {
		a.renderAccountPage(w, r, "Enter your account password to confirm restoring a backup.", "")
		return
	}
	file, _, err := r.FormFile("backup")
	if err != nil {
		a.renderAccountPage(w, r, "Choose a backup file to restore.", "")
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxBackupRestoreBytes+1))
	if err != nil {
		a.renderAccountPage(w, r, "Failed to read the uploaded file: "+err.Error(), "")
		return
	}
	if len(payload) > maxBackupRestoreBytes {
		a.renderAccountPage(w, r, "Backup file is too large.", "")
		return
	}
	if err := a.Storage.RestoreAll(payload); err != nil {
		a.renderAccountPage(w, r, "Restore failed: "+err.Error(), "")
		return
	}
	a.reloadCachedState()
	a.renderAccountPage(w, r, "", "Data restored from backup.")
}

// accountFlushHandler wipes every table Atlas stores data in — positions, transactions,
// watchlist, price/indicator history, recommendations, ML/2FA settings — back to the state a
// freshly migrated database would be in (see persistence.TimescaleDB.FlushAll's doc comment for
// exactly what survives, notably login itself). Confirmed client-side (account.html) since there
// is no undo short of a prior backup.
func (a *Application) accountFlushHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.Storage == nil {
		a.renderAccountPage(w, r, "No database configured.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	// A session cookie is not enough for this one: it proves somebody logged in at some point,
	// not that the account holder is the one wiping the database right now.
	if err := a.Auth.Reauthenticate(r.FormValue("password")); err != nil {
		a.renderAccountPage(w, r, "Enter your account password to confirm wiping all data.", "")
		return
	}
	a.extendWriteDeadline(w, backupOperationTimeout)
	if err := a.Storage.FlushAll(); err != nil {
		a.renderAccountPage(w, r, "Flush failed: "+err.Error(), "")
		return
	}
	a.reloadCachedState()
	a.renderAccountPage(w, r, "", "All data has been wiped. Atlas is back to a freshly installed state.")
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

// totpSetupHandler starts a new enrollment (authUC.UseCase.BeginTOTPEnrollment) and renders the
// QR-code confirmation page. Requires a valid session (like every /dashboard/* route), read via
// the same cookie requireAuth already validated.
func (a *Application) totpSetupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	secret, err := a.Auth.BeginTOTPEnrollment(r.Context(), cookie.Value)
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	web.Render(w, "totp_enroll", web.TOTPEnrollPage{Lang: a.resolveLang(r), Secret: secret})
}

// totpQRHandler serves the pending enrollment's QR code as a PNG image — rendered via
// github.com/skip2/go-qrcode (QR encoding is a well-specified but genuinely intricate matrix/
// error-correction algorithm, unlike TOTP's straightforward HMAC construction, so a maintained
// library is the right call here rather than hand-rolling it). Looks up the same pending secret
// PendingTOTPSecret already stashed for this session, so the image matches whatever
// totpSetupHandler rendered the manual key for.
func (a *Application) totpQRHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	secret, found, err := a.Auth.PendingTOTPSecret(r.Context(), cookie.Value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "no pending 2FA enrollment for this session — start again at /dashboard/account/totp/setup", http.StatusNotFound)
		return
	}
	uri := authUC.TOTPProvisioningURI("Atlas", a.Auth.Username, secret)
	png, err := qrcode.Encode(uri, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

// totpConfirmHandler verifies the code the operator typed in against the pending secret and, on
// success, durably activates 2FA (authUC.UseCase.ConfirmTOTPEnrollment) — this is the step that
// proves the authenticator app actually has the secret, not just that a QR code was shown once.
func (a *Application) totpConfirmHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		web.Render(w, "totp_enroll", web.TOTPEnrollPage{Lang: a.resolveLang(r), Error: "Invalid form submission."})
		return
	}
	if err := a.Auth.ConfirmTOTPEnrollment(r.Context(), cookie.Value, r.FormValue("code")); err != nil {
		secret, _, _ := a.Auth.PendingTOTPSecret(r.Context(), cookie.Value)
		web.Render(w, "totp_enroll", web.TOTPEnrollPage{Lang: a.resolveLang(r), Secret: secret, Error: "Incorrect code — try again."})
		return
	}
	a.renderAccountPage(w, r, "", "Two-factor authentication is now enabled.")
}

// totpDisableHandler turns 2FA off after re-verifying the current password (authUC.UseCase.
// DisableTOTP) — a real re-auth check, not just "there's a valid session cookie", since this
// weakens the account.
func (a *Application) totpDisableHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	if err := a.Auth.DisableTOTP(r.FormValue("password")); err != nil {
		a.renderAccountPage(w, r, "Incorrect password.", "")
		return
	}
	a.renderAccountPage(w, r, "", "Two-factor authentication has been disabled.")
}
