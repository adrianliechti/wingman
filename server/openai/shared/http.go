package shared

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func CompletionFailure(completion *provider.Completion) error {
	if completion != nil && completion.StopDetails != nil && completion.StopDetails.Type != "" {
		return errors.New("The model stopped generating: " + completion.StopDetails.Type)
	}
	//lint:ignore ST1005 Preserve the client-facing Responses error text.
	return errors.New("The model stopped generating without a usable result.")
}

func WriteJson(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("encode response: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// ErrorTypeFromError maps a ProviderError to an OpenAI-compatible error type string.
func ErrorTypeFromError(err error) string {
	return errorType(err, provider.CodeFromError(err, 0))
}

// OpenAI reports an exhausted quota with its own error type.
func errorType(err error, code int) string {
	if kind := provider.KindFromError(err); kind == provider.ErrorKindQuotaExceeded || kind == provider.ErrorKindBilling {
		return "insufficient_quota"
	}
	return errorTypeFromCode(code)
}

// ErrorCodeFromError returns the OpenAI error code. Codes from other vendors
// would mislead OpenAI clients, so the error kind supplies their code.
func ErrorCodeFromError(err error) string {
	if code := provider.NativeType(err, provider.VendorOpenAI); code != "" {
		return code
	}
	switch provider.KindFromError(err) {
	case provider.ErrorKindQuotaExceeded, provider.ErrorKindBilling:
		return "insufficient_quota"
	case provider.ErrorKindRateLimited:
		return "rate_limit_exceeded"
	}
	return ""
}

func errorTypeFromCode(code int) string {
	switch {
	case code == http.StatusUnauthorized:
		return "authentication_error"
	case code == http.StatusForbidden:
		return "permission_error"
	case code == http.StatusNotFound:
		return "not_found_error"
	case code == http.StatusRequestTimeout || code == http.StatusGatewayTimeout:
		return "timeout_error"
	case code == http.StatusConflict:
		return "conflict_error"
	case code == http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case code == http.StatusServiceUnavailable:
		return "service_unavailable_error"
	case code >= 500:
		return "server_error"
	case code >= 400:
		return "invalid_request_error"
	default:
		return "server_error"
	}
}

func WriteError(w http.ResponseWriter, code int, err error) {
	code = provider.CodeFromError(err, code)
	if kind := provider.KindFromError(err); kind == provider.ErrorKindQuotaExceeded || kind == provider.ErrorKindBilling {
		code = http.StatusTooManyRequests
		// The OpenAI SDK decides whether to retry before it reads the body.
		w.Header().Set("x-should-retry", "false")
	}

	// Propagate Retry-After from upstream
	if v := provider.RetryAfterHeaderValue(provider.RetryAfterFromError(err)); v != "" {
		w.Header().Set("Retry-After", v)
	}

	errorType := errorType(err, code)

	resp := ErrorResponse{
		Error: Error{
			Type:    errorType,
			Code:    ErrorCodeFromError(err),
			Message: err.Error(),
		},
	}

	if apiErr, ok := errors.AsType[*Error](err); ok {
		resp.Error = *apiErr

		if resp.Error.Type == "" {
			resp.Error.Type = errorType
		}

		if resp.Error.Code == "" {
			resp.Error.Code = "invalid_value"
		}
	}

	var body any = resp

	// Native OpenAI error objects already use this wire format. Preserve all
	// fields, including param, null codes and details unknown to the SDK.
	if upstream := provider.NativeError(err, provider.VendorOpenAI); upstream != nil && upstream.RawJSON != "" {
		raw := jsontext.Value(upstream.RawJSON)
		var fields Error
		// Other services using the SDK can return incompatible shapes (for
		// example numeric Google error codes); retain normalization for those.
		if json.Unmarshal(raw, &fields) == nil && fields.Type != "" && fields.Message != "" {
			body = struct {
				Error jsontext.Value `json:"error"`
			}{Error: raw}
		}
	}

	data, encodeErr := json.Marshal(body)
	if encodeErr != nil {
		code = http.StatusInternalServerError
		data = []byte(`{"error":{"type":"server_error","code":"server_error","message":"could not encode error response"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

// WriteSSERetry emits an SSE "retry:" field (milliseconds) if the error
// carries a RetryAfter duration. Must be written before the event/data lines
// of the SSE message block it belongs to.
func WriteSSERetry(w http.ResponseWriter, err error) {
	d := provider.RetryAfterFromError(err)
	if d <= 0 {
		return
	}

	ms := max(d.Milliseconds(), 1)

	fmt.Fprintf(w, "retry: %d\n", ms)
}
