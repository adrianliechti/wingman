package shared

type ErrorResponse struct {
	Error Error `json:"error"`
}

type Error struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return e.Message
}
