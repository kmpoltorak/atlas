package watchlist

// Item represents a single symbol the user wants to keep under observation.
type Item struct {
	Symbol  string `json:"symbol"`
	AddedAt string `json:"added_at"`
}
