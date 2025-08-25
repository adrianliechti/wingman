package shared

type ErrorResponse struct {
	Error Error `json:"error,omitempty"`
}

type Error struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
