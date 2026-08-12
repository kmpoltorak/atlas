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
	"table.symbol": {LangEN: "Symbol", LangPL: "Symbol"},
	"table.name":   {LangEN: "Name", LangPL: "Nazwa"},
	// Today's live price vs. the prior session's close — see web.SymbolInfo.ChangePercent.
	// Blank until the first alert-poll cycle after a symbol is added has run.
	"table.today_change": {LangEN: "Today", LangPL: "Dzisiaj"},
	// Announced on every symbol link. It names the destination and the new tab, because a link
	// that opens elsewhere without saying so is disorienting for anyone not watching the viewport.
	"table.open_in_yahoo": {LangEN: "Open on Yahoo Finance in a new tab", LangPL: "Otwórz w Yahoo Finance w nowej karcie"},
	"table.quantity":      {LangEN: "Quantity", LangPL: "Ilość"},
	"table.avg_cost":      {LangEN: "Avg. Cost", LangPL: "Śr. koszt"},
	// Position.CurrentPrice (see portfolio.applyTransaction) is normally overlaid with a live
	// quote before this renders (see web_handlers.go's livePricedPositions) — "Current Price",
	// not "Last Transaction Price", is what's actually true most of the time now. table.as_of
	// is the small "as of <date>" line shown under it: the live quote's own timestamp when one
	// was found, falling back to the last transaction's timestamp (the honest label) when not.
	"table.current_price": {LangEN: "Current Price", LangPL: "Aktualna cena"},
	"table.as_of":         {LangEN: "as of", LangPL: "stan na"},
	"table.pnl":           {LangEN: "P&L", LangPL: "Zysk/strata"},
	"table.total":         {LangEN: "Total", LangPL: "Suma"},
	"table.exposure":      {LangEN: "Exposure", LangPL: "Ekspozycja"},
	// table.drawdown and table.avg_deviation are the dashboard/portfolio Risk table's own
	// simple heuristics (portfolio.RiskSnapshot — see its doc comment), deliberately named for
	// what they actually compute rather than reusing "Drawdown"/"Volatility", which
	// table.max_drawdown/table.volatility below already mean something different and real
	// (backtesting.Result — peak-to-trough drawdown, annualized return stddev).
	"table.drawdown":      {LangEN: "Unrealized Loss / Exposure", LangPL: "Strata niezrealizowana / ekspozycja"},
	"table.avg_deviation": {LangEN: "Avg. Deviation from Cost", LangPL: "Śr. odchylenie od kosztu"},
	"table.volatility":    {LangEN: "Volatility", LangPL: "Zmienność"},
	"table.type":          {LangEN: "Type", LangPL: "Typ"},
	// Named "Unit price", not "Price": Transaction.Price is per-unit (see its doc comment) and
	// gets multiplied by Quantity everywhere it's used, so a bare "Price" reads ambiguously as
	// the total cost of the trade.
	"table.price":          {LangEN: "Unit Price", LangPL: "Cena jednostkowa"},
	"table.timestamp":      {LangEN: "Timestamp", LangPL: "Znacznik czasu"},
	"table.currency":       {LangEN: "Currency", LangPL: "Waluta"},
	"table.pln_equivalent": {LangEN: "PLN Equivalent", LangPL: "Równowartość w PLN"},
	"table.action":         {LangEN: "Action", LangPL: "Akcja"},
	"table.confidence":     {LangEN: "Confidence", LangPL: "Pewność"},
	"table.triggered_at":   {LangEN: "Triggered At", LangPL: "Wyzwolono o"},
	// How long the symbol has held its current action, not how long the alert row has existed —
	// see AlertsPage.ActiveFor.
	"table.active_for":         {LangEN: "Active For", LangPL: "Aktywny od"},
	"table.reason":             {LangEN: "Reason", LangPL: "Powód"},
	"table.score":              {LangEN: "Score", LangPL: "Wynik"},
	"table.technical":          {LangEN: "Technical", LangPL: "Techniczny"},
	"table.ml":                 {LangEN: "ML", LangPL: "ML"},
	"table.portfolio":          {LangEN: "Portfolio", LangPL: "Portfel"},
	"table.risk":               {LangEN: "Risk", LangPL: "Ryzyko"},
	"table.risk_na":            {LangEN: "n/a", LangPL: "brak danych"},
	"table.risk_na_hint":       {LangEN: "No drawdown or price deviation observed yet — nothing to score.", LangPL: "Nie zaobserwowano jeszcze straty ani odchylenia ceny — nie ma czego ocenić."},
	"table.market_condition":   {LangEN: "Market Condition", LangPL: "Stan rynku"},
	"table.model":              {LangEN: "Engine", LangPL: "Silnik"},
	"table.ml_model":           {LangEN: "ML model", LangPL: "Model ML"},
	"table.ml_model_none":      {LangEN: "none loaded — stub", LangPL: "brak — placeholder"},
	"table.total_return":       {LangEN: "Total Return", LangPL: "Całkowity zwrot"},
	"table.annual_return":      {LangEN: "Annual Return", LangPL: "Zwrot roczny"},
	"table.sharpe_ratio":       {LangEN: "Sharpe Ratio", LangPL: "Wskaźnik Sharpe'a"},
	"table.max_drawdown":       {LangEN: "Max Drawdown", LangPL: "Maks. obsunięcie kapitału"},
	"table.trades":             {LangEN: "Trades", LangPL: "Transakcje"},
	"table.win_rate":           {LangEN: "Win Rate", LangPL: "Skuteczność"},
	"table.sample_size":        {LangEN: "Sample Size", LangPL: "Liczebność próby"},
	"table.accuracy":           {LangEN: "Accuracy", LangPL: "Dokładność"},
	"table.precision":          {LangEN: "Precision", LangPL: "Precyzja"},
	"table.recall":             {LangEN: "Recall", LangPL: "Czułość"},
	"table.f1_score":           {LangEN: "F1 Score", LangPL: "Wynik F1"},
	"table.roc_auc":            {LangEN: "ROC AUC", LangPL: "ROC AUC"},
	"table.added":              {LangEN: "Added", LangPL: "Dodano"},
	"table.filter_placeholder": {LangEN: "Filter…", LangPL: "Filtruj…"},
	"table.username":           {LangEN: "Username", LangPL: "Nazwa użytkownika"},

	"common.remove":   {LangEN: "Remove", LangPL: "Usuń"},
	"common.choose":   {LangEN: "Choose…", LangPL: "Wybierz…"},
	"common.enabled":  {LangEN: "Enabled", LangPL: "Włączone"},
	"common.disabled": {LangEN: "Disabled", LangPL: "Wyłączone"},
	"common.disclaimer": {
		LangEN: "Decision-support only — Atlas never executes trades.",
		LangPL: "Wyłącznie wsparcie decyzyjne — Atlas nigdy nie realizuje transakcji.",
	},

	// Error page (error.html) — see web.RenderError.
	"error.title":     {LangEN: "Atlas — Error", LangPL: "Atlas — Błąd"},
	"error.not_found": {LangEN: "Page not found.", LangPL: "Nie znaleziono strony."},

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
	"dashboard.no_risk": {
		LangEN: "No risk data — no open positions.",
		LangPL: "Brak danych o ryzyku — brak otwartych pozycji.",
	},
	"dashboard.active_alerts":   {LangEN: "Active Alerts", LangPL: "Aktywne alerty"},
	"dashboard.view_all_alerts": {LangEN: "View all →", LangPL: "Zobacz wszystkie →"},

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
	"alerts.poll_status": {LangEN: "Background poll", LangPL: "Sondowanie w tle"},

	// Backtests (backtests.html)
	"backtests.title":         {LangEN: "Atlas — Backtests", LangPL: "Atlas — Backtesty"},
	"backtests.heading":       {LangEN: "Backtests", LangPL: "Backtesty"},
	"backtests.window":        {LangEN: "Window (bars)", LangPL: "Okno (świece)"},
	"backtests.capital":       {LangEN: "Initial capital", LangPL: "Kapitał początkowy"},
	"backtests.submit":        {LangEN: "Run backtest", LangPL: "Uruchom backtest"},
	"backtests.ml_evaluation": {LangEN: "ML Evaluation", LangPL: "Ocena modelu ML"},
	"backtests.no_trades":     {LangEN: "No trades over this window.", LangPL: "Brak transakcji w tym oknie."},

	// Watchlist (watchlist.html)
	"watchlist.title":        {LangEN: "Atlas — Watchlist", LangPL: "Atlas — Obserwowane"},
	"watchlist.heading":      {LangEN: "Watchlist", LangPL: "Obserwowane"},
	"watchlist.empty":        {LangEN: "Watchlist is empty.", LangPL: "Lista obserwowanych jest pusta."},
	"watchlist.add_symbol":   {LangEN: "Add a symbol", LangPL: "Dodaj symbol"},
	"watchlist.add_submit":   {LangEN: "Add", LangPL: "Dodaj"},
	"watchlist.manage":       {LangEN: "Manage watchlist", LangPL: "Zarządzanie listą"},
	"watchlist.manage_hint":  {LangEN: "Add one symbol, or move the whole list between environments.", LangPL: "Dodaj pojedynczy symbol albo przenieś całą listę między środowiskami."},
	"watchlist.refresh_info": {LangEN: "Refresh info", LangPL: "Odśwież info"},
	"watchlist.refresh_info_hint": {
		LangEN: "Fetches the display name and instrument type (stock, ETF, future, …) for every symbol that doesn't have one yet, from the market data provider.",
		LangPL: "Pobiera nazwę i typ instrumentu (akcja, ETF, kontrakt terminowy, …) dla każdego symbolu, który jeszcze go nie ma, od dostawcy danych rynkowych.",
	},
	"watchlist.export":        {LangEN: "Export", LangPL: "Eksport"},
	"watchlist.export_submit": {LangEN: "Download JSON", LangPL: "Pobierz JSON"},
	"watchlist.export_hint": {
		LangEN: "Downloads a JSON file you can import into another Atlas instance.",
		LangPL: "Pobiera plik JSON, który zaimportujesz do innej instancji Atlasa.",
	},
	"watchlist.import":      {LangEN: "Import", LangPL: "Import"},
	"watchlist.import_file": {LangEN: "Exported JSON file", LangPL: "Wyeksportowany plik JSON"},
	// Short labels because these sit in a third-width column, where the full sentence wrapped to
	// three lines. Each still names its consequence, and the destructive one repeats the full
	// warning in confirm.import_replace at the moment it actually matters.
	"watchlist.import_mode_merge":   {LangEN: "Merge (keep existing)", LangPL: "Scal (zachowaj obecne)"},
	"watchlist.import_mode_replace": {LangEN: "Replace (drop the rest)", LangPL: "Zastąp (usuń resztę)"},
	"watchlist.import_submit":       {LangEN: "Import", LangPL: "Importuj"},
	"watchlist.import_busy":         {LangEN: "Importing…", LangPL: "Importowanie…"},
	"watchlist.export_empty":        {LangEN: "Nothing to export yet.", LangPL: "Nie ma jeszcze czego eksportować."},
	"watchlist.import_no_file":      {LangEN: "Choose a file to import.", LangPL: "Wybierz plik do zaimportowania."},
	"watchlist.import_invalid":      {LangEN: "Invalid upload.", LangPL: "Nieprawidłowy plik."},
	"watchlist.import_done": {
		LangEN: "Imported: %d added, %d already tracked, %d removed.",
		LangPL: "Zaimportowano: %d dodanych, %d już obserwowanych, %d usuniętych.",
	},
	"confirm.import_replace": {
		LangEN: "Replace mode will untrack every symbol missing from this file. Continue?",
		LangPL: "Tryb zastąpienia usunie wszystkie symbole, których nie ma w tym pliku. Kontynuować?",
	},
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

	"account.ml_engine_heading": {LangEN: "ML in recommendations", LangPL: "ML w rekomendacjach"},
	"account.ml_engine_hint": {
		LangEN: "Master switch for whether BUY/SELL/HOLD scoring factors in ML/portfolio/risk at all, on top of the technical score. Takes effect immediately, no restart needed. Model files below stay loaded and usable via /predict either way.",
		LangPL: "Główny przełącznik decydujący, czy ocena BUY/SELL/HOLD w ogóle uwzględnia ML/portfel/ryzyko, poza wynikiem technicznym. Działa natychmiast, bez restartu. Pliki modeli poniżej zostają załadowane i dostępne przez /predict niezależnie od tego ustawienia.",
	},
	"account.ml_engine_enable_submit":  {LangEN: "Enable ML additive", LangPL: "Włącz dodatek ML"},
	"account.ml_engine_disable_submit": {LangEN: "Disable ML additive", LangPL: "Wyłącz dodatek ML"},

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

	"account.data_heading": {LangEN: "Database backup", LangPL: "Kopia zapasowa bazy danych"},
	"account.data_hint": {
		LangEN: "Downloads everything Atlas stores — positions, transactions, watchlist, price/indicator history, recommendations, settings — as one JSON file, restorable here or on a fresh environment.",
		LangPL: "Pobiera wszystko, co przechowuje Atlas — pozycje, transakcje, listę obserwowanych, historię cen/wskaźników, rekomendacje, ustawienia — jako jeden plik JSON, możliwy do przywrócenia tutaj lub na nowym środowisku.",
	},
	"account.backup_submit":  {LangEN: "Download backup", LangPL: "Pobierz kopię zapasową"},
	"account.restore_file":   {LangEN: "Backup JSON file", LangPL: "Plik JSON kopii zapasowej"},
	"account.restore_submit": {LangEN: "Restore from backup", LangPL: "Przywróć z kopii zapasowej"},
	"confirm.restore_backup": {
		LangEN: "Restoring will replace ALL current data with the contents of this file. Continue?",
		LangPL: "Przywrócenie zastąpi WSZYSTKIE obecne dane zawartością tego pliku. Kontynuować?",
	},

	"account.danger_heading": {LangEN: "Danger zone", LangPL: "Strefa zagrożenia"},
	"account.flush_hint": {
		LangEN: "Permanently wipes positions, transactions, watchlist, price/indicator history, recommendations, and ML/2FA settings — back to a freshly installed Atlas. Your login isn't affected. There is no undo short of restoring a backup taken beforehand.",
		LangPL: "Trwale usuwa pozycje, transakcje, listę obserwowanych, historię cen/wskaźników, rekomendacje oraz ustawienia ML/2FA — Atlas wraca do stanu świeżej instalacji. Logowanie pozostaje bez zmian. Nie ma cofnięcia poza przywróceniem wcześniej zrobionej kopii zapasowej.",
	},
	"account.flush_submit": {LangEN: "Flush all data", LangPL: "Wyczyść wszystkie dane"},
	"account.last_backup":  {LangEN: "Last backup:", LangPL: "Ostatnia kopia zapasowa:"},
	"account.flush_locked_hint": {
		LangEN: "Locked — no backup has been taken yet. Download one above first; the button unlocks as soon as one exists.",
		LangPL: "Zablokowane — nie wykonano jeszcze żadnej kopii zapasowej. Najpierw pobierz ją powyżej; przycisk odblokuje się, gdy tylko kopia będzie istnieć.",
	},
	"account.flush_stale_hint": {
		LangEN: "Locked — your last backup is more than 5 minutes old. Download a fresh one above; the button unlocks right after.",
		LangPL: "Zablokowane — ostatnia kopia zapasowa ma ponad 5 minut. Pobierz nową powyżej; przycisk odblokuje się od razu potem.",
	},
	"confirm.flush_all": {
		LangEN: "This permanently deletes ALL Atlas data (positions, transactions, watchlist, price history, recommendations, ML/2FA settings) and cannot be undone. Have you downloaded a backup? Continue?",
		LangPL: "To trwale usuwa WSZYSTKIE dane Atlasa (pozycje, transakcje, listę obserwowanych, historię cen, rekomendacje, ustawienia ML/2FA) i nie da się tego cofnąć. Czy pobrałeś(-aś) kopię zapasową? Kontynuować?",
	},

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

	// Decision-engine rationale strings — the closed, hardcoded set internal/application/
	// decision.UseCase actually returns in Recommendation.Reason (see BuildRecommendation and
	// waitOrHold). Keyed on the literal English sentence rather than a short code because these
	// are plain string literals in the engine, not an enum — reasonLabel falls back to the raw
	// English string for anything not listed here, so a future engine change adding a new reason
	// degrades to untranslated text instead of breaking rendering.
	"reason.Momentum signal is favorable":   {LangEN: "Momentum signal is favorable", LangPL: "Sygnał momentum jest korzystny"},
	"reason.Adding to an existing position": {LangEN: "Adding to an existing position", LangPL: "Powiększanie istniejącej pozycji"},
	"reason.Decision-support layer only":    {LangEN: "Decision-support layer only", LangPL: "Wyłącznie warstwa wsparcia decyzyjnego"},
	"reason.Momentum signal is weak":        {LangEN: "Momentum signal is weak", LangPL: "Sygnał momentum jest słaby"},
	"reason.Momentum signal is weak, but no position exists to act on": {
		LangEN: "Momentum signal is weak, but no position exists to act on",
		LangPL: "Sygnał momentum jest słaby, ale nie ma pozycji, na której można by działać",
	},
	"reason.Momentum signal is softening": {LangEN: "Momentum signal is softening", LangPL: "Sygnał momentum słabnie"},
	"reason.Trimming an existing position rather than exiting it": {
		LangEN: "Trimming an existing position rather than exiting it",
		LangPL: "Ograniczanie istniejącej pozycji zamiast jej zamykania",
	},
	"reason.Momentum signal is softening, but no position exists to act on": {
		LangEN: "Momentum signal is softening, but no position exists to act on",
		LangPL: "Sygnał momentum słabnie, ale nie ma pozycji, na której można by działać",
	},
	"reason.Momentum signal leans favorable but isn't strong enough yet": {
		LangEN: "Momentum signal leans favorable but isn't strong enough yet",
		LangPL: "Sygnał momentum skłania się ku korzystnemu, ale nie jest jeszcze wystarczająco silny",
	},
	"reason.Mixed signal composition": {LangEN: "Mixed signal composition", LangPL: "Mieszany układ sygnałów"},
	"reason.ML confidence too low to act on this signal yet": {
		LangEN: "ML confidence too low to act on this signal yet",
		LangPL: "Pewność modelu ML jest jeszcze za niska, aby działać na podstawie tego sygnału",
	},
	"reason.No automated trading action": {LangEN: "No automated trading action", LangPL: "Brak automatycznego działania transakcyjnego"},
	// These four are appended separately by internal/application/decision.Orchestrator.blend
	// (not UseCase.BuildRecommendation above) whenever ML/Portfolio/Risk scoring is wired in —
	// a second, independent source of Reason strings on the same Recommendation.Reason slice.
	"reason.ML signal supportive": {LangEN: "ML signal supportive", LangPL: "Sygnał ML jest wspierający"},
	"reason.ML signal cautious":   {LangEN: "ML signal cautious", LangPL: "Sygnał ML jest ostrożny"},
	"reason.Portfolio risk context reduces conviction": {
		LangEN: "Portfolio risk context reduces conviction",
		LangPL: "Kontekst ryzyka portfela obniża pewność",
	},
	"reason.Elevated portfolio-wide risk (drawdown/volatility)": {
		LangEN: "Elevated portfolio-wide risk (drawdown/volatility)",
		LangPL: "Podwyższone ryzyko całego portfela (obsunięcie/zmienność)",
	},
	// Orchestrator's own degraded-input fallbacks (no bars/technical signal, or Decision is nil).
	"reason.No signal data available":    {LangEN: "No signal data available", LangPL: "Brak danych sygnałowych"},
	"reason.Decision engine unavailable": {LangEN: "Decision engine unavailable", LangPL: "Silnik decyzyjny niedostępny"},
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

// reasonLabel translates a raw decision-engine rationale string (see the "reason.*" keys above).
// Unlike actionLabel, an unknown value passes through as-is in whatever language it was written
// (English) rather than falling back to a key lookup miss — these are full sentences, not a
// short fixed vocabulary, so "untranslated but readable" is the right degrade.
func reasonLabel(lang Lang, reason string) string {
	if strs, ok := translations["reason."+reason]; ok {
		if s, ok := strs[lang]; ok {
			return s
		}
	}
	return reason
}
