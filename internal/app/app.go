package app

import (
	"atlas/internal/config"
	"atlas/internal/observability"
)

type Application struct {
	Config  config.Config
	Metrics *observability.Metrics
}

func New(cfg config.Config, metrics *observability.Metrics) *Application {
	return &Application{
		Config:  cfg,
		Metrics: metrics,
	}
}
