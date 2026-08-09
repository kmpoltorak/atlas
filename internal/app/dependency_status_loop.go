package app

import (
	"context"
	"time"
)

// dependencyPollInterval matches prometheus.yml's global scrape_interval — no point refreshing
// the gauges faster than Prometheus actually reads them.
const dependencyPollInterval = 15 * time.Second

// StartDependencyStatusPolling periodically pings every dependency atlas-api talks to (see
// dependencyStatuses) and updates Metrics.DependencyUp, so Grafana can graph each one's
// reachability over time instead of only ever seeing a single point-in-time /health response.
// Runs an immediate check before the first tick so the gauges aren't empty for up to a full
// interval right after startup. Returns a stop function, same pattern as StartAlertPolling.
func (a *Application) StartDependencyStatusPolling() func() {
	ticker := time.NewTicker(dependencyPollInterval)
	done := make(chan struct{})

	update := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for dependency, up := range a.dependencyStatuses(ctx) {
			value := 0.0
			if up {
				value = 1
			}
			a.Metrics.DependencyUp.WithLabelValues(dependency).Set(value)
		}
	}
	update()

	go func() {
		for {
			select {
			case <-ticker.C:
				update()
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()

	return func() { close(done) }
}
