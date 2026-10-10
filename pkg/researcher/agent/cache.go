package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
)

// Caches live inside one Research call, never on the shared Client. Bound both
// retained bytes and entries; failed, empty and oversized results are not kept.
const maxCacheEntries = 16
const maxCacheBytes = 8 * 1024 * 1024
const maxCacheEntryBytes = 2 * 1024 * 1024

type cacheEntry[T any] struct {
	done  chan struct{}
	value T
	err   error
}
type runCache[T any] struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry[T]
	bytes   int
}

func (c *runCache[T]) get(ctx context.Context, key string, retrieve func() (T, int, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	c.mu.Lock()
	if entry, found := c.entries[key]; found {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-entry.done:
			return entry.value, entry.err
		}
	}
	if c.entries == nil {
		c.entries = map[string]*cacheEntry[T]{}
	}
	if len(c.entries) >= maxCacheEntries {
		c.mu.Unlock()
		value, _, err := retrieve()
		return value, err
	}
	entry := &cacheEntry[T]{done: make(chan struct{})}
	c.entries[key] = entry
	c.mu.Unlock()
	value, size, err := retrieve()
	c.mu.Lock()
	entry.value, entry.err = value, err
	if err != nil || size <= 0 || size > maxCacheEntryBytes || c.bytes+size > maxCacheBytes {
		delete(c.entries, key)
	} else {
		c.bytes += size
	}
	close(entry.done)
	c.mu.Unlock()
	return value, err
}

type cachedSearcher struct {
	searcher.Provider
	cache runCache[[]searcher.Result]
}

func (c *cachedSearcher) Capabilities() searcher.Capabilities {
	return c.Provider.Capabilities()
}

func (c *cachedSearcher) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	query = strings.TrimSpace(query)
	key, _ := json.Marshal(struct {
		Query   string
		Options *searcher.SearchOptions
	}{query, options})
	return c.cache.get(ctx, string(key), func() ([]searcher.Result, int, error) {
		results, err := c.Provider.Search(ctx, query, options)
		size := 0
		if len(results) > 0 {
			data, _ := json.Marshal(results)
			size = len(data)
		}
		return results, size, err
	})
}

type cachedScraper struct {
	scraper.Provider
	cache runCache[*scraper.Document]
}

func (c *cachedScraper) Scrape(ctx context.Context, source string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	return c.cache.get(ctx, strings.TrimSpace(source), func() (*scraper.Document, int, error) {
		doc, err := c.Provider.Scrape(ctx, source, options)
		size := 0
		if doc != nil {
			size = len(doc.Text)
		}
		return doc, size, err
	})
}
