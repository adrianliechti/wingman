package opa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/policy"
)

type Client struct {
	client *http.Client

	url string
}

type Option func(*Client)

func WithClient(client *http.Client) Option {
	return func(c *Client) {
		c.client = client
	}
}

func NewClient(url string, opts ...Option) (*Client, error) {
	if url == "" {
		return nil, errors.New("invalid url")
	}

	c := &Client{
		client: http.DefaultClient,

		url: strings.TrimRight(url, "/") + "/v1/data/wingman/allow",
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

func (c *Client) Verify(ctx context.Context, resource policy.Resource, id string, action policy.Action) error {
	body, err := json.Marshal(map[string]any{
		"input": evalInput(ctx, resource, id, action),
	})

	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))

	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New("unable to query policy: " + resp.Status)
	}

	var result struct {
		Result *bool `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if result.Result == nil || !*result.Result {
		return policy.ErrAccessDenied
	}

	return nil
}
