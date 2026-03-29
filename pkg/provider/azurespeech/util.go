package azurespeech

import (
	"io"
	"net/http"

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
		RetryAfter: provider.ParseRetryAfter(resp.Header.Get("Retry-After")),
	}
}
