package web

// Lang is a supported dashboard display language. Atlas is a single-user personal tool (see
// PROJECT_CONTEXT.md), so this is a simple two-language switch, not a full locale system —
// no pluralization rules, number/date formatting, or RTL support.
type Lang string

const (
	LangEN Lang = "en"
	LangPL Lang = "pl"
)

// DefaultLang is used when no language cookie is set, or it holds an unsupported value.
const DefaultLang = LangEN

// ValidLang reports whether lang is one Atlas actually has translations for — used to reject
// an unrecognized or tampered cookie value rather than silently rendering blank/fallback text
// everywhere.
func ValidLang(lang Lang) bool {
	return lang == LangEN || lang == LangPL
}

// translations maps a translation key to its text in each supported language. English is the
// source of truth: every key must have an "en" entry (enforced by TestEveryTranslationKeyHasBothLanguages).
// A handful of values contain literal HTML (an inline <a> or <code>) — those are rendered via
// the safeHTML template func, not text-escaped, so keep them limited to static markup with no
// user-supplied content.
var translations = map[string]map[Lang]string{
	// Navigation (partials.html)
	"nav.dashboard":       {LangEN: "Dashboard", LangPL: "Panel główny"},
	"nav.portfolio":       {LangEN: "Portfolio", LangPL: "Portfel"},
	"nav.recommendations": {LangEN: "Recommendations", LangPL: "Rekomendacje"},
	"nav.alerts":          {LangEN: "Alerts", LangPL: "Alerty"},
	"nav.backtests":       {LangEN: "Backtests", LangPL: "Backtesty"},
	"nav.watchlist":       {LangEN: "Watchlist", LangPL: "Obserwowane"},
	"nav.account":         {LangEN: "Account", LangPL: "Konto"},
	"nav.theme":           {LangEN: "Theme", LangPL: "Motyw"},
	"nav.theme_light":     {LangEN: "Light", LangPL: "Jasny"},
	"nav.theme_dark":      {LangEN: "Dark", LangPL: "Ciemny"},
	"nav.theme_auto":      {LangEN: "Auto (system)", LangPL: "Automatyczny (systemowy)"},
	"nav.language":        {LangEN: "Language", LangPL: "Język"},
	"nav.language_en":     {LangEN: "English", LangPL: "Angielski"},
	"nav.language_pl":     {LangEN: "Polish", LangPL: "Polski"},
	"nav.logout":          {LangEN: "Log out", LangPL: "Wyloguj"},
	"nav.skip_to_content": {LangEN: "Skip to content", LangPL: "Przejdź do treści"},

	// Shared table headers/labels, reused across multiple pages
	"table.symbol":        {LangEN: "Symbol", LangPL: "Symbol"},
	"table.quantity":      {LangEN: "Quantity", LangPL: "Ilość"},
	"table.avg_cost":      {LangEN: "Avg. Cost", LangPL: "Śr. koszt"},
	"table.current_price": {LangEN: "Current Price", LangPL: "Aktualna cena"},
	"table.pnl":           {LangEN: "P&L", LangPL: "Zysk/strata"},
	"table.total":         {LangEN: "Total", LangPL: "Suma"},
	"table.exposure":      {LangEN: "Exposure", LangPL: "Ekspozycja"},
	// table.drawdown and table.avg_deviation are the dashboard/portfolio Risk table's own
	// simple heuristics (portfolio.RiskSnapshot — see its doc comment), deliberately named for
	// what they actually compute rather than reusing "Drawdown"/"Volatility", which
	// table.max_drawdown/table.volatility below already mean something different and real
	// (backtesting.Result — peak-to-trough drawdown, annualized return stddev).
	"table.drawdown":         {LangEN: "Unrealized Loss / Exposure", LangPL: "Strata niezrealizowana / ekspozycja"},
	"table.avg_deviation":    {LangEN: "Avg. Deviation from Cost", LangPL: "Śr. odchylenie od kosztu"},
	"table.volatility":       {LangEN: "Volatility", LangPL: "Zmienność"},
	"table.type":             {LangEN: "Type", LangPL: "Typ"},
	"table.price":            {LangEN: "Price", LangPL: "Cena"},
	"table.timestamp":        {LangEN: "Timestamp", LangPL: "Znacznik czasu"},
	"table.currency":         {LangEN: "Currency", LangPL: "Waluta"},
	"table.pln_equivalent":   {LangEN: "PLN Equivalent", LangPL: "Równowartość w PLN"},
	"table.action":           {LangEN: "Action", LangPL: "Akcja"},
	"table.confidence":       {LangEN: "Confidence", LangPL: "Pewność"},
	"table.triggered_at":     {LangEN: "Triggered At", LangPL: "Wyzwolono o"},
	"table.reason":           {LangEN: "Reason", LangPL: "Powód"},
	"table.score":            {LangEN: "Score", LangPL: "Wynik"},
	"table.technical":        {LangEN: "Technical", LangPL: "Techniczny"},
	"table.ml":               {LangEN: "ML", LangPL: "ML"},
	"table.portfolio":        {LangEN: "Portfolio", LangPL: "Portfel"},
	"table.risk":             {LangEN: "Risk", LangPL: "Ryzyko"},
	"table.market_condition": {LangEN: "Market Condition", LangPL: "Stan rynku"},
	"table.model":            {LangEN: "Engine", LangPL: "Silnik"},
	"table.ml_model":         {LangEN: "ML model", LangPL: "Model ML"},
	"table.ml_model_none":    {LangEN: "none loaded — stub", LangPL: "brak — placeholder"},
	"table.total_return":     {LangEN: "Total Return", LangPL: "Całkowity zwrot"},
	"table.annual_return":    {LangEN: "Annual Return", LangPL: "Zwrot roczny"},
	"table.sharpe_ratio":     {LangEN: "Sharpe Ratio", LangPL: "Wskaźnik Sharpe'a"},
	"table.max_drawdown":     {LangEN: "Max Drawdown", LangPL: "Maks. obsunięcie kapitału"},
	"table.trades":           {LangEN: "Trades", LangPL: "Transakcje"},
	"table.win_rate":         {LangEN: "Win Rate", LangPL: "Skuteczność"},
	"table.sample_size":      {LangEN: "Sample Size", LangPL: "Liczebność próby"},
	"table.accuracy":         {LangEN: "Accuracy", LangPL: "Dokładność"},
	"table.precision":        {LangEN: "Precision", LangPL: "Precyzja"},
	"table.recall":           {LangEN: "Recall", LangPL: "Czułość"},
	"table.f1_score":         {LangEN: "F1 Score", LangPL: "Wynik F1"},
	"table.roc_auc":          {LangEN: "ROC AUC", LangPL: "ROC AUC"},
	"table.added":            {LangEN: "Added", LangPL: "Dodano"},
	"table.username":         {LangEN: "Username", LangPL: "Nazwa użytkownika"},

	"common.remove":   {LangEN: "Remove", LangPL: "Usuń"},
	"common.choose":   {LangEN: "Choose…", LangPL: "Wybierz…"},
	"common.enabled":  {LangEN: "Enabled", LangPL: "Włączone"},
	"common.disabled": {LangEN: "Disabled", LangPL: "Wyłączone"},
	"common.disclaimer": {
		LangEN: "Decision-support only — Atlas never executes trades.",
		LangPL: "Wyłącznie wsparcie decyzyjne — Atlas nigdy nie realizuje transakcji.",
	},

	// Login (login.html)
	"login.title": {LangEN: "Atlas — Log in", LangPL: "Atlas — Logowanie"},
	"login.subtitle": {
		LangEN: "AI portfolio decision support — sign in to continue.",
		LangPL: "Wsparcie decyzyjne AI dla portfela — zaloguj się, aby kontynuować.",
	},
	"login.username":  {LangEN: "Username", LangPL: "Nazwa użytkownika"},
	"login.password":  {LangEN: "Password", LangPL: "Hasło"},
	"login.totp_code": {LangEN: "2FA code", LangPL: "Kod 2FA"},
	"login.submit":    {LangEN: "Log in", LangPL: "Zaloguj się"},

	// Dashboard (dashboard.html)
	"dashboard.title":     {LangEN: "Atlas — Dashboard", LangPL: "Atlas — Panel główny"},
	"dashboard.heading":   {LangEN: "Dashboard", LangPL: "Panel główny"},
	"dashboard.positions": {LangEN: "Positions", LangPL: "Pozycje"},
	"dashboard.no_positions": {
		LangEN: `No open positions yet — see <a href="/dashboard/portfolio">Portfolio</a> to record a transaction.`,
		LangPL: `Brak otwartych pozycji — przejdź do <a href="/dashboard/portfolio">Portfela</a>, aby zapisać transakcję.`,
	},
	"dashboard.portfolio_risk": {LangEN: "Portfolio Risk", LangPL: "Ryzyko portfela"},

	// Portfolio (portfolio.html)
	"portfolio.title":               {LangEN: "Atlas — Portfolio", LangPL: "Atlas — Portfel"},
	"portfolio.heading":             {LangEN: "Portfolio", LangPL: "Portfel"},
	"portfolio.no_positions":        {LangEN: "No open positions yet.", LangPL: "Brak otwartych pozycji."},
	"portfolio.transaction_history": {LangEN: "Transaction History", LangPL: "Historia transakcji"},
	"portfolio.no_transactions":     {LangEN: "No transactions recorded yet.", LangPL: "Nie zapisano jeszcze żadnych transakcji."},
	"portfolio.record_transaction":  {LangEN: "Record a transaction", LangPL: "Zapisz transakcję"},
	"portfolio.record_transaction_hint": {
		LangEN: `The only real portfolio-write path — matches <code>POST /portfolio</code>.`,
		LangPL: `Jedyna rzeczywista ścieżka zapisu do portfela — odpowiada <code>POST /portfolio</code>.`,
	},
	"portfolio.record_submit": {LangEN: "Record transaction", LangPL: "Zapisz transakcję"},
	"confirm.remove_position": {
		LangEN: "Remove %s entirely? Transaction history is kept, only the current position is cleared.",
		LangPL: "Usunąć %s całkowicie? Historia transakcji zostaje zachowana, czyszczona jest tylko bieżąca pozycja.",
	},
	"confirm.remove_transaction": {
		LangEN: "Remove this transaction and recompute %s's position from what remains?",
		LangPL: "Usunąć tę transakcję i przeliczyć pozycję %s na podstawie pozostałych?",
	},
	"confirm.remove_transactions_bulk": {
		LangEN: "Remove %d selected transaction(s) and recompute the affected positions from what remains?",
		LangPL: "Usunąć %d wybranych transakcji i przeliczyć dotknięte pozycje na podstawie pozostałych?",
	},
	"confirm.remove_positions_bulk": {
		LangEN: "Remove %d selected position(s) entirely? Transaction history is kept, only the current positions are cleared.",
		LangPL: "Usunąć %d wybranych pozycji całkowicie? Historia transakcji zostaje zachowana, czyszczone są tylko bieżące pozycje.",
	},
	"portfolio.delete_selected": {LangEN: "Delete selected", LangPL: "Usuń zaznaczone"},
	"table.select_all":          {LangEN: "Select all", LangPL: "Zaznacz wszystkie"},

	// Recommendations (recommendations.html)
	"recommendations.title":     {LangEN: "Atlas — Recommendations", LangPL: "Atlas — Rekomendacje"},
	"recommendations.heading":   {LangEN: "Recommendations", LangPL: "Rekomendacje"},
	"recommendations.submit":    {LangEN: "Get recommendation", LangPL: "Pobierz rekomendację"},
	"recommendations.reasoning": {LangEN: "Reasoning", LangPL: "Uzasadnienie"},
	"watchlist.dropdown_hint": {
		LangEN: `Add symbols to your <a href="/dashboard/watchlist">watchlist</a> for a dropdown here.`,
		LangPL: `Dodaj symbole do <a href="/dashboard/watchlist">listy obserwowanych</a>, aby pojawiła się tu lista rozwijana.`,
	},

	// Alerts (alerts.html)
	"alerts.title":   {LangEN: "Atlas — Alerts", LangPL: "Atlas — Alerty"},
	"alerts.heading": {LangEN: "Alerts", LangPL: "Alerty"},
	"alerts.subtitle": {
		LangEN: "Every watchlist symbol whose current recommendation isn't HOLD.",
		LangPL: "Każdy symbol z listy obserwowanych, którego aktualna rekomendacja to nie HOLD.",
	},
	"alerts.empty": {
		LangEN: "No active alerts — every watched symbol is currently HOLD, or the watchlist is empty.",
		LangPL: "Brak aktywnych alertów — każdy obserwowany symbol ma obecnie rekomendację HOLD albo lista obserwowanych jest pusta.",
	},

	// Backtests (backtests.html)
	"backtests.title":         {LangEN: "Atlas — Backtests", LangPL: "Atlas — Backtesty"},
	"backtests.heading":       {LangEN: "Backtests", LangPL: "Backtesty"},
	"backtests.window":        {LangEN: "Window (bars)", LangPL: "Okno (świece)"},
	"backtests.capital":       {LangEN: "Initial capital", LangPL: "Kapitał początkowy"},
	"backtests.submit":        {LangEN: "Run backtest", LangPL: "Uruchom backtest"},
	"backtests.ml_evaluation": {LangEN: "ML Evaluation", LangPL: "Ocena modelu ML"},
	"backtests.no_trades":     {LangEN: "No trades over this window.", LangPL: "Brak transakcji w tym oknie."},

	// Watchlist (watchlist.html)
	"watchlist.title":      {LangEN: "Atlas — Watchlist", LangPL: "Atlas — Obserwowane"},
	"watchlist.heading":    {LangEN: "Watchlist", LangPL: "Obserwowane"},
	"watchlist.empty":      {LangEN: "Watchlist is empty.", LangPL: "Lista obserwowanych jest pusta."},
	"watchlist.add_symbol": {LangEN: "Add a symbol", LangPL: "Dodaj symbol"},
	"watchlist.add_submit": {LangEN: "Add", LangPL: "Dodaj"},
	"confirm.remove_watchlist_item": {
		LangEN: "Stop watching %s?",
		LangPL: "Przestać obserwować %s?",
	},
	"confirm.remove_watchlist_bulk": {
		LangEN: "Stop watching %d selected symbol(s)?",
		LangPL: "Przestać obserwować %d wybranych symboli?",
	},

	// Account (account.html)
	"account.title":            {LangEN: "Atlas — Account", LangPL: "Atlas — Konto"},
	"account.heading":          {LangEN: "Account", LangPL: "Konto"},
	"account.two_factor":       {LangEN: "Two-factor authentication", LangPL: "Uwierzytelnianie dwuskładnikowe"},
	"account.disable_2fa":      {LangEN: "Disable two-factor authentication", LangPL: "Wyłącz uwierzytelnianie dwuskładnikowe"},
	"account.disable_2fa_hint": {LangEN: "Requires your current password to confirm.", LangPL: "Wymaga potwierdzenia aktualnym hasłem."},
	"account.current_password": {LangEN: "Current password", LangPL: "Aktualne hasło"},
	"account.disable_submit":   {LangEN: "Disable 2FA", LangPL: "Wyłącz 2FA"},
	"account.enable_2fa":       {LangEN: "Enable two-factor authentication", LangPL: "Włącz uwierzytelnianie dwuskładnikowe"},
	"account.enable_2fa_hint": {
		LangEN: "Adds a verification code from an authenticator app (Google Authenticator, Authy, etc.) to every login.",
		LangPL: "Dodaje do każdego logowania kod weryfikacyjny z aplikacji uwierzytelniającej (Google Authenticator, Authy itp.).",
	},
	"account.setup_submit": {LangEN: "Set up 2FA", LangPL: "Skonfiguruj 2FA"},

	"account.ml_models_heading": {LangEN: "ML models", LangPL: "Modele ML"},
	"account.ml_models_hint": {
		LangEN: "Pick which trained model is active for each prediction. Takes effect immediately, no restart needed.",
		LangPL: "Wybierz, który wytrenowany model jest aktywny dla każdej predykcji. Działa natychmiast, bez restartu.",
	},
	"account.ml_correction_heading": {LangEN: "Correction probability", LangPL: "Prawdopodobieństwo korekty"},
	"account.ml_recovery_heading":   {LangEN: "Recovery probability", LangPL: "Prawdopodobieństwo odbicia"},
	"account.ml_no_models":          {LangEN: "No trained models found in the models directory.", LangPL: "Nie znaleziono wytrenowanych modeli w katalogu modeli."},
	"account.ml_none_active":        {LangEN: "None active — using the deterministic stub.", LangPL: "Brak aktywnego — używany deterministyczny placeholder."},
	"account.ml_active_badge":       {LangEN: "active", LangPL: "aktywny"},
	"account.ml_activate_submit":    {LangEN: "Activate", LangPL: "Aktywuj"},
	"account.ml_delete_submit":      {LangEN: "Remove", LangPL: "Usuń"},
	"confirm.remove_model": {
		LangEN: "Delete %s permanently? This can't be undone.",
		LangPL: "Usunąć %s na stałe? Tej operacji nie można cofnąć.",
	},

	"account.train_heading": {LangEN: "Train new candidate", LangPL: "Wytrenuj nowego kandydata"},
	"account.train_hint": {
		LangEN: "Exports fresh training data and trains a new, uniquely-versioned candidate for both models. Never changes what's active — pick it above once it looks good.",
		LangPL: "Eksportuje świeże dane treningowe i trenuje nowego, jednoznacznie wersjonowanego kandydata dla obu modeli. Nigdy nie zmienia aktywnego modelu — wybierz go powyżej, gdy wygląda dobrze.",
	},
	"account.train_submit": {LangEN: "Train now", LangPL: "Trenuj teraz"},
	"account.train_not_configured": {
		LangEN: "Not configured — set TRAINER_URL and start the atlas-trainer service to enable this, or run training/retrain.sh by hand/cron instead.",
		LangPL: "Nie skonfigurowano — ustaw TRAINER_URL i uruchom usługę atlas-trainer, żeby to włączyć, albo uruchom training/retrain.sh ręcznie/przez cron.",
	},
	"account.train_unreachable": {LangEN: "Trainer isn't reachable right now:", LangPL: "Trener jest teraz nieosiągalny:"},
	"account.train_running":     {LangEN: "Training is currently running…", LangPL: "Trening jest w trakcie…"},
	"account.train_last_result": {LangEN: "Last run", LangPL: "Ostatnie uruchomienie"},
	"account.train_succeeded":   {LangEN: "succeeded", LangPL: "zakończone sukcesem"},
	"account.train_failed":      {LangEN: "failed", LangPL: "nieudane"},
	"account.train_output":      {LangEN: "Output", LangPL: "Wynik"},

	// TOTP enrollment (totp_enroll.html)
	"totp.title":   {LangEN: "Atlas — Set up 2FA", LangPL: "Atlas — Konfiguracja 2FA"},
	"totp.heading": {LangEN: "Set up two-factor authentication", LangPL: "Konfiguracja uwierzytelniania dwuskładnikowego"},
	"totp.scan_hint": {
		LangEN: "Scan this QR code with an authenticator app (Google Authenticator, Authy, etc.):",
		LangPL: "Zeskanuj ten kod QR aplikacją uwierzytelniającą (Google Authenticator, Authy itp.):",
	},
	"totp.manual_hint":     {LangEN: "Can't scan? Enter this key manually:", LangPL: "Nie możesz zeskanować? Wpisz ten klucz ręcznie:"},
	"totp.confirm_heading": {LangEN: "Confirm", LangPL: "Potwierdź"},
	"totp.confirm_hint": {
		LangEN: "Enter the current 6-digit code from the app to finish enabling 2FA.",
		LangPL: "Wpisz aktualny 6-cyfrowy kod z aplikacji, aby zakończyć włączanie 2FA.",
	},
	"totp.verification_code": {LangEN: "Verification code", LangPL: "Kod weryfikacyjny"},
	"totp.submit":            {LangEN: "Confirm and enable", LangPL: "Potwierdź i włącz"},

	// Recommendation/trade action labels — applied via actionLabel wherever a raw BUY/SELL/HOLD/
	// ACCUMULATE/REDUCE/WAIT value is displayed (recommendations, alerts, backtests trades, the
	// portfolio transaction table and its manual-entry form). The underlying value used for
	// scoring/CSS-class matching (actionClass) and form submission is never translated — only
	// this display label is.
	"action.BUY":        {LangEN: "BUY", LangPL: "KUPNO"},
	"action.SELL":       {LangEN: "SELL", LangPL: "SPRZEDAŻ"},
	"action.HOLD":       {LangEN: "HOLD", LangPL: "TRZYMAJ"},
	"action.ACCUMULATE": {LangEN: "ACCUMULATE", LangPL: "DOKUPUJ"},
	"action.REDUCE":     {LangEN: "REDUCE", LangPL: "REDUKUJ"},
	"action.WAIT":       {LangEN: "WAIT", LangPL: "CZEKAJ"},
}

// T looks up key for lang, falling back to English, then to the key itself if the key genuinely
// isn't in the dictionary at all — a missing translation should surface as an odd string in the
// UI, not crash template rendering.
func T(lang Lang, key string) string {
	if strs, ok := translations[key]; ok {
		if s, ok := strs[lang]; ok {
			return s
		}
		if s, ok := strs[LangEN]; ok {
			return s
		}
	}
	return key
}

// actionLabel translates a raw decision-engine/transaction action value (BUY, SELL, HOLD,
// ACCUMULATE, REDUCE, WAIT) for display. Unknown values pass through unchanged rather than
// erroring, since new action types are a domain change, not a template bug.
func actionLabel(lang Lang, action string) string {
	if strs, ok := translations["action."+action]; ok {
		if s, ok := strs[lang]; ok {
			return s
		}
	}
	return action
}
