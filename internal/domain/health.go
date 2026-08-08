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
}
