package shared

type ErrorResponse struct {
	Error Error `json:"error,omitempty"`
}

type Error struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
	Message string `json:"message"`
}
