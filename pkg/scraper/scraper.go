package scraper

import (
	"context"
	"errors"
)

type Provider interface {
	Scrape(ctx context.Context, url string, options *ScrapeOptions) (*Document, error)
}

var (
	ErrUnsupported = errors.New("unsupported type")
)

type ScrapeOptions struct {
}

type Document struct {
	Text string
}
