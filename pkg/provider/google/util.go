package google

import (
	"errors"

	"github.com/adrianliechti/wingman/pkg/provider"

	"google.golang.org/api/googleapi"
)

func convertError(err error) error {
	var apierr *googleapi.Error

	if errors.As(err, &apierr) {
		return &provider.ProviderError{
			StatusCode: apierr.Code,
			Message:    apierr.Body,
			Err:        err,
		}
	}

	return err
}
