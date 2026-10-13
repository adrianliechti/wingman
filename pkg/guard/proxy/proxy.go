package proxy

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/guard"
)

func check(ctx context.Context, provider guard.Provider, text string) error {
	result, err := provider.Check(ctx, text, nil)

	if err != nil {
		return err
	}

	if result == nil {
		return errors.New("guard returned no result")
	}

	if result.Flagged {
		return &guard.FlaggedError{
			Categories: result.Categories,
		}
	}

	return nil
}
