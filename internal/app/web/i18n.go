package web

// Lang is a supported dashboard display language. Atlas is a single-user personal tool (see
// PROJECT_CONTEXT.md), so this is a simple fixed-list switch, not a full locale system — no
// pluralization rules, number/date formatting, or RTL support.
type Lang string

const (
	LangEN Lang = "en"
	LangPL Lang = "pl"
	LangDE Lang = "de"
	LangFR Lang = "fr"
	LangES Lang = "es"
)

// DefaultLang is used when no language cookie is set, or it holds an unsupported value.
const DefaultLang = LangEN

// ValidLang reports whether lang is one Atlas actually has translations for — used to reject
// an unrecognized or tampered cookie value rather than silently rendering blank/fallback text
// everywhere.
func ValidLang(lang Lang) bool {
	return lang == LangEN || lang == LangPL || lang == LangDE || lang == LangFR || lang == LangES
}

// translations maps a translation key to its text in each supported language. English is the
// source of truth: every key must have an "en" entry (enforced by TestEveryTranslationKeyHasAllLanguages).
// A handful of values contain literal HTML (an inline <a> or <code>) — those are rendered via
// the safeHTML template func, not text-escaped, so keep them limited to static markup with no
// user-supplied content.
var translations = map[string]map[Lang]string{
	// Navigation (partials.html)
	"nav.dashboard":       {LangEN: "Dashboard", LangPL: "Panel główny", LangDE: "Übersicht", LangFR: "Tableau de bord", LangES: "Panel de control"},
	"nav.portfolio":       {LangEN: "Portfolio", LangPL: "Portfel", LangDE: "Portfolio", LangFR: "Portefeuille", LangES: "Cartera"},
	"nav.recommendations": {LangEN: "Recommendations", LangPL: "Rekomendacje", LangDE: "Empfehlungen", LangFR: "Recommandations", LangES: "Recomendaciones"},
	"nav.alerts":          {LangEN: "Alerts", LangPL: "Alerty", LangDE: "Warnungen", LangFR: "Alertes", LangES: "Alertas"},
	"nav.backtests":       {LangEN: "Backtests", LangPL: "Backtesty", LangDE: "Backtests", LangFR: "Backtests", LangES: "Backtests"},
	"nav.watchlist":       {LangEN: "Watchlist", LangPL: "Obserwowane", LangDE: "Beobachtungsliste", LangFR: "Liste de surveillance", LangES: "Lista de seguimiento"},
	"nav.account":         {LangEN: "Account", LangPL: "Konto", LangDE: "Konto", LangFR: "Compte", LangES: "Cuenta"},
	"nav.theme":           {LangEN: "Theme", LangPL: "Motyw", LangDE: "Design", LangFR: "Thème", LangES: "Tema"},
	"nav.theme_light":     {LangEN: "Light", LangPL: "Jasny", LangDE: "Hell", LangFR: "Clair", LangES: "Claro"},
	"nav.theme_dark":      {LangEN: "Dark", LangPL: "Ciemny", LangDE: "Dunkel", LangFR: "Sombre", LangES: "Oscuro"},
	"nav.theme_auto":      {LangEN: "Auto (system)", LangPL: "Automatyczny (systemowy)", LangDE: "Automatisch (System)", LangFR: "Automatique (système)", LangES: "Automático (sistema)"},
	"nav.language":        {LangEN: "Language", LangPL: "Język", LangDE: "Sprache", LangFR: "Langue", LangES: "Idioma"},
	"nav.language_en":     {LangEN: "English", LangPL: "Angielski", LangDE: "Englisch", LangFR: "Anglais", LangES: "Inglés"},
	"nav.language_pl":     {LangEN: "Polish", LangPL: "Polski", LangDE: "Polnisch", LangFR: "Polonais", LangES: "Polaco"},
	"nav.language_de":     {LangEN: "German", LangPL: "Niemiecki", LangDE: "Deutsch", LangFR: "Allemand", LangES: "Alemán"},
	"nav.language_fr":     {LangEN: "French", LangPL: "Francuski", LangDE: "Französisch", LangFR: "Français", LangES: "Francés"},
	"nav.language_es":     {LangEN: "Spanish", LangPL: "Hiszpański", LangDE: "Spanisch", LangFR: "Espagnol", LangES: "Español"},
	"nav.skip_to_content": {LangEN: "Skip to content", LangPL: "Przejdź do treści", LangDE: "Zum Inhalt springen", LangFR: "Passer au contenu", LangES: "Saltar al contenido"},

	// Shared table headers/labels, reused across multiple pages
	"table.symbol": {LangEN: "Symbol", LangPL: "Symbol", LangDE: "Symbol", LangFR: "Symbole", LangES: "Símbolo"},
	"table.name":   {LangEN: "Name", LangPL: "Nazwa", LangDE: "Name", LangFR: "Nom", LangES: "Nombre"},
	// Today's live price vs. the prior session's close — see web.SymbolInfo.ChangePercent.
	// Blank until the first alert-poll cycle after a symbol is added has run.
	"table.today_change": {LangEN: "Today", LangPL: "Dzisiaj", LangDE: "Heute", LangFR: "Aujourd'hui", LangES: "Hoy"},
	// Announced on every symbol link. It names the destination and the new tab, because a link
	// that opens elsewhere without saying so is disorienting for anyone not watching the viewport.
	"table.open_in_yahoo": {LangEN: "Open on Yahoo Finance in a new tab", LangPL: "Otwórz w Yahoo Finance w nowej karcie", LangDE: "Auf Yahoo Finance in neuem Tab öffnen", LangFR: "Ouvrir sur Yahoo Finance dans un nouvel onglet", LangES: "Abrir en Yahoo Finance en una pestaña nueva"},
	"table.quantity":      {LangEN: "Quantity", LangPL: "Ilość", LangDE: "Menge", LangFR: "Quantité", LangES: "Cantidad"},
	"table.avg_cost":      {LangEN: "Avg. Cost", LangPL: "Śr. koszt", LangDE: "Ø Kosten", LangFR: "Coût moyen", LangES: "Costo promedio"},
	// Position.CurrentPrice (see portfolio.applyTransaction) is normally overlaid with a live
	// quote before this renders (see web_handlers.go's livePricedPositions) — "Current Price",
	// not "Last Transaction Price", is what's actually true most of the time now. table.as_of
	// is the small "as of <date>" line shown under it: the live quote's own timestamp when one
	// was found, falling back to the last transaction's timestamp (the honest label) when not.
	"table.current_price": {LangEN: "Current Price", LangPL: "Aktualna cena", LangDE: "Aktueller Kurs", LangFR: "Prix actuel", LangES: "Precio actual"},
	"table.as_of":         {LangEN: "as of", LangPL: "stan na", LangDE: "Stand", LangFR: "au", LangES: "a fecha de"},
	// Shown instead of table.as_of when the price is not a current market quote (see the
	// priceState template func), so a cost basis or a days-old quote is never read as today's value.
	// A recommendation scored with an expected input missing (decision.Recommendation.Degraded).
	// Which correction event backtesting's ML evaluation scored the model against (MLEvaluation.Label).
	"table.ml_label":              {LangEN: "Evaluated against", LangPL: "Oceniany względem", LangDE: "Bewertet gegen", LangFR: "Évalué par rapport à", LangES: "Evaluado frente a"},
	"table.ml_label_default":      {LangEN: "default label", LangPL: "domyślna etykieta", LangDE: "Standard-Label", LangFR: "étiquette par défaut", LangES: "etiqueta por defecto"},
	"table.ml_label_default_hint": {LangEN: "This model's metadata does not record the label it was trained on, so it was scored against the default definition, which may not be the event it learned.", LangPL: "Metadane tego modelu nie zapisują etykiety, na której go wytrenowano, więc oceniono go według domyślnej definicji, która może nie odpowiadać zdarzeniu, którego się uczył.", LangDE: "Die Metadaten dieses Modells enthalten nicht das Label, mit dem es trainiert wurde; es wurde daher gegen die Standarddefinition bewertet, die nicht dem gelernten Ereignis entsprechen muss.", LangFR: "Les métadonnées de ce modèle n'indiquent pas l'étiquette sur laquelle il a été entraîné ; il a donc été évalué selon la définition par défaut, qui peut ne pas correspondre à l'événement appris.", LangES: "Los metadatos de este modelo no registran la etiqueta con la que se entrenó, así que se evaluó con la definición por defecto, que puede no ser el evento que aprendió."},
	"rec.degraded":                {LangEN: "incomplete inputs", LangPL: "niepełne dane wejściowe", LangDE: "unvollständige Eingaben", LangFR: "données incomplètes", LangES: "datos incompletos"},
	"rec.degraded_hint":           {LangEN: "An input the engine expected (ML prediction, portfolio state or a current price) was unavailable, so this recommendation rests on fewer signals than usual. The reasons say which.", LangPL: "Zabrakło danych, których silnik oczekiwał (predykcji ML, stanu portfela lub aktualnej ceny), więc ta rekomendacja opiera się na mniejszej liczbie sygnałów niż zwykle. Szczegóły podają uzasadnienia.", LangDE: "Eine erwartete Eingabe (ML-Prognose, Portfoliostand oder aktueller Kurs) war nicht verfügbar; diese Empfehlung stützt sich daher auf weniger Signale als üblich. Die Begründungen nennen, welche.", LangFR: "Une donnée attendue par le moteur (prédiction ML, état du portefeuille ou prix actuel) était indisponible ; cette recommandation repose donc sur moins de signaux que d'habitude. Les justifications précisent laquelle.", LangES: "Faltó un dato que el motor esperaba (predicción ML, estado de la cartera o un precio actual), así que esta recomendación se basa en menos señales de lo habitual. Los motivos indican cuál."},
	"table.ml_recovery_model":     {LangEN: "ML recovery model", LangPL: "Model ML odbicia", LangDE: "ML-Erholungsmodell", LangFR: "Modèle ML de rebond", LangES: "Modelo ML de recuperación"},
	"price.trade":                 {LangEN: "last trade price, not a market quote — as of", LangPL: "cena ostatniej transakcji, nie notowanie — stan na", LangDE: "Preis der letzten Transaktion, kein Marktkurs — Stand", LangFR: "prix de la dernière transaction, pas une cotation — au", LangES: "precio de la última operación, no una cotización — a fecha de"},
	"price.stale":                 {LangEN: "stale quote — last traded", LangPL: "nieaktualne notowanie — ostatni obrót", LangDE: "veralteter Kurs — zuletzt gehandelt", LangFR: "cotation périmée — dernier échange le", LangES: "cotización desactualizada — última negociación"},
	"table.pnl":                   {LangEN: "P&L", LangPL: "Zysk/strata", LangDE: "G/V", LangFR: "P&L", LangES: "G/P"},
	"table.total":                 {LangEN: "Total", LangPL: "Suma", LangDE: "Gesamt", LangFR: "Total", LangES: "Total"},
	"table.exposure":              {LangEN: "Exposure", LangPL: "Ekspozycja", LangDE: "Exponierung", LangFR: "Exposition", LangES: "Exposición"},
	// table.drawdown and table.avg_deviation are the dashboard/portfolio Risk table's own
	// simple heuristics (portfolio.RiskSnapshot — see its doc comment), deliberately named for
	// what they actually compute rather than reusing "Drawdown"/"Volatility", which
	// table.max_drawdown/table.volatility below already mean something different and real
	// (backtesting.Result — peak-to-trough drawdown, annualized return stddev).
	"table.drawdown":      {LangEN: "Unrealized Loss / Exposure", LangPL: "Strata niezrealizowana / ekspozycja", LangDE: "Nicht realisierter Verlust / Exponierung", LangFR: "Perte latente / Exposition", LangES: "Pérdida no realizada / Exposición"},
	"table.avg_deviation": {LangEN: "Avg. Deviation from Cost", LangPL: "Śr. odchylenie od kosztu", LangDE: "Ø Abweichung vom Einstandspreis", LangFR: "Écart moyen par rapport au coût", LangES: "Desviación media respecto al costo"},
	"table.volatility":    {LangEN: "Volatility", LangPL: "Zmienność", LangDE: "Volatilität", LangFR: "Volatilité", LangES: "Volatilidad"},
	"table.type":          {LangEN: "Type", LangPL: "Typ", LangDE: "Typ", LangFR: "Type", LangES: "Tipo"},
	// Named "Unit price", not "Price": Transaction.Price is per-unit (see its doc comment) and
	// gets multiplied by Quantity everywhere it's used, so a bare "Price" reads ambiguously as
	// the total cost of the trade.
	"table.price":          {LangEN: "Unit Price", LangPL: "Cena jednostkowa", LangDE: "Stückpreis", LangFR: "Prix unitaire", LangES: "Precio unitario"},
	"table.timestamp":      {LangEN: "Timestamp", LangPL: "Znacznik czasu", LangDE: "Zeitstempel", LangFR: "Horodatage", LangES: "Marca de tiempo"},
	"table.currency":       {LangEN: "Currency", LangPL: "Waluta", LangDE: "Währung", LangFR: "Devise", LangES: "Moneda"},
	"table.pln_equivalent": {LangEN: "PLN Equivalent", LangPL: "Równowartość w PLN", LangDE: "PLN-Gegenwert", LangFR: "Équivalent en PLN", LangES: "Equivalente en PLN"},
	"table.action":         {LangEN: "Action", LangPL: "Akcja", LangDE: "Aktion", LangFR: "Action", LangES: "Acción"},
	"table.confidence":     {LangEN: "Confidence", LangPL: "Pewność", LangDE: "Konfidenz", LangFR: "Confiance", LangES: "Confianza"},
	"table.triggered_at":   {LangEN: "Triggered At", LangPL: "Wyzwolono o", LangDE: "Ausgelöst am", LangFR: "Déclenché à", LangES: "Activado el"},
	// How long the symbol has held its current action, not how long the alert row has existed —
	// see AlertsPage.ActiveFor.
	"table.active_for":         {LangEN: "Active For", LangPL: "Aktywny od", LangDE: "Aktiv seit", LangFR: "Actif depuis", LangES: "Activo desde"},
	"table.reason":             {LangEN: "Reason", LangPL: "Powód", LangDE: "Grund", LangFR: "Raison", LangES: "Motivo"},
	"table.score":              {LangEN: "Score", LangPL: "Wynik", LangDE: "Score", LangFR: "Score", LangES: "Score"},
	"table.technical":          {LangEN: "Technical", LangPL: "Techniczny", LangDE: "Technisch", LangFR: "Technique", LangES: "Técnico"},
	"table.ml":                 {LangEN: "ML", LangPL: "ML", LangDE: "ML", LangFR: "ML", LangES: "ML"},
	"table.portfolio":          {LangEN: "Portfolio", LangPL: "Portfel", LangDE: "Portfolio", LangFR: "Portefeuille", LangES: "Cartera"},
	"table.risk":               {LangEN: "Risk", LangPL: "Ryzyko", LangDE: "Risiko", LangFR: "Risque", LangES: "Riesgo"},
	"table.risk_na":            {LangEN: "n/a", LangPL: "brak danych", LangDE: "k. A.", LangFR: "n/d", LangES: "n/d"},
	"table.risk_na_hint":       {LangEN: "No drawdown or price deviation observed yet — nothing to score.", LangPL: "Nie zaobserwowano jeszcze straty ani odchylenia ceny — nie ma czego ocenić.", LangDE: "Noch kein Kapitalrückgang oder keine Preisabweichung beobachtet — nichts zu bewerten.", LangFR: "Aucune perte ou déviation de prix observée pour l'instant — rien à évaluer.", LangES: "Aún no se ha observado ninguna pérdida ni desviación de precio — nada que puntuar."},
	"table.market_condition":   {LangEN: "Market Condition", LangPL: "Stan rynku", LangDE: "Marktlage", LangFR: "Conditions de marché", LangES: "Condición del mercado"},
	"table.model":              {LangEN: "Engine", LangPL: "Silnik", LangDE: "Engine", LangFR: "Moteur", LangES: "Motor"},
	"table.ml_model":           {LangEN: "ML model", LangPL: "Model ML", LangDE: "ML-Modell", LangFR: "Modèle ML", LangES: "Modelo ML"},
	"table.ml_model_none":      {LangEN: "none loaded — stub", LangPL: "brak — placeholder", LangDE: "keines geladen — Platzhalter", LangFR: "aucun chargé — substitut", LangES: "ninguno cargado — sustituto"},
	"table.total_return":       {LangEN: "Total Return", LangPL: "Całkowity zwrot", LangDE: "Gesamtrendite", LangFR: "Rendement total", LangES: "Rendimiento total"},
	"table.annual_return":      {LangEN: "Annual Return", LangPL: "Zwrot roczny", LangDE: "Jährliche Rendite", LangFR: "Rendement annuel", LangES: "Rendimiento anual"},
	"table.sharpe_ratio":       {LangEN: "Sharpe Ratio", LangPL: "Wskaźnik Sharpe'a", LangDE: "Sharpe-Ratio", LangFR: "Ratio de Sharpe", LangES: "Ratio de Sharpe"},
	"table.max_drawdown":       {LangEN: "Max Drawdown", LangPL: "Maks. obsunięcie kapitału", LangDE: "Maximaler Drawdown", LangFR: "Drawdown maximal", LangES: "Drawdown máximo"},
	"table.trades":             {LangEN: "Trades", LangPL: "Transakcje", LangDE: "Trades", LangFR: "Transactions", LangES: "Operaciones"},
	"table.win_rate":           {LangEN: "Win Rate", LangPL: "Skuteczność", LangDE: "Trefferquote", LangFR: "Taux de réussite", LangES: "Tasa de acierto"},
	"table.sample_size":        {LangEN: "Sample Size", LangPL: "Liczebność próby", LangDE: "Stichprobengröße", LangFR: "Taille de l'échantillon", LangES: "Tamaño de la muestra"},
	"table.accuracy":           {LangEN: "Accuracy", LangPL: "Dokładność", LangDE: "Genauigkeit", LangFR: "Exactitude", LangES: "Exactitud"},
	"table.precision":          {LangEN: "Precision", LangPL: "Precyzja", LangDE: "Präzision", LangFR: "Précision", LangES: "Precisión"},
	"table.recall":             {LangEN: "Recall", LangPL: "Czułość", LangDE: "Recall", LangFR: "Rappel", LangES: "Exhaustividad"},
	"table.f1_score":           {LangEN: "F1 Score", LangPL: "Wynik F1", LangDE: "F1-Score", LangFR: "Score F1", LangES: "Puntuación F1"},
	"table.roc_auc":            {LangEN: "ROC AUC", LangPL: "ROC AUC", LangDE: "ROC AUC", LangFR: "ROC AUC", LangES: "ROC AUC"},
	"table.added":              {LangEN: "Added", LangPL: "Dodano", LangDE: "Hinzugefügt", LangFR: "Ajouté", LangES: "Añadido"},
	"table.filter_placeholder": {LangEN: "Filter…", LangPL: "Filtruj…", LangDE: "Filtern…", LangFR: "Filtrer…", LangES: "Filtrar…"},

	"common.remove":   {LangEN: "Remove", LangPL: "Usuń", LangDE: "Entfernen", LangFR: "Supprimer", LangES: "Eliminar"},
	"common.choose":   {LangEN: "Choose…", LangPL: "Wybierz…", LangDE: "Auswählen…", LangFR: "Choisir…", LangES: "Elegir…"},
	"common.enabled":  {LangEN: "Enabled", LangPL: "Włączone", LangDE: "Aktiviert", LangFR: "Activé", LangES: "Activado"},
	"common.disabled": {LangEN: "Disabled", LangPL: "Wyłączone", LangDE: "Deaktiviert", LangFR: "Désactivé", LangES: "Desactivado"},
	"common.disclaimer": {
		LangEN: "Decision-support only — Atlas never executes trades.",
		LangPL: "Wyłącznie wsparcie decyzyjne — Atlas nigdy nie realizuje transakcji.",
		LangDE: "Nur Entscheidungsunterstützung — Atlas führt niemals Trades aus.",
		LangFR: "Aide à la décision uniquement — Atlas n'exécute jamais de transactions.",
		LangES: "Solo apoyo a la decisión — Atlas nunca ejecuta operaciones.",
	},

	// Error page (error.html) — see web.RenderError.
	"error.title":     {LangEN: "Atlas — Error", LangPL: "Atlas — Błąd", LangDE: "Atlas — Fehler", LangFR: "Atlas — Erreur", LangES: "Atlas — Error"},
	"error.not_found": {LangEN: "Page not found.", LangPL: "Nie znaleziono strony.", LangDE: "Seite nicht gefunden.", LangFR: "Page introuvable.", LangES: "Página no encontrada."},

	// Dashboard (dashboard.html)
	"dashboard.title":     {LangEN: "Atlas — Dashboard", LangPL: "Atlas — Panel główny", LangDE: "Atlas — Übersicht", LangFR: "Atlas — Tableau de bord", LangES: "Atlas — Panel de control"},
	"dashboard.heading":   {LangEN: "Dashboard", LangPL: "Panel główny", LangDE: "Übersicht", LangFR: "Tableau de bord", LangES: "Panel de control"},
	"dashboard.positions": {LangEN: "Positions", LangPL: "Pozycje", LangDE: "Positionen", LangFR: "Positions", LangES: "Posiciones"},
	"dashboard.no_positions": {
		LangEN: `No open positions yet — see <a href="/dashboard/portfolio">Portfolio</a> to record a transaction.`,
		LangPL: `Brak otwartych pozycji — przejdź do <a href="/dashboard/portfolio">Portfela</a>, aby zapisać transakcję.`,
		LangDE: `Noch keine offenen Positionen — unter <a href="/dashboard/portfolio">Portfolio</a> kannst du eine Transaktion erfassen.`,
		LangFR: `Aucune position ouverte pour l'instant — consultez <a href="/dashboard/portfolio">Portefeuille</a> pour enregistrer une transaction.`,
		LangES: `Aún no hay posiciones abiertas — consulta <a href="/dashboard/portfolio">Cartera</a> para registrar una transacción.`,
	},
	"dashboard.portfolio_risk": {LangEN: "Portfolio Risk", LangPL: "Ryzyko portfela", LangDE: "Portfoliorisiko", LangFR: "Risque du portefeuille", LangES: "Riesgo de la cartera"},
	"dashboard.no_risk": {
		LangEN: "No risk data — no open positions.",
		LangPL: "Brak danych o ryzyku — brak otwartych pozycji.",
		LangDE: "Keine Risikodaten — keine offenen Positionen.",
		LangFR: "Aucune donnée de risque — aucune position ouverte.",
		LangES: "Sin datos de riesgo — no hay posiciones abiertas.",
	},
	"dashboard.active_alerts":   {LangEN: "Active Alerts", LangPL: "Aktywne alerty", LangDE: "Aktive Warnungen", LangFR: "Alertes actives", LangES: "Alertas activas"},
	"dashboard.view_all_alerts": {LangEN: "View all →", LangPL: "Zobacz wszystkie →", LangDE: "Alle anzeigen →", LangFR: "Voir tout →", LangES: "Ver todas →"},

	// Portfolio (portfolio.html)
	"portfolio.title":               {LangEN: "Atlas — Portfolio", LangPL: "Atlas — Portfel", LangDE: "Atlas — Portfolio", LangFR: "Atlas — Portefeuille", LangES: "Atlas — Cartera"},
	"portfolio.heading":             {LangEN: "Portfolio", LangPL: "Portfel", LangDE: "Portfolio", LangFR: "Portefeuille", LangES: "Cartera"},
	"portfolio.no_positions":        {LangEN: "No open positions yet.", LangPL: "Brak otwartych pozycji.", LangDE: "Noch keine offenen Positionen.", LangFR: "Aucune position ouverte pour l'instant.", LangES: "Aún no hay posiciones abiertas."},
	"portfolio.transaction_history": {LangEN: "Transaction History", LangPL: "Historia transakcji", LangDE: "Transaktionsverlauf", LangFR: "Historique des transactions", LangES: "Historial de transacciones"},
	"portfolio.no_transactions":     {LangEN: "No transactions recorded yet.", LangPL: "Nie zapisano jeszcze żadnych transakcji.", LangDE: "Noch keine Transaktionen erfasst.", LangFR: "Aucune transaction enregistrée pour l'instant.", LangES: "Aún no se han registrado transacciones."},
	"portfolio.record_transaction":  {LangEN: "Record a transaction", LangPL: "Zapisz transakcję", LangDE: "Transaktion erfassen", LangFR: "Enregistrer une transaction", LangES: "Registrar una transacción"},
	"portfolio.record_transaction_hint": {
		LangEN: `The only real portfolio-write path — matches <code>POST /portfolio</code>.`,
		LangPL: `Jedyna rzeczywista ścieżka zapisu do portfela — odpowiada <code>POST /portfolio</code>.`,
		LangDE: `Der einzige tatsächliche Schreibpfad für das Portfolio — entspricht <code>POST /portfolio</code>.`,
		LangFR: `Le seul véritable chemin d'écriture du portefeuille — correspond à <code>POST /portfolio</code>.`,
		LangES: `La única vía real de escritura en la cartera — corresponde a <code>POST /portfolio</code>.`,
	},
	"portfolio.record_submit":    {LangEN: "Record transaction", LangPL: "Zapisz transakcję", LangDE: "Transaktion speichern", LangFR: "Enregistrer la transaction", LangES: "Registrar transacción"},
	"portfolio.record_portfolio": {LangEN: "Portfolio", LangPL: "Portfel", LangDE: "Portfolio", LangFR: "Portefeuille", LangES: "Cartera"},
	"portfolio.record_busy":      {LangEN: "Recording…", LangPL: "Zapisywanie…", LangDE: "Wird gespeichert…", LangFR: "Enregistrement…", LangES: "Registrando…"},
	"portfolio.trade_date":       {LangEN: "Trade date", LangPL: "Data transakcji", LangDE: "Handelsdatum", LangFR: "Date de l'opération", LangES: "Fecha de la operación"},
	"portfolio.trade_date_hint":  {LangEN: "A EUR or USD trade is converted to PLN at the NBP rate for this date.", LangPL: "Transakcja w EUR lub USD jest przeliczana na PLN po kursie NBP z tego dnia.", LangDE: "Ein Geschäft in EUR oder USD wird zum NBP-Kurs dieses Tages in PLN umgerechnet.", LangFR: "Une opération en EUR ou USD est convertie en PLN au taux NBP de cette date.", LangES: "Una operación en EUR o USD se convierte a PLN al tipo del NBP de esa fecha."},
	"confirm.remove_position": {
		LangEN: "Remove %s entirely? Transaction history is kept, only the current position is cleared.",
		LangPL: "Usunąć %s całkowicie? Historia transakcji zostaje zachowana, czyszczona jest tylko bieżąca pozycja.",
		LangDE: "%s vollständig entfernen? Der Transaktionsverlauf bleibt erhalten, nur die aktuelle Position wird gelöscht.",
		LangFR: "Supprimer %s entièrement ? L'historique des transactions est conservé, seule la position actuelle est effacée.",
		LangES: "¿Eliminar %s por completo? El historial de transacciones se conserva, solo se borra la posición actual.",
	},
	"confirm.remove_transaction": {
		LangEN: "Remove this transaction and recompute %s's position from what remains?",
		LangPL: "Usunąć tę transakcję i przeliczyć pozycję %s na podstawie pozostałych?",
		LangDE: "Diese Transaktion entfernen und die Position von %s aus den verbleibenden neu berechnen?",
		LangFR: "Supprimer cette transaction et recalculer la position de %s à partir de ce qui reste ?",
		LangES: "¿Eliminar esta transacción y recalcular la posición de %s con lo que queda?",
	},
	"confirm.remove_transactions_bulk": {
		LangEN: "Remove %d selected transaction(s) and recompute the affected positions from what remains?",
		LangPL: "Usunąć %d wybranych transakcji i przeliczyć dotknięte pozycje na podstawie pozostałych?",
		LangDE: "%d ausgewählte Transaktion(en) entfernen und die betroffenen Positionen aus den verbleibenden neu berechnen?",
		LangFR: "Supprimer %d transaction(s) sélectionnée(s) et recalculer les positions concernées à partir de ce qui reste ?",
		LangES: "¿Eliminar %d transacción(es) seleccionada(s) y recalcular las posiciones afectadas con lo que queda?",
	},
	"confirm.remove_positions_bulk": {
		LangEN: "Remove %d selected position(s) entirely? Transaction history is kept, only the current positions are cleared.",
		LangPL: "Usunąć %d wybranych pozycji całkowicie? Historia transakcji zostaje zachowana, czyszczone są tylko bieżące pozycje.",
		LangDE: "%d ausgewählte Position(en) vollständig entfernen? Der Transaktionsverlauf bleibt erhalten, nur die aktuellen Positionen werden gelöscht.",
		LangFR: "Supprimer entièrement %d position(s) sélectionnée(s) ? L'historique des transactions est conservé, seules les positions actuelles sont effacées.",
		LangES: "¿Eliminar %d posición(es) seleccionada(s) por completo? El historial de transacciones se conserva, solo se borran las posiciones actuales.",
	},
	"portfolio.delete_selected": {LangEN: "Delete selected", LangPL: "Usuń zaznaczone", LangDE: "Auswahl löschen", LangFR: "Supprimer la sélection", LangES: "Eliminar selección"},
	"table.select_all":          {LangEN: "Select all", LangPL: "Zaznacz wszystkie", LangDE: "Alle auswählen", LangFR: "Tout sélectionner", LangES: "Seleccionar todo"},

	// Recommendations (recommendations.html)
	"recommendations.title":     {LangEN: "Atlas — Recommendations", LangPL: "Atlas — Rekomendacje", LangDE: "Atlas — Empfehlungen", LangFR: "Atlas — Recommandations", LangES: "Atlas — Recomendaciones"},
	"recommendations.heading":   {LangEN: "Recommendations", LangPL: "Rekomendacje", LangDE: "Empfehlungen", LangFR: "Recommandations", LangES: "Recomendaciones"},
	"recommendations.submit":    {LangEN: "Get recommendation", LangPL: "Pobierz rekomendację", LangDE: "Empfehlung abrufen", LangFR: "Obtenir une recommandation", LangES: "Obtener recomendación"},
	"recommendations.reasoning": {LangEN: "Reasoning", LangPL: "Uzasadnienie", LangDE: "Begründung", LangFR: "Justification", LangES: "Justificación"},
	"watchlist.dropdown_hint": {
		LangEN: `Add symbols to your <a href="/dashboard/watchlist">watchlist</a> for a dropdown here.`,
		LangPL: `Dodaj symbole do <a href="/dashboard/watchlist">listy obserwowanych</a>, aby pojawiła się tu lista rozwijana.`,
		LangDE: `Füge Symbole zu deiner <a href="/dashboard/watchlist">Beobachtungsliste</a> hinzu, damit hier eine Auswahlliste erscheint.`,
		LangFR: `Ajoutez des symboles à votre <a href="/dashboard/watchlist">liste de surveillance</a> pour obtenir une liste déroulante ici.`,
		LangES: `Añade símbolos a tu <a href="/dashboard/watchlist">lista de seguimiento</a> para tener aquí un menú desplegable.`,
	},

	// Alerts (alerts.html)
	"alerts.title":   {LangEN: "Atlas — Alerts", LangPL: "Atlas — Alerty", LangDE: "Atlas — Warnungen", LangFR: "Atlas — Alertes", LangES: "Atlas — Alertas"},
	"alerts.heading": {LangEN: "Alerts", LangPL: "Alerty", LangDE: "Warnungen", LangFR: "Alertes", LangES: "Alertas"},
	"alerts.subtitle": {
		LangEN: "Every watchlist symbol whose current recommendation isn't HOLD.",
		LangPL: "Każdy symbol z listy obserwowanych, którego aktualna rekomendacja to nie HOLD.",
		LangDE: "Jedes Symbol der Beobachtungsliste, dessen aktuelle Empfehlung nicht HOLD lautet.",
		LangFR: "Chaque symbole de la liste de surveillance dont la recommandation actuelle n'est pas HOLD.",
		LangES: "Todos los símbolos de la lista de seguimiento cuya recomendación actual no es HOLD.",
	},
	"alerts.empty": {
		LangEN: "No active alerts — every watched symbol is currently HOLD, or the watchlist is empty.",
		LangPL: "Brak aktywnych alertów — każdy obserwowany symbol ma obecnie rekomendację HOLD albo lista obserwowanych jest pusta.",
		LangDE: "Keine aktiven Warnungen — jedes beobachtete Symbol steht aktuell auf HOLD, oder die Beobachtungsliste ist leer.",
		LangFR: "Aucune alerte active — tous les symboles surveillés sont actuellement en HOLD, ou la liste de surveillance est vide.",
		LangES: "Sin alertas activas — todos los símbolos observados están actualmente en HOLD, o la lista de seguimiento está vacía.",
	},
	"alerts.poll_status": {LangEN: "Background poll", LangPL: "Sondowanie w tle", LangDE: "Hintergrundabfrage", LangFR: "Interrogation en arrière-plan", LangES: "Sondeo en segundo plano"},

	// Backtests (backtests.html)
	"backtests.title":         {LangEN: "Atlas — Backtests", LangPL: "Atlas — Backtesty", LangDE: "Atlas — Backtests", LangFR: "Atlas — Backtests", LangES: "Atlas — Backtests"},
	"backtests.heading":       {LangEN: "Backtests", LangPL: "Backtesty", LangDE: "Backtests", LangFR: "Backtests", LangES: "Backtests"},
	"backtests.window":        {LangEN: "Window (bars)", LangPL: "Okno (świece)", LangDE: "Fenster (Kerzen)", LangFR: "Fenêtre (bougies)", LangES: "Ventana (velas)"},
	"backtests.capital":       {LangEN: "Initial capital", LangPL: "Kapitał początkowy", LangDE: "Startkapital", LangFR: "Capital initial", LangES: "Capital inicial"},
	"backtests.submit":        {LangEN: "Run backtest", LangPL: "Uruchom backtest", LangDE: "Backtest starten", LangFR: "Lancer le backtest", LangES: "Ejecutar backtest"},
	"backtests.ml_evaluation": {LangEN: "ML Evaluation", LangPL: "Ocena modelu ML", LangDE: "ML-Auswertung", LangFR: "Évaluation ML", LangES: "Evaluación ML"},
	"backtests.no_trades":     {LangEN: "No trades over this window.", LangPL: "Brak transakcji w tym oknie.", LangDE: "Keine Trades in diesem Zeitfenster.", LangFR: "Aucune transaction sur cette période.", LangES: "Sin operaciones en esta ventana."},

	// Watchlist (watchlist.html)
	"watchlist.title":        {LangEN: "Atlas — Watchlist", LangPL: "Atlas — Obserwowane", LangDE: "Atlas — Beobachtungsliste", LangFR: "Atlas — Liste de surveillance", LangES: "Atlas — Lista de seguimiento"},
	"watchlist.heading":      {LangEN: "Watchlist", LangPL: "Obserwowane", LangDE: "Beobachtungsliste", LangFR: "Liste de surveillance", LangES: "Lista de seguimiento"},
	"watchlist.empty":        {LangEN: "Watchlist is empty.", LangPL: "Lista obserwowanych jest pusta.", LangDE: "Die Beobachtungsliste ist leer.", LangFR: "La liste de surveillance est vide.", LangES: "La lista de seguimiento está vacía."},
	"watchlist.add_symbol":   {LangEN: "Add a symbol", LangPL: "Dodaj symbol", LangDE: "Symbol hinzufügen", LangFR: "Ajouter un symbole", LangES: "Añadir un símbolo"},
	"watchlist.add_submit":   {LangEN: "Add", LangPL: "Dodaj", LangDE: "Hinzufügen", LangFR: "Ajouter", LangES: "Añadir"},
	"watchlist.manage":       {LangEN: "Manage watchlist", LangPL: "Zarządzanie listą", LangDE: "Beobachtungsliste verwalten", LangFR: "Gérer la liste de surveillance", LangES: "Gestionar lista de seguimiento"},
	"watchlist.manage_hint":  {LangEN: "Add one symbol, or move the whole list between environments.", LangPL: "Dodaj pojedynczy symbol albo przenieś całą listę między środowiskami.", LangDE: "Füge ein einzelnes Symbol hinzu oder verschiebe die gesamte Liste zwischen Umgebungen.", LangFR: "Ajoutez un symbole ou transférez la liste entière entre environnements.", LangES: "Añade un símbolo o mueve toda la lista entre entornos."},
	"watchlist.refresh_info": {LangEN: "Refresh info", LangPL: "Odśwież info", LangDE: "Infos aktualisieren", LangFR: "Actualiser les infos", LangES: "Actualizar info"},
	"watchlist.refresh_info_hint": {
		LangEN: "Fetches the display name and instrument type (stock, ETF, future, …) for every symbol that doesn't have one yet, from the market data provider.",
		LangPL: "Pobiera nazwę i typ instrumentu (akcja, ETF, kontrakt terminowy, …) dla każdego symbolu, który jeszcze go nie ma, od dostawcy danych rynkowych.",
		LangDE: "Ruft für jedes Symbol, das noch keinen hat, den Anzeigenamen und den Instrumententyp (Aktie, ETF, Future, …) vom Marktdatenanbieter ab.",
		LangFR: "Récupère le nom d'affichage et le type d'instrument (action, ETF, future, …) auprès du fournisseur de données de marché pour chaque symbole qui n'en a pas encore.",
		LangES: "Obtiene el nombre visible y el tipo de instrumento (acción, ETF, futuro, …) del proveedor de datos de mercado para cada símbolo que aún no lo tenga.",
	},
	"watchlist.export":        {LangEN: "Export", LangPL: "Eksport", LangDE: "Export", LangFR: "Exporter", LangES: "Exportar"},
	"watchlist.export_submit": {LangEN: "Download JSON", LangPL: "Pobierz JSON", LangDE: "JSON herunterladen", LangFR: "Télécharger le JSON", LangES: "Descargar JSON"},
	"watchlist.export_hint": {
		LangEN: "Downloads a JSON file you can import into another Atlas instance.",
		LangPL: "Pobiera plik JSON, który zaimportujesz do innej instancji Atlasa.",
		LangDE: "Lädt eine JSON-Datei herunter, die du in eine andere Atlas-Instanz importieren kannst.",
		LangFR: "Télécharge un fichier JSON que vous pouvez importer dans une autre instance d'Atlas.",
		LangES: "Descarga un archivo JSON que puedes importar en otra instancia de Atlas.",
	},
	"watchlist.import":      {LangEN: "Import", LangPL: "Import", LangDE: "Import", LangFR: "Importer", LangES: "Importar"},
	"watchlist.import_file": {LangEN: "Exported JSON file", LangPL: "Wyeksportowany plik JSON", LangDE: "Exportierte JSON-Datei", LangFR: "Fichier JSON exporté", LangES: "Archivo JSON exportado"},
	// Short labels because these sit in a third-width column, where the full sentence wrapped to
	// three lines. Each still names its consequence, and the destructive one repeats the full
	// warning in confirm.import_replace at the moment it actually matters.
	"watchlist.import_mode_merge":   {LangEN: "Merge (keep existing)", LangPL: "Scal (zachowaj obecne)", LangDE: "Zusammenführen (Bestehendes behalten)", LangFR: "Fusionner (conserver l'existant)", LangES: "Combinar (mantener lo existente)"},
	"watchlist.import_mode_replace": {LangEN: "Replace (drop the rest)", LangPL: "Zastąp (usuń resztę)", LangDE: "Ersetzen (Rest verwerfen)", LangFR: "Remplacer (supprimer le reste)", LangES: "Reemplazar (descartar el resto)"},
	"watchlist.import_submit":       {LangEN: "Import", LangPL: "Importuj", LangDE: "Importieren", LangFR: "Importer", LangES: "Importar"},
	"watchlist.import_busy":         {LangEN: "Importing…", LangPL: "Importowanie…", LangDE: "Wird importiert…", LangFR: "Importation…", LangES: "Importando…"},
	"watchlist.export_empty":        {LangEN: "Nothing to export yet.", LangPL: "Nie ma jeszcze czego eksportować.", LangDE: "Noch nichts zu exportieren.", LangFR: "Rien à exporter pour l'instant.", LangES: "Aún no hay nada que exportar."},
	"watchlist.import_no_file":      {LangEN: "Choose a file to import.", LangPL: "Wybierz plik do zaimportowania.", LangDE: "Wähle eine Datei zum Importieren.", LangFR: "Choisissez un fichier à importer.", LangES: "Elige un archivo para importar."},
	"watchlist.import_invalid":      {LangEN: "Invalid upload.", LangPL: "Nieprawidłowy plik.", LangDE: "Ungültiger Upload.", LangFR: "Fichier importé invalide.", LangES: "Archivo no válido."},
	"watchlist.import_too_large":    {LangEN: "That file is too large to import.", LangPL: "Ten plik jest za duży, aby go zaimportować.", LangDE: "Diese Datei ist zu groß für den Import.", LangFR: "Ce fichier est trop volumineux pour être importé.", LangES: "Ese archivo es demasiado grande para importarlo."},
	"watchlist.import_done": {
		LangEN: "Imported: %d added, %d already tracked, %d removed.",
		LangPL: "Zaimportowano: %d dodanych, %d już obserwowanych, %d usuniętych.",
		LangDE: "Importiert: %d hinzugefügt, %d bereits erfasst, %d entfernt.",
		LangFR: "Importé : %d ajoutés, %d déjà suivis, %d supprimés.",
		LangES: "Importado: %d añadidos, %d ya seguidos, %d eliminados.",
	},
	"confirm.import_replace": {
		LangEN: "Replace mode will untrack every symbol missing from this file. Continue?",
		LangPL: "Tryb zastąpienia usunie wszystkie symbole, których nie ma w tym pliku. Kontynuować?",
		LangDE: "Der Ersetzen-Modus entfernt jedes Symbol aus der Beobachtung, das in dieser Datei fehlt. Fortfahren?",
		LangFR: "Le mode remplacement retirera de la surveillance tout symbole absent de ce fichier. Continuer ?",
		LangES: "El modo de reemplazo dejará de seguir todos los símbolos que falten en este archivo. ¿Continuar?",
	},
	"confirm.remove_watchlist_item": {
		LangEN: "Stop watching %s?",
		LangPL: "Przestać obserwować %s?",
		LangDE: "%s nicht mehr beobachten?",
		LangFR: "Arrêter de surveiller %s ?",
		LangES: "¿Dejar de seguir %s?",
	},
	"confirm.remove_watchlist_bulk": {
		LangEN: "Stop watching %d selected symbol(s)?",
		LangPL: "Przestać obserwować %d wybranych symboli?",
		LangDE: "%d ausgewählte(s) Symbol(e) nicht mehr beobachten?",
		LangFR: "Arrêter de surveiller %d symbole(s) sélectionné(s) ?",
		LangES: "¿Dejar de seguir %d símbolo(s) seleccionado(s)?",
	},

	// Account (account.html)
	"account.title":   {LangEN: "Atlas — Account", LangPL: "Atlas — Konto", LangDE: "Atlas — Konto", LangFR: "Atlas — Compte", LangES: "Atlas — Cuenta"},
	"account.heading": {LangEN: "Account", LangPL: "Konto", LangDE: "Konto", LangFR: "Compte", LangES: "Cuenta"},

	"account.portfolios_heading": {LangEN: "Portfolios", LangPL: "Portfele", LangDE: "Portfolios", LangFR: "Portefeuilles", LangES: "Carteras"},
	"account.portfolios_hint": {
		LangEN: "Separate buckets of positions/transactions — e.g. \"Main\", \"Safe\", \"Speculative\". Switch which one the Dashboard/Portfolio pages show using the tabs there; manage the list itself here.",
		LangPL: "Osobne zestawy pozycji/transakcji — np. \"Główny\", \"Bezpieczny\", \"Spekulacyjny\". Przełączaj, który z nich pokazują strony Dashboard/Portfel, tabami na tych stronach; samą listę zarządzasz tutaj.",
		LangDE: "Getrennte Gruppen von Positionen/Transaktionen — z. B. \"Haupt\", \"Sicher\", \"Spekulativ\". Wechsle über die Tabs auf den Seiten Übersicht/Portfolio, welches angezeigt wird; die Liste selbst verwaltest du hier.",
		LangFR: "Ensembles distincts de positions/transactions — par ex. \"Principal\", \"Sûr\", \"Spéculatif\". Changez celui affiché sur les pages Tableau de bord/Portefeuille via les onglets correspondants ; gérez la liste elle-même ici.",
		LangES: "Grupos independientes de posiciones/transacciones — p. ej. \"Principal\", \"Segura\", \"Especulativa\". Cambia cuál muestran las páginas Panel de control/Cartera con las pestañas correspondientes; la lista en sí se gestiona aquí.",
	},
	"account.portfolio_rename_submit": {LangEN: "Rename", LangPL: "Zmień nazwę", LangDE: "Umbenennen", LangFR: "Renommer", LangES: "Renombrar"},
	"account.portfolio_remove_submit": {LangEN: "Delete", LangPL: "Usuń", LangDE: "Löschen", LangFR: "Supprimer", LangES: "Eliminar"},
	"account.portfolio_new_name":      {LangEN: "New portfolio name", LangPL: "Nazwa nowego portfela", LangDE: "Name des neuen Portfolios", LangFR: "Nom du nouveau portefeuille", LangES: "Nombre de la nueva cartera"},
	"account.portfolio_create_submit": {LangEN: "Create portfolio", LangPL: "Utwórz portfel", LangDE: "Portfolio erstellen", LangFR: "Créer un portefeuille", LangES: "Crear cartera"},
	"account.portfolios_max_reached": {
		LangEN: "Maximum number of portfolios reached. Delete one to create another.",
		LangPL: "Osiągnięto maksymalną liczbę portfeli. Usuń jeden, aby utworzyć kolejny.",
		LangDE: "Maximale Anzahl an Portfolios erreicht. Lösche eines, um ein neues zu erstellen.",
		LangFR: "Nombre maximal de portefeuilles atteint. Supprimez-en un pour en créer un nouveau.",
		LangES: "Se alcanzó el número máximo de carteras. Elimina una para crear otra.",
	},
	"confirm.remove_portfolio": {
		LangEN: "Delete portfolio and everything in it — this cannot be undone. Continue?",
		LangPL: "Usunąć portfel wraz z całą jego zawartością — nie da się tego cofnąć. Kontynuować?",
		LangDE: "Portfolio und alles darin löschen — dies kann nicht rückgängig gemacht werden. Fortfahren?",
		LangFR: "Supprimer le portefeuille et tout son contenu — cette action est irréversible. Continuer ?",
		LangES: "¿Eliminar la cartera y todo su contenido? Esta acción no se puede deshacer. ¿Continuar?",
	},

	"account.ml_engine_heading": {LangEN: "ML in recommendations", LangPL: "ML w rekomendacjach", LangDE: "ML in Empfehlungen", LangFR: "ML dans les recommandations", LangES: "ML en las recomendaciones"},
	"account.ml_engine_hint": {
		LangEN: "Master switch for whether BUY/SELL/HOLD scoring factors in ML/portfolio/risk at all, on top of the technical score. Takes effect immediately, no restart needed. Model files below stay loaded and usable via /predict either way.",
		LangPL: "Główny przełącznik decydujący, czy ocena BUY/SELL/HOLD w ogóle uwzględnia ML/portfel/ryzyko, poza wynikiem technicznym. Działa natychmiast, bez restartu. Pliki modeli poniżej zostają załadowane i dostępne przez /predict niezależnie od tego ustawienia.",
		LangDE: "Hauptschalter dafür, ob die BUY/SELL/HOLD-Bewertung überhaupt ML/Portfolio/Risiko zusätzlich zum technischen Score berücksichtigt. Wirkt sofort, kein Neustart nötig. Die Modelldateien unten bleiben in jedem Fall geladen und über /predict nutzbar.",
		LangFR: "Interrupteur principal déterminant si le score BUY/SELL/HOLD prend en compte ML/portefeuille/risque en plus du score technique. Effet immédiat, aucun redémarrage nécessaire. Les fichiers de modèle ci-dessous restent chargés et utilisables via /predict dans tous les cas.",
		LangES: "Interruptor principal que determina si la puntuación BUY/SELL/HOLD tiene en cuenta ML/cartera/riesgo, además del score técnico. Se aplica de inmediato, sin necesidad de reiniciar. Los archivos de modelo de abajo permanecen cargados y utilizables vía /predict en cualquier caso.",
	},
	"account.ml_engine_enable_submit":  {LangEN: "Enable ML additive", LangPL: "Włącz dodatek ML", LangDE: "ML-Zusatz aktivieren", LangFR: "Activer l'apport ML", LangES: "Activar el aporte de ML"},
	"account.ml_engine_disable_submit": {LangEN: "Disable ML additive", LangPL: "Wyłącz dodatek ML", LangDE: "ML-Zusatz deaktivieren", LangFR: "Désactiver l'apport ML", LangES: "Desactivar el aporte de ML"},

	"account.ml_models_heading": {LangEN: "ML models", LangPL: "Modele ML", LangDE: "ML-Modelle", LangFR: "Modèles ML", LangES: "Modelos ML"},
	"account.ml_models_hint": {
		LangEN: "Pick which trained model is active for each prediction. Takes effect immediately, no restart needed.",
		LangPL: "Wybierz, który wytrenowany model jest aktywny dla każdej predykcji. Działa natychmiast, bez restartu.",
		LangDE: "Wähle aus, welches trainierte Modell für jede Vorhersage aktiv ist. Wirkt sofort, kein Neustart nötig.",
		LangFR: "Choisissez quel modèle entraîné est actif pour chaque prédiction. Effet immédiat, aucun redémarrage nécessaire.",
		LangES: "Elige qué modelo entrenado está activo para cada predicción. Se aplica de inmediato, sin necesidad de reiniciar.",
	},
	"account.ml_correction_heading": {LangEN: "Correction probability", LangPL: "Prawdopodobieństwo korekty", LangDE: "Korrekturwahrscheinlichkeit", LangFR: "Probabilité de correction", LangES: "Probabilidad de corrección"},
	"account.ml_recovery_heading":   {LangEN: "Recovery probability", LangPL: "Prawdopodobieństwo odbicia", LangDE: "Erholungswahrscheinlichkeit", LangFR: "Probabilité de reprise", LangES: "Probabilidad de recuperación"},
	"account.ml_no_models":          {LangEN: "No trained models found in the models directory.", LangPL: "Nie znaleziono wytrenowanych modeli w katalogu modeli.", LangDE: "Keine trainierten Modelle im Modellverzeichnis gefunden.", LangFR: "Aucun modèle entraîné trouvé dans le répertoire des modèles.", LangES: "No se encontraron modelos entrenados en el directorio de modelos."},
	"account.ml_none_active":        {LangEN: "None active — using the deterministic stub.", LangPL: "Brak aktywnego — używany deterministyczny placeholder.", LangDE: "Keines aktiv — es wird der deterministische Platzhalter verwendet.", LangFR: "Aucun actif — utilisation du substitut déterministe.", LangES: "Ninguno activo — se usa el sustituto determinista."},
	"account.ml_active_badge":       {LangEN: "active", LangPL: "aktywny", LangDE: "aktiv", LangFR: "actif", LangES: "activo"},
	"account.ml_activate_submit":    {LangEN: "Activate", LangPL: "Aktywuj", LangDE: "Aktivieren", LangFR: "Activer", LangES: "Activar"},
	"account.ml_delete_submit":      {LangEN: "Remove", LangPL: "Usuń", LangDE: "Entfernen", LangFR: "Supprimer", LangES: "Eliminar"},
	"confirm.remove_model": {
		LangEN: "Delete %s permanently? This can't be undone.",
		LangPL: "Usunąć %s na stałe? Tej operacji nie można cofnąć.",
		LangDE: "%s dauerhaft löschen? Dies kann nicht rückgängig gemacht werden.",
		LangFR: "Supprimer %s définitivement ? Cette action est irréversible.",
		LangES: "¿Eliminar %s de forma permanente? Esta acción no se puede deshacer.",
	},

	"account.train_heading": {LangEN: "Train new candidate", LangPL: "Wytrenuj nowego kandydata", LangDE: "Neuen Kandidaten trainieren", LangFR: "Entraîner un nouveau candidat", LangES: "Entrenar nuevo candidato"},
	"account.train_hint": {
		LangEN: "Exports fresh training data and trains a new, uniquely-versioned candidate for both models. Never changes what's active — pick it above once it looks good.",
		LangPL: "Eksportuje świeże dane treningowe i trenuje nowego, jednoznacznie wersjonowanego kandydata dla obu modeli. Nigdy nie zmienia aktywnego modelu — wybierz go powyżej, gdy wygląda dobrze.",
		LangDE: "Exportiert frische Trainingsdaten und trainiert für beide Modelle einen neuen, eindeutig versionierten Kandidaten. Ändert nie, welches Modell aktiv ist — wähle ihn oben aus, sobald er gut aussieht.",
		LangFR: "Exporte des données d'entraînement fraîches et entraîne un nouveau candidat, versionné de façon unique, pour les deux modèles. Ne change jamais ce qui est actif — sélectionnez-le ci-dessus une fois qu'il semble satisfaisant.",
		LangES: "Exporta datos de entrenamiento nuevos y entrena un candidato nuevo, con versión única, para ambos modelos. Nunca cambia cuál está activo — selecciónalo arriba cuando tenga buen aspecto.",
	},
	"account.train_submit": {LangEN: "Train now", LangPL: "Trenuj teraz", LangDE: "Jetzt trainieren", LangFR: "Entraîner maintenant", LangES: "Entrenar ahora"},
	"account.train_not_configured": {
		LangEN: "Not configured — set TRAINER_URL and start the atlas-trainer service to enable this, or run training/retrain.sh by hand/cron instead.",
		LangPL: "Nie skonfigurowano — ustaw TRAINER_URL i uruchom usługę atlas-trainer, żeby to włączyć, albo uruchom training/retrain.sh ręcznie/przez cron.",
		LangDE: "Nicht konfiguriert — setze TRAINER_URL und starte den atlas-trainer-Dienst, um dies zu aktivieren, oder führe stattdessen training/retrain.sh manuell/per Cron aus.",
		LangFR: "Non configuré — définissez TRAINER_URL et démarrez le service atlas-trainer pour l'activer, ou exécutez plutôt training/retrain.sh manuellement/via cron.",
		LangES: "No configurado — define TRAINER_URL e inicia el servicio atlas-trainer para habilitar esto, o ejecuta training/retrain.sh manualmente o por cron.",
	},
	"account.train_unreachable": {LangEN: "Trainer isn't reachable right now:", LangPL: "Trener jest teraz nieosiągalny:", LangDE: "Der Trainer ist momentan nicht erreichbar:", LangFR: "Le service d'entraînement est actuellement injoignable :", LangES: "El entrenador no está disponible en este momento:"},
	"account.train_running":     {LangEN: "Training is currently running…", LangPL: "Trening jest w trakcie…", LangDE: "Training läuft gerade…", LangFR: "Entraînement en cours…", LangES: "El entrenamiento está en curso…"},
	"account.train_last_result": {LangEN: "Last run", LangPL: "Ostatnie uruchomienie", LangDE: "Letzter Lauf", LangFR: "Dernière exécution", LangES: "Última ejecución"},
	"account.train_succeeded":   {LangEN: "succeeded", LangPL: "zakończone sukcesem", LangDE: "erfolgreich", LangFR: "réussi", LangES: "correcto"},
	"account.train_failed":      {LangEN: "failed", LangPL: "nieudane", LangDE: "fehlgeschlagen", LangFR: "échoué", LangES: "fallido"},
	"account.train_output":      {LangEN: "Output", LangPL: "Wynik", LangDE: "Ausgabe", LangFR: "Résultat", LangES: "Salida"},

	"account.data_heading": {LangEN: "Database backup", LangPL: "Kopia zapasowa bazy danych", LangDE: "Datenbank-Backup", LangFR: "Sauvegarde de la base de données", LangES: "Copia de seguridad de la base de datos"},
	"account.data_hint": {
		LangEN: "Downloads everything Atlas stores — positions, transactions, watchlist, price/indicator history, recommendations, settings — as one JSON file, restorable here or on a fresh environment.",
		LangPL: "Pobiera wszystko, co przechowuje Atlas — pozycje, transakcje, listę obserwowanych, historię cen/wskaźników, rekomendacje, ustawienia — jako jeden plik JSON, możliwy do przywrócenia tutaj lub na nowym środowisku.",
		LangDE: "Lädt alles herunter, was Atlas speichert — Positionen, Transaktionen, Beobachtungsliste, Kurs-/Indikatorenverlauf, Empfehlungen, Einstellungen — als eine einzige JSON-Datei, die hier oder in einer neuen Umgebung wiederhergestellt werden kann.",
		LangFR: "Télécharge tout ce qu'Atlas stocke — positions, transactions, liste de surveillance, historique des prix/indicateurs, recommandations, paramètres — en un seul fichier JSON, restaurable ici ou sur un nouvel environnement.",
		LangES: "Descarga todo lo que almacena Atlas — posiciones, transacciones, lista de seguimiento, historial de precios/indicadores, recomendaciones, configuración — en un único archivo JSON, restaurable aquí o en un entorno nuevo.",
	},
	"account.backup_submit":  {LangEN: "Download backup", LangPL: "Pobierz kopię zapasową", LangDE: "Backup herunterladen", LangFR: "Télécharger la sauvegarde", LangES: "Descargar copia de seguridad"},
	"account.leader_heading": {LangEN: "Background polling", LangPL: "Odpytywanie w tle", LangDE: "Hintergrund-Polling", LangFR: "Interrogation en arrière-plan", LangES: "Sondeo en segundo plano"},
	"account.leader_self":    {LangEN: "This instance is polling.", LangPL: "Ta instancja odpytuje.", LangDE: "Diese Instanz fragt ab.", LangFR: "Cette instance interroge.", LangES: "Esta instancia sondea."},
	"account.leader_other":   {LangEN: "Another instance is polling:", LangPL: "Odpytuje inna instancja:", LangDE: "Eine andere Instanz fragt ab:", LangFR: "Une autre instance interroge :", LangES: "Otra instancia sondea:"},
	"account.leader_none":    {LangEN: "No instance currently holds the polling lease.", LangPL: "Żadna instancja nie ma teraz dzierżawy odpytywania.", LangDE: "Keine Instanz hält derzeit die Polling-Lease.", LangFR: "Aucune instance ne détient actuellement le bail d'interrogation.", LangES: "Ninguna instancia tiene actualmente el arrendamiento de sondeo."},
	"account.restore_file":   {LangEN: "Backup JSON file", LangPL: "Plik JSON kopii zapasowej", LangDE: "Backup-JSON-Datei", LangFR: "Fichier JSON de sauvegarde", LangES: "Archivo JSON de copia de seguridad"},
	"account.restore_submit": {LangEN: "Restore from backup", LangPL: "Przywróć z kopii zapasowej", LangDE: "Aus Backup wiederherstellen", LangFR: "Restaurer depuis la sauvegarde", LangES: "Restaurar desde copia de seguridad"},
	"confirm.restore_backup": {
		LangEN: "Restoring will replace ALL current data with the contents of this file. Continue?",
		LangPL: "Przywrócenie zastąpi WSZYSTKIE obecne dane zawartością tego pliku. Kontynuować?",
		LangDE: "Die Wiederherstellung ersetzt ALLE aktuellen Daten durch den Inhalt dieser Datei. Fortfahren?",
		LangFR: "La restauration remplacera TOUTES les données actuelles par le contenu de ce fichier. Continuer ?",
		LangES: "La restauración reemplazará TODOS los datos actuales con el contenido de este archivo. ¿Continuar?",
	},

	"account.danger_heading": {LangEN: "Danger zone", LangPL: "Strefa zagrożenia", LangDE: "Gefahrenzone", LangFR: "Zone dangereuse", LangES: "Zona de peligro"},
	"account.flush_hint": {
		LangEN: "Permanently wipes positions, transactions, watchlist, price/indicator history, recommendations, and ML settings — back to a freshly installed Atlas. Your login isn't affected. There is no undo short of restoring a backup taken beforehand.",
		LangPL: "Trwale usuwa pozycje, transakcje, listę obserwowanych, historię cen/wskaźników, rekomendacje oraz ustawienia ML — Atlas wraca do stanu świeżej instalacji. Logowanie pozostaje bez zmian. Nie ma cofnięcia poza przywróceniem wcześniej zrobionej kopii zapasowej.",
		LangDE: "Löscht dauerhaft Positionen, Transaktionen, Beobachtungsliste, Kurs-/Indikatorenverlauf, Empfehlungen sowie ML-Einstellungen — Atlas kehrt in den Zustand einer frischen Installation zurück. Dein Login ist davon nicht betroffen. Es gibt kein Rückgängigmachen außer dem Wiederherstellen eines zuvor erstellten Backups.",
		LangFR: "Efface définitivement les positions, transactions, la liste de surveillance, l'historique des prix/indicateurs, les recommandations et les paramètres ML — retour à un Atlas fraîchement installé. Votre connexion n'est pas affectée. Il n'y a aucune annulation possible, hormis la restauration d'une sauvegarde effectuée au préalable.",
		LangES: "Borra permanentemente posiciones, transacciones, lista de seguimiento, historial de precios/indicadores, recomendaciones y configuración de ML — vuelve a un Atlas recién instalado. Tu inicio de sesión no se ve afectado. No hay forma de deshacerlo salvo restaurar una copia de seguridad hecha de antemano.",
	},
	"account.flush_submit": {LangEN: "Flush all data", LangPL: "Wyczyść wszystkie dane", LangDE: "Alle Daten löschen", LangFR: "Effacer toutes les données", LangES: "Borrar todos los datos"},
	"account.last_backup":  {LangEN: "Last backup:", LangPL: "Ostatnia kopia zapasowa:", LangDE: "Letztes Backup:", LangFR: "Dernière sauvegarde :", LangES: "Última copia de seguridad:"},
	"account.flush_locked_hint": {
		LangEN: "Locked — no backup has been taken yet. Download one above first; the button unlocks as soon as one exists.",
		LangPL: "Zablokowane — nie wykonano jeszcze żadnej kopii zapasowej. Najpierw pobierz ją powyżej; przycisk odblokuje się, gdy tylko kopia będzie istnieć.",
		LangDE: "Gesperrt — es wurde noch kein Backup erstellt. Lade zuerst oben eines herunter; der Button wird freigeschaltet, sobald eines existiert.",
		LangFR: "Verrouillé — aucune sauvegarde n'a encore été effectuée. Téléchargez-en d'abord une ci-dessus ; le bouton se débloque dès qu'une sauvegarde existe.",
		LangES: "Bloqueado — aún no se ha hecho ninguna copia de seguridad. Descarga una arriba primero; el botón se desbloquea en cuanto exista una.",
	},
	"account.flush_stale_hint": {
		LangEN: "Locked — your last backup is more than 5 minutes old. Download a fresh one above; the button unlocks right after.",
		LangPL: "Zablokowane — ostatnia kopia zapasowa ma ponad 5 minut. Pobierz nową powyżej; przycisk odblokuje się od razu potem.",
		LangDE: "Gesperrt — dein letztes Backup ist älter als 5 Minuten. Lade oben ein neues herunter; der Button wird direkt danach freigeschaltet.",
		LangFR: "Verrouillé — votre dernière sauvegarde date de plus de 5 minutes. Téléchargez-en une nouvelle ci-dessus ; le bouton se débloque juste après.",
		LangES: "Bloqueado — tu última copia de seguridad tiene más de 5 minutos. Descarga una nueva arriba; el botón se desbloquea justo después.",
	},
	"confirm.flush_all": {
		LangEN: "This permanently deletes ALL Atlas data (positions, transactions, watchlist, price history, recommendations, ML settings) and cannot be undone. Have you downloaded a backup? Continue?",
		LangPL: "To trwale usuwa WSZYSTKIE dane Atlasa (pozycje, transakcje, listę obserwowanych, historię cen, rekomendacje, ustawienia ML) i nie da się tego cofnąć. Czy pobrałeś(-aś) kopię zapasową? Kontynuować?",
		LangDE: "Dies löscht dauerhaft ALLE Atlas-Daten (Positionen, Transaktionen, Beobachtungsliste, Kursverlauf, Empfehlungen, ML-Einstellungen) und kann nicht rückgängig gemacht werden. Hast du ein Backup heruntergeladen? Fortfahren?",
		LangFR: "Ceci supprime définitivement TOUTES les données d'Atlas (positions, transactions, liste de surveillance, historique des prix, recommandations, paramètres ML) et ne peut pas être annulé. Avez-vous téléchargé une sauvegarde ? Continuer ?",
		LangES: "Esto elimina permanentemente TODOS los datos de Atlas (posiciones, transacciones, lista de seguimiento, historial de precios, recomendaciones, configuración de ML) y no se puede deshacer. ¿Has descargado una copia de seguridad? ¿Continuar?",
	},

	// Recommendation/trade action labels — applied via actionLabel wherever a raw BUY/SELL/HOLD/
	// ACCUMULATE/REDUCE/WAIT value is displayed (recommendations, alerts, backtests trades, the
	// portfolio transaction table and its manual-entry form). The underlying value used for
	// scoring/CSS-class matching (actionClass) and form submission is never translated — only
	// this display label is.
	"action.BUY":        {LangEN: "BUY", LangPL: "KUPNO", LangDE: "KAUFEN", LangFR: "ACHETER", LangES: "COMPRAR"},
	"action.SELL":       {LangEN: "SELL", LangPL: "SPRZEDAŻ", LangDE: "VERKAUFEN", LangFR: "VENDRE", LangES: "VENDER"},
	"action.HOLD":       {LangEN: "HOLD", LangPL: "TRZYMAJ", LangDE: "HALTEN", LangFR: "CONSERVER", LangES: "MANTENER"},
	"action.ACCUMULATE": {LangEN: "ACCUMULATE", LangPL: "DOKUPUJ", LangDE: "AUFSTOCKEN", LangFR: "ACCUMULER", LangES: "ACUMULAR"},
	"action.REDUCE":     {LangEN: "REDUCE", LangPL: "REDUKUJ", LangDE: "REDUZIEREN", LangFR: "RÉDUIRE", LangES: "REDUCIR"},
	"action.WAIT":       {LangEN: "WAIT", LangPL: "CZEKAJ", LangDE: "WARTEN", LangFR: "ATTENDRE", LangES: "ESPERAR"},

	// Decision-engine rationale strings — the closed, hardcoded set internal/application/
	// decision.UseCase actually returns in Recommendation.Reason (see BuildRecommendation and
	// waitOrHold). Keyed on the literal English sentence rather than a short code because these
	// are plain string literals in the engine, not an enum — reasonLabel falls back to the raw
	// English string for anything not listed here, so a future engine change adding a new reason
	// degrades to untranslated text instead of breaking rendering.
	"reason.Momentum signal is favorable":   {LangEN: "Momentum signal is favorable", LangPL: "Sygnał momentum jest korzystny", LangDE: "Momentum-Signal ist günstig", LangFR: "Le signal de momentum est favorable", LangES: "La señal de momentum es favorable"},
	"reason.Adding to an existing position": {LangEN: "Adding to an existing position", LangPL: "Powiększanie istniejącej pozycji", LangDE: "Aufstockung einer bestehenden Position", LangFR: "Renforcement d'une position existante", LangES: "Aumentando una posición existente"},
	"reason.Decision-support layer only":    {LangEN: "Decision-support layer only", LangPL: "Wyłącznie warstwa wsparcia decyzyjnego", LangDE: "Nur Entscheidungsunterstützungsebene", LangFR: "Uniquement une couche d'aide à la décision", LangES: "Solo capa de apoyo a la decisión"},
	"reason.Momentum signal is weak":        {LangEN: "Momentum signal is weak", LangPL: "Sygnał momentum jest słaby", LangDE: "Momentum-Signal ist schwach", LangFR: "Le signal de momentum est faible", LangES: "La señal de momentum es débil"},
	"reason.Momentum signal is weak, but no position exists to act on": {
		LangEN: "Momentum signal is weak, but no position exists to act on",
		LangPL: "Sygnał momentum jest słaby, ale nie ma pozycji, na której można by działać",
		LangDE: "Momentum-Signal ist schwach, aber es besteht keine Position, auf der gehandelt werden könnte",
		LangFR: "Le signal de momentum est faible, mais aucune position n'existe pour agir",
		LangES: "La señal de momentum es débil, pero no existe una posición sobre la que actuar",
	},
	"reason.Momentum signal is softening": {LangEN: "Momentum signal is softening", LangPL: "Sygnał momentum słabnie", LangDE: "Momentum-Signal schwächt sich ab", LangFR: "Le signal de momentum s'affaiblit", LangES: "La señal de momentum se está debilitando"},
	"reason.Trimming an existing position rather than exiting it": {
		LangEN: "Trimming an existing position rather than exiting it",
		LangPL: "Ograniczanie istniejącej pozycji zamiast jej zamykania",
		LangDE: "Verringerung einer bestehenden Position statt vollständigem Ausstieg",
		LangFR: "Réduction d'une position existante plutôt que sa clôture",
		LangES: "Reduciendo una posición existente en lugar de cerrarla",
	},
	"reason.Momentum signal is softening, but no position exists to act on": {
		LangEN: "Momentum signal is softening, but no position exists to act on",
		LangPL: "Sygnał momentum słabnie, ale nie ma pozycji, na której można by działać",
		LangDE: "Momentum-Signal schwächt sich ab, aber es besteht keine Position, auf der gehandelt werden könnte",
		LangFR: "Le signal de momentum s'affaiblit, mais aucune position n'existe pour agir",
		LangES: "La señal de momentum se está debilitando, pero no existe una posición sobre la que actuar",
	},
	"reason.Momentum signal leans favorable but isn't strong enough yet": {
		LangEN: "Momentum signal leans favorable but isn't strong enough yet",
		LangPL: "Sygnał momentum skłania się ku korzystnemu, ale nie jest jeszcze wystarczająco silny",
		LangDE: "Momentum-Signal tendiert positiv, ist aber noch nicht stark genug",
		LangFR: "Le signal de momentum penche favorablement mais n'est pas encore assez fort",
		LangES: "La señal de momentum se inclina a favor, pero aún no es lo bastante fuerte",
	},
	"reason.Mixed signal composition": {LangEN: "Mixed signal composition", LangPL: "Mieszany układ sygnałów", LangDE: "Gemischte Signalzusammensetzung", LangFR: "Composition de signaux mixte", LangES: "Composición de señales mixta"},
	"reason.ML confidence too low to act on this signal yet": {
		LangEN: "ML confidence too low to act on this signal yet",
		LangPL: "Pewność modelu ML jest jeszcze za niska, aby działać na podstawie tego sygnału",
		LangDE: "ML-Konfidenz noch zu niedrig, um auf dieses Signal zu reagieren",
		LangFR: "Confiance ML encore trop faible pour agir sur ce signal",
		LangES: "La confianza del ML aún es demasiado baja para actuar sobre esta señal",
	},
	"reason.No automated trading action": {LangEN: "No automated trading action", LangPL: "Brak automatycznego działania transakcyjnego", LangDE: "Keine automatisierte Handelsaktion", LangFR: "Aucune action de trading automatisée", LangES: "Sin acción de trading automatizada"},
	// These four are appended separately by internal/application/decision.Orchestrator.blend
	// (not UseCase.BuildRecommendation above) whenever ML/Portfolio/Risk scoring is wired in —
	// a second, independent source of Reason strings on the same Recommendation.Reason slice.
	"reason.ML signal supportive": {LangEN: "ML signal supportive", LangPL: "Sygnał ML jest wspierający", LangDE: "ML-Signal unterstützend", LangFR: "Signal ML favorable", LangES: "Señal ML favorable"},
	"reason.ML signal cautious":   {LangEN: "ML signal cautious", LangPL: "Sygnał ML jest ostrożny", LangDE: "ML-Signal vorsichtig", LangFR: "Signal ML prudent", LangES: "Señal ML cautelosa"},
	"reason.Portfolio risk context reduces conviction": {
		LangEN: "Portfolio risk context reduces conviction",
		LangPL: "Kontekst ryzyka portfela obniża pewność",
		LangDE: "Portfoliorisiko-Kontext verringert die Überzeugung",
		LangFR: "Le contexte de risque du portefeuille réduit la conviction",
		LangES: "El contexto de riesgo de la cartera reduce la convicción",
	},
	"reason.Elevated portfolio-wide risk (drawdown/volatility)": {
		LangEN: "Elevated portfolio-wide risk (drawdown/volatility)",
		LangPL: "Podwyższone ryzyko całego portfela (obsunięcie/zmienność)",
		LangDE: "Erhöhtes portfolioweites Risiko (Drawdown/Volatilität)",
		LangFR: "Risque élevé à l'échelle du portefeuille (drawdown/volatilité)",
		LangES: "Riesgo elevado en toda la cartera (drawdown/volatilidad)",
	},
	// Orchestrator's own degraded-input fallbacks (no bars/technical signal, or Decision is nil).
	"reason.No signal data available":    {LangEN: "No signal data available", LangPL: "Brak danych sygnałowych", LangDE: "Keine Signaldaten verfügbar", LangFR: "Aucune donnée de signal disponible", LangES: "No hay datos de señal disponibles"},
	"reason.Decision engine unavailable": {LangEN: "Decision engine unavailable", LangPL: "Silnik decyzyjny niedostępny", LangDE: "Entscheidungs-Engine nicht verfügbar", LangFR: "Moteur de décision indisponible", LangES: "Motor de decisión no disponible"},
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
