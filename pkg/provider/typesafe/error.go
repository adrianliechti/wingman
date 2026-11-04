package typesafe

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
)

// APIError retains TypeSafe's structured detail for callers using errors.As.
type APIError struct {
	Detail jsontext.Value
}

func (e *APIError) Error() string {
	var message string
	if json.Unmarshal(e.Detail, &message) == nil {
		return message
	}
	var object struct {
		Message string `json:"message"`
		Msg     string `json:"msg"`
	}
	if json.Unmarshal(e.Detail, &object) == nil {
		if object.Message != "" {
			return object.Message
		}
		if object.Msg != "" {
			return object.Msg
		}
	}
	var issues []struct {
		Location []any  `json:"loc"`
		Message  string `json:"msg"`
	}
	if json.Unmarshal(e.Detail, &issues) == nil && len(issues) > 0 {
		var messages []string
		for _, issue := range issues {
			if issue.Message == "" {
				return string(e.Detail)
			}
			var location []string
			for _, part := range issue.Location {
				location = append(location, fmt.Sprint(part))
			}
			message := issue.Message
			if len(location) > 0 {
				message = strings.Join(location, ".") + ": " + message
			}
			messages = append(messages, message)
		}
		return strings.Join(messages, "; ")
	}
	return string(e.Detail)
}

func (e *APIError) errorType() string {
	var object struct {
		Type string `json:"type"`
		Code string `json:"code"`
	}
	if json.Unmarshal(e.Detail, &object) == nil {
		if object.Type != "" {
			return object.Type
		}
		return object.Code
	}
	var issues []jsontext.Value
	if json.Unmarshal(e.Detail, &issues) == nil && len(issues) > 0 {
		return "validation_error"
	}
	return ""
}
