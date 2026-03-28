package anthropic

import (
	"errors"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go"
)

func convertError(err error) error {
	var apierr *anthropic.Error

	if errors.As(err, &apierr) {
		provErr := &provider.ProviderError{
			StatusCode: apierr.StatusCode,
			Message:    apierr.Error(),
			Err:        err,
		}

		if apierr.Response != nil {
			provErr.RetryAfter = provider.ParseRetryAfter(apierr.Response.Header.Get("Retry-After"))
		}

		return provErr
	}

	return err
}
