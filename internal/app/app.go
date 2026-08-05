package app

import (
	alertsUC "atlas/internal/application/alerts"
	analyzerUC "atlas/internal/application/analyzer"
	backtestingUC "atlas/internal/application/backtesting"
	collectorUC "atlas/internal/application/collector"
	decisionUC "atlas/internal/application/decision"
	mlUC "atlas/internal/application/ml"
	notificationsUC "atlas/internal/application/notifications"
	portfolioUC "atlas/internal/application/portfolio"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/config"
	"atlas/internal/domain/market"
	"atlas/internal/infrastructure/cache"
	"atlas/internal/infrastructure/notifications"
	"atlas/internal/infrastructure/persistence"
	"atlas/internal/infrastructure/providers"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

// Application is the composition root. It wires the infrastructure adapters (market data
// provider, persistence, cache, notifications) into the tested application use cases so they
// are actually reachable through the HTTP layer.
type Application struct {
	Config  config.Config
	Metrics *observability.Metrics
	Logger  *zap.Logger

	Storage *persistence.TimescaleDB
	Cache   *cache.RedisClient
	Discord *notifications.DiscordClient

	Collector     *collectorUC.UseCase
	Analyzer      *analyzerUC.UseCase
	Portfolio     *portfolioUC.UseCase
	Decision      *decisionUC.UseCase
	Orchestrator  *decisionUC.Orchestrator
	ML            *mlUC.UseCase
	Notifications *notificationsUC.UseCase
	Backtesting   *backtestingUC.UseCase
	Watchlist     *watchlistUC.UseCase
	Alerts        *alertsUC.UseCase
}

func New(cfg config.Config, metrics *observability.Metrics, logger *zap.Logger) *Application {
	storage := persistence.NewTimescaleDB(cfg.DatabaseHost, cfg.DatabasePort, cfg.DatabaseName, cfg.DatabaseUser, cfg.DatabasePassword)
	redisCache := cache.NewRedisClient(cfg.RedisHost, cfg.RedisPort)
	discord := notifications.NewDiscordClient(cfg.DiscordWebhookURL)

	provider := newProvider(cfg)
	collector := collectorUC.NewUseCaseWithStorage(provider, storage)
	analyzer := analyzerUC.NewUseCase()
	portfolioUseCase := portfolioUC.NewUseCase()
	decisionUseCase := decisionUC.NewUseCase()
	orchestrator := decisionUC.NewOrchestrator(analyzer, portfolioUseCase, decisionUseCase)
	watchlist := watchlistUC.NewUseCase()

	return &Application{
		Config:  cfg,
		Metrics: metrics,
		Logger:  logger,

		Storage: storage,
		Cache:   redisCache,
		Discord: discord,

		Collector:     collector,
		Analyzer:      analyzer,
		Portfolio:     portfolioUseCase,
		Decision:      decisionUseCase,
		Orchestrator:  orchestrator,
		ML:            mlUC.NewUseCase(),
		Notifications: notificationsUC.NewUseCase(discord),
		Backtesting:   backtestingUC.NewUseCase(orchestrator),
		Watchlist:     watchlist,
		Alerts:        alertsUC.NewUseCase(watchlist, collector, orchestrator, cfg.CollectorWindow),
	}
}

// newProvider selects the market data provider from configuration. Yahoo Finance has no
// official, supported API (see PROJECT_CONTEXT.md); its unofficial endpoint is the default
// since it's confirmed reachable with a browser-like User-Agent header. Stooq is a free,
// keyless CSV alternative available via MARKET_PROVIDER=stooq, but its endpoint enforces a
// JavaScript bot challenge from at least some networks — verify it works from the actual
// deployment host before relying on it.
func newProvider(cfg config.Config) market.MarketProvider {
	switch cfg.MarketProvider {
	case "stooq":
		return providers.NewStooqProvider()
	default:
		return providers.NewYahooFinanceProvider()
	}
}
