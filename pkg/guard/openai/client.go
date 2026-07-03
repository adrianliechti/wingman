package openai

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"

	"github.com/adrianliechti/wingman/pkg/guard"
)

var (
	_ guard.Provider = (*Client)(nil)
)

type Client struct {
	client *http.Client

	url   string
	token string
	model string
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" {
		url = "https://api.openai.com/v1"
	}

	c := &Client{
		client: http.DefaultClient,

		url:   url,
		model: "omni-moderation-latest",
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Check(ctx context.Context, text string, options *guard.CheckOptions) (*guard.Result, error) {
	if options == nil {
		options = new(guard.CheckOptions)
	}

	type bodyType struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}

	body := bodyType{
		Model: c.model,
		Input: text,
	}

	u, _ := url.JoinPath(c.url, "/moderations")
	r, _ := http.NewRequestWithContext(ctx, "POST", u, jsonReader(body))
	r.Header.Add("Authorization", "Bearer "+c.token)
	r.Header.Add("Content-Type", "application/json")

	resp, err := c.client.Do(r)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	type resultType struct {
		Results []struct {
			Flagged bool `json:"flagged"`

			Categories     map[string]bool    `json:"categories"`
			CategoryScores map[string]float64 `json:"category_scores"`
		} `json:"results"`
	}

	var result resultType

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Results) == 0 {
		return nil, errors.New("unable to check content")
	}

	moderation := result.Results[0]

	value := &guard.Result{
		Flagged: moderation.Flagged,
	}

	for name, flagged := range moderation.Categories {
		if !flagged {
			continue
		}

		value.Categories = append(value.Categories, guard.Category{
			Name:  name,
			Score: moderation.CategoryScores[name],
		})
	}

	slices.SortFunc(value.Categories, func(i, j guard.Category) int {
		return cmp.Compare(j.Score, i.Score)
	})

	return value, nil
}
