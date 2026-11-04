package shared

type ErrorResponse struct {
	Error Error `json:"error"`
}

type Error struct {
	Type    string `json:"type,omitzero"`
	Code    string `json:"code,omitzero"`
	Param   string `json:"param,omitzero"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return e.Message
}

// ModelNotFound is OpenAI's error for an unknown or inaccessible model. It
// deliberately doesn't say which.
func ModelNotFound(model string) *Error {
	return &Error{
		Type:    "invalid_request_error",
		Code:    "model_not_found",
		Param:   "model",
		Message: "The model `" + model + "` does not exist or you do not have access to it.",
	}
}
