package guard

import (
	"context"
	"errors"
)

func check(ctx context.Context, provider Provider, text string) error {
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
