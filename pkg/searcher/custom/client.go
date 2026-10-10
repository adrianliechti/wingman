package custom

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	_ searcher.Provider = (*Client)(nil)
)

type Client struct {
	client       SearcherClient
	capabilities searcher.Capabilities

	category string
	location string
}

func New(url string, options ...Option) (*Client, error) {
	if !strings.HasPrefix(url, "grpc://") || strings.TrimSpace(strings.TrimPrefix(url, "grpc://")) == "" {
		return nil, errors.New("invalid url")
	}
	connection, err := grpc.NewClient(strings.TrimPrefix(url, "grpc://"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100*1024*1024)),
	)
	if err != nil {
		return nil, err
	}

	c := &Client{
		client: NewSearcherClient(connection),
	}

	for _, option := range options {
		option(c)
	}

	if err := c.loadCapabilities(); err != nil {
		connection.Close()
		return nil, fmt.Errorf("custom search capabilities: %w", err)
	}

	return c, nil
}

func (c *Client) loadCapabilities() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := c.client.Capabilities(ctx, &CapabilitiesRequest{})
	if status.Code(err) == codes.Unimplemented {
		return nil
	}
	if err != nil {
		return err
	}
	if response == nil {
		return errors.New("empty capabilities response")
	}
	capabilities := searcher.Capabilities{DateFilters: response.DateFilters}
	names := make(map[string]bool)
	for _, category := range response.Categories {
		if category == nil || strings.TrimSpace(category.Name) == "" {
			return errors.New("empty category name")
		}
		if names[category.Name] {
			return fmt.Errorf("duplicate category %q", category.Name)
		}
		names[category.Name] = true
		capabilities.Categories = append(capabilities.Categories, searcher.Category{Name: category.Name, Description: category.Description})
	}
	c.capabilities = capabilities
	return nil
}

func (c *Client) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("custom search: query is required")
	}
	settings := searcher.SearchOptions{}
	if options != nil {
		settings = *options
	}
	if settings.Limit != nil && (*settings.Limit < 1 || int64(*settings.Limit) > math.MaxInt32) {
		return nil, errors.New("custom search: limit must be between 1 and 2147483647")
	}
	if (settings.Since != nil || settings.Until != nil) && !c.capabilities.DateFilters {
		return nil, errors.New("custom search: service does not support publication date filters; omit since and until")
	}
	if settings.Since != nil && settings.Until != nil && !settings.Since.Before(*settings.Until) {
		return nil, errors.New("custom search: since must be before until")
	}

	if settings.Category == "" {
		settings.Category = c.category
	}

	if settings.Location == "" {
		settings.Location = c.location
	}

	req := &SearchRequest{
		Query: query,
	}
	if settings.Since != nil {
		req.Since = timestamppb.New(*settings.Since)
		if err := req.Since.CheckValid(); err != nil {
			return nil, fmt.Errorf("custom search: invalid since: %w", err)
		}
	}
	if settings.Until != nil {
		req.Until = timestamppb.New(*settings.Until)
		if err := req.Until.CheckValid(); err != nil {
			return nil, fmt.Errorf("custom search: invalid until: %w", err)
		}
	}

	if settings.Category != "" {
		req.Category = &settings.Category
	}

	if settings.Location != "" {
		req.Location = &settings.Location
	}

	if settings.Limit != nil {
		val := int32(*settings.Limit)
		req.Limit = &val
	}

	if len(settings.Include) > 0 {
		req.Include = settings.Include
	}

	if len(settings.Exclude) > 0 {
		req.Exclude = settings.Exclude
	}

	resp, err := c.client.Search(ctx, req)

	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("custom search: empty response")
	}

	results := []searcher.Result{}

	for _, r := range resp.Results {
		if r == nil {
			continue
		}
		result := searcher.Result{
			Source: r.Source,

			Title:   r.Title,
			Content: r.Content,

			Metadata: r.Metadata,
		}
		if published, err := time.Parse(time.RFC3339, r.Metadata["published"]); err == nil {
			result.Timestamp = &published
		}
		results = append(results, result)
	}

	return results, nil
}

func (c *Client) Capabilities() searcher.Capabilities {
	result := c.capabilities
	result.Categories = slices.Clone(result.Categories)
	return result
}
