package guard

import (
	"context"
	"errors"
)

// Check returns an error if the guard fails or flags the text.
func Check(ctx context.Context, provider Provider, text string) error {
	result, err := provider.Check(ctx, text, nil)

	if err != nil {
		return err
	}

	if result == nil {
		return errors.New("guard returned no result")
	}

	if result.Flagged {
		return &FlaggedError{
			Categories: result.Categories,
		}
	}

	return nil
}
