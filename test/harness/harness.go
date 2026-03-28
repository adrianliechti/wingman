package harness

// Endpoint represents an API target.
type Endpoint struct {
	Name    string
	BaseURL string // e.g. "http://localhost:8080/v1"
	APIKey  string
}
