package web

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
)

type URLList []string

func (u *URLList) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case '"':
		var single string
		if err := json.UnmarshalDecode(dec, &single); err != nil {
			return err
		}
		*u = []string{single}
		return nil
	case '[', 'n':
		return json.UnmarshalDecode(dec, (*[]string)(u))
	default:
		return errors.New("urls must be a string or array of strings")
	}
}

func (h *Retriever) Scraper(r *http.Request, model string) (scraper.Provider, error) {
	p, err := h.Config.Scraper(model)
	if err != nil {
		return nil, BadRequest(err.Error())
	}
	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		return nil, &requestError{404, "scraper not found"}
	}
	return p, nil
}

func ValidateURLs(urls []string) error {
	if len(urls) < 1 || len(urls) > 20 {
		return errors.New("provide between 1 and 20 URLs")
	}
	for _, value := range urls {
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return errors.New("urls must contain absolute HTTP(S) URLs without credentials")
		}
	}
	return nil
}

type Extraction struct {
	URL  string
	Text string
	Err  error
}

// SearchContents reuses source text already retrieved by any search provider.
// Only providers that returned excerpts without attempting full-text retrieval
// need a scraper. An attempted but empty retrieval remains a partial failure.
func (h *Retriever) SearchContents(r *http.Request, model string, hits []searcher.Result) ([]Extraction, error) {
	results := make([]Extraction, len(hits))
	var urls []string
	var missing []int
	for i, hit := range hits {
		results[i].URL = hit.Source
		if hit.Text == nil {
			urls = append(urls, hit.Source)
			missing = append(missing, i)
			continue
		}
		results[i].Text = *hit.Text
		if results[i].Text == "" {
			results[i].Err = errors.New("no content returned")
		}
	}
	if len(urls) == 0 {
		return results, nil
	}
	p, err := h.Scraper(r, model)
	if err != nil {
		return nil, err
	}
	for i, result := range Extract(r.Context(), p, urls, 0) {
		results[missing[i]] = result
	}
	return results, nil
}

// Extract bounds the whole batch by timeout, which SDKs also use as their HTTP
// timeout, so slow pages fail individually instead of losing every result.
func Extract(ctx context.Context, p scraper.Provider, urls []string, timeout time.Duration) []Extraction {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	results := make([]Extraction, len(urls))
	var wg sync.WaitGroup
	workers := make(chan struct{}, 4)
	for i, value := range urls {
		wg.Go(func() {
			result := Extraction{URL: value}
			defer func() { results[i] = result }()
			select {
			case workers <- struct{}{}:
			case <-ctx.Done():
				result.Err = ctx.Err()
				return
			}
			defer func() { <-workers }()
			if err := ValidateURLs([]string{value}); err != nil {
				result.Err = err
				return
			}
			doc, err := p.Scrape(ctx, value, &scraper.ScrapeOptions{})
			if err != nil {
				result.Err = err
				return
			}
			if doc == nil || doc.Text == "" {
				result.Err = errors.New("no content returned")
				return
			}
			result.Text = doc.Text
		})
	}
	wg.Wait()
	return results
}

func Truncate(text string, limit int) string {
	if limit > 0 {
		chars := []rune(text)
		if len(chars) > limit {
			return string(chars[:limit])
		}
	}
	return text
}
