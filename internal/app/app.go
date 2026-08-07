package app

import (
	"context"
	"errors"
	"fmt"

	alertsUC "atlas/internal/application/alerts"
	analyzerUC "atlas/internal/application/analyzer"
	authUC "atlas/internal/application/auth"
	backtestingUC "atlas/internal/application/backtesting"
	collectorUC "atlas/internal/application/collector"
	decisionUC "atlas/internal/application/decision"
	exchangerateUC "atlas/internal/application/exchangerate"
	mlUC "atlas/internal/application/ml"
	notificationsUC "atlas/internal/application/notifications"
	portfolioUC "atlas/internal/application/portfolio"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/config"
	"atlas/internal/domain/market"
	"atlas/internal/infrastructure/cache"
	infraExchangerate "atlas/internal/infrastructure/exchangerate"
	"atlas/internal/infrastructure/notifications"
	"atlas/internal/infrastructure/persistence"
	"atlas/internal/infrastructure/providers"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

// Application is the composition root. It wires the infrastructure adapters (market data
// provider, persistence, cache, notifications) into the tested application use cases so they
// are actually reachable through the HTTP layer. Prices, portfolio positions/transactions,
// the watchlist, and every live recommendation/prediction are all backed by TimescaleDB and
// survive restarts (see handlers.go's persistRecommendation/persistPrediction); indicators
// are still computed fresh per request — nothing persists the intermediate indicator series
// itself. Backtesting's simulated recommendations are deliberately excluded from persistence
// since they're hypothetical, not real, decisions. The Orchestrator is wired via
// NewOrchestratorWithML, so recommendations genuinely blend technical + ML + portfolio-risk
// signals (see decision.Orchestrator.blendMLAndPortfolio) — not just technical score, even
// though the ML component is still a deterministic stub. Auth (see internal/application/auth)
// is optional, same non-fatal pattern as ML/Discord: unset ADMIN_USERNAME/ADMIN_PASSWORD_HASH
// means Auth.Enabled() is false and every route is reachable with no login gate, exactly as
// before this feature existed — see authMiddleware.
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
	MLModel       *mlUC.OnnxModel // nil unless MODEL_PATH loaded successfully; owns the onnxruntime session
	Notifications *notificationsUC.UseCase
	Backtesting   *backtestingUC.UseCase
	Watchlist     *watchlistUC.UseCase
	Alerts        *alertsUC.UseCase
	Auth          *authUC.UseCase
	ExchangeRates *exchangerateUC.UseCase
}

// New connects to TimescaleDB and Redis and wires the full application. A connection
// failure here is fatal and returned to the caller rather than silently degrading: Atlas
// can't give a truthful recommendation without real historical data (see
// persistence.NewTimescaleDB's doc comment).
func New(ctx context.Context, cfg config.Config, metrics *observability.Metrics, logger *zap.Logger) (*Application, error) {
	dsn := persistence.DSN(cfg.DatabaseHost, cfg.DatabasePort, cfg.DatabaseName, cfg.DatabaseUser, cfg.DatabasePassword)
	storage, err := persistence.NewTimescaleDB(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to timescaledb: %w", err)
	}

	redisCache := cache.NewRedisClient(cfg.RedisHost, cfg.RedisPort)
	if err := redisCache.Ping(ctx); err != nil {
		_ = storage.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}

	discord := notifications.NewDiscordClient(cfg.DiscordWebhookURL)

	provider := newProvider()
	barsCache := &redisBarsCache{client: redisCache}
	collector := collectorUC.NewUseCaseWithStorageAndCache(provider, storage, barsCache)
	analyzer := analyzerUC.NewUseCase()

	// A missing/unloadable model is non-fatal: Atlas falls back to the deterministic
	// RSI-derived stub (see mlUC.UseCase.Predict) rather than refusing to start, unlike the
	// TimescaleDB/Redis connections above — this is a genuinely optional enhancement, not a
	// dependency the app can't function without. See training/README.md.
	mlUseCase := mlUC.NewUseCase()
	var mlModel *mlUC.OnnxModel
	if cfg.ModelPath != "" {
		model, err := mlUC.LoadOnnxModel(cfg.ModelPath, cfg.LibOnnxRuntimePath)
		if err != nil {
			logger.Warn("failed to load ML model; falling back to deterministic RSI stub",
				zap.String("model_path", cfg.ModelPath), zap.Error(err))
		} else {
			mlModel = model
			mlUseCase = mlUC.NewUseCaseWithModel(model)
			logger.Info("loaded ML model", zap.String("version", model.Version))
		}
	}

	// NBP being unreachable is non-fatal, same stance as the ML model above: a missing/failed
	// rate fetch only blocks recording a *non-PLN* transaction (see portfolio.UseCase.
	// RecordTransaction) — PLN-only usage, which is everything that worked before this feature
	// existed, needs no exchange rate at all.
	exchangeRates := exchangerateUC.NewUseCase(infraExchangerate.NewNBPProvider(), &redisRatesCache{client: redisCache})

	portfolioUseCase, err := portfolioUC.NewUseCaseWithRepository(storage)
	if err != nil {
		_ = storage.Close()
		_ = redisCache.Close()
		return nil, fmt.Errorf("load portfolio state: %w", err)
	}
	portfolioUseCase.ExchangeRates = exchangeRates
	decisionUseCase := decisionUC.NewUseCase()
	orchestrator := decisionUC.NewOrchestratorWithML(analyzer, portfolioUseCase, decisionUseCase, mlUseCase)

	watchlist, err := watchlistUC.NewUseCaseWithRepository(storage)
	if err != nil {
		_ = storage.Close()
		_ = redisCache.Close()
		return nil, fmt.Errorf("load watchlist state: %w", err)
	}

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
		ML:            mlUseCase,
		MLModel:       mlModel,
		Notifications: notificationsUC.NewUseCase(discord),
		Backtesting:   backtestingUC.NewUseCase(orchestrator),
		Watchlist:     watchlist,
		Alerts:        alertsUC.NewUseCase(watchlist, collector, orchestrator, cfg.CollectorWindow),
		Auth:          authUC.NewUseCase(cfg.AdminUsername, cfg.AdminPasswordHash, redisCache, storage),
		ExchangeRates: exchangeRates,
	}, nil
}

// Close releases the database, cache, and (if loaded) ML model resources. Call it during
// graceful shutdown.
func (a *Application) Close() error {
	var errs []error
	if err := a.Storage.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close timescaledb: %w", err))
	}
	if err := a.Cache.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close redis: %w", err))
	}
	if a.MLModel != nil {
		if err := a.MLModel.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close ml model: %w", err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// newProvider returns the market data provider. Yahoo Finance has no official, supported API
// (see PROJECT_CONTEXT.md); its unofficial endpoint is the only provider now — confirmed
// reachable with a browser-like User-Agent header and confirmed to support the hourly bars this
// engine needs. Stooq was evaluated as a free alternative and removed: its free endpoint serves
// a client-side JavaScript bot challenge instead of data from this environment, and even where
// reachable its free CSV export is daily-only (no intraday), which wouldn't meet the
// hourly-granularity requirement anyway. cfg.MarketProvider (MARKET_PROVIDER) still exists as a
// metrics label (see handlers.go), just not as a selector, since there's nothing left to select.
func newProvider() market.MarketProvider {
	return providers.NewYahooFinanceProvider()
}
