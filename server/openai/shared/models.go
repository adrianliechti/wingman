package shared

type ErrorResponse struct {
	Error Error `json:"error"`
}

type Error struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
	Message string `json:"message"`
}

type InvalidValueError struct {
	Param   string
	Message string
}

func (e *InvalidValueError) Error() string {
	return e.Message
}
