package guard

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

type Provider interface {
	Check(ctx context.Context, text string, options *CheckOptions) (*Result, error)
}

var (
	ErrUnsupported = errors.New("unsupported type")
)

type FlaggedError struct {
	Categories []Category
}

func (e *FlaggedError) Error() string {
	if len(e.Categories) == 0 {
		return "content flagged by guard"
	}

	categories := make([]string, 0, len(e.Categories))

	for _, c := range e.Categories {
		categories = append(categories, c.Name+" ("+strconv.FormatFloat(c.Score, 'f', -1, 64)+")")
	}

	return "content flagged by guard: " + strings.Join(categories, ", ")
}

type CheckOptions struct {
}

type Result struct {
	Flagged bool

	Categories []Category
}

type Category struct {
	Name  string
	Score float64
}
