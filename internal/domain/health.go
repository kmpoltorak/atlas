package domain

type HealthStatus struct {
	Status   string `json:"status"`
	Service  string `json:"service"`
	Database string `json:"database"`
	Redis    string `json:"redis"`
	// Trainer is omitted entirely when TRAINER_URL isn't configured — the trainer sidecar is a
	// genuinely optional component (see docker-compose.yml's "trainer" profile), so its absence
	// is not reported as an error the way Database/Redis would be.
	Trainer string `json:"trainer,omitempty"`
	// Prometheus/Alertmanager/Grafana are purely informational, like Trainer — omitted when
	// their URL is configured empty, and never affect Status/the HTTP status code the way
	// Database/Redis do: atlas-api doesn't depend on any of them to function, so Docker/
	// Kubernetes restarting it because Grafana (say) is down would be exactly the wrong
	// response to that outage.
	Prometheus   string `json:"prometheus,omitempty"`
	Alertmanager string `json:"alertmanager,omitempty"`
	Grafana      string `json:"grafana,omitempty"`
}
