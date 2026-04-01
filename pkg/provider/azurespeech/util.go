package azurespeech

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func convertError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	message := http.StatusText(resp.StatusCode)
	if len(data) > 0 {
		message = string(data)
	}

	return &provider.ProviderError{
		StatusCode: resp.StatusCode,
		Message:    message,
		RetryAfter: parseRetryAfter(resp.Header),
	}
}

// parseRetryAfter parses Retry-After (seconds, float, HTTP-date).
func parseRetryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}

	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}

	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(secs * float64(time.Second))
	}

	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}

	return 0
}
