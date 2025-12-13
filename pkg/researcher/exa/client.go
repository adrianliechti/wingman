package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/researcher"
)

var _ researcher.Provider = &Client{}

type Client struct {
	url    string
	token  string
	client *http.Client
}

func New(token string, options ...Option) (*Client, error) {
	c := &Client{
		url: "https://api.exa.ai",

		token:  token,
		client: http.DefaultClient,
	}

	for _, option := range options {
		option(c)
	}

	if c.token == "" {
		return nil, errors.New("invalid token")
	}

	return c, nil
}

func (c *Client) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) (*researcher.Result, error) {
	if options == nil {
		options = new(researcher.ResearchOptions)
	}

	model := ResearchModelStandard

	task, err := c.CreateTask(ctx, CreateTaskRequest{
		Instructions: instructions,
		Model:        model,
	})

	if err != nil {
		return nil, err
	}

	for {
		time.Sleep(5 * time.Second)

		result, err := c.Task(ctx, task.ResearchID)

		if err != nil {
			return nil, err
		}

		if result.Status == ResearchStatusPending {
			continue
		}

		if result.Status == ResearchStatusRunning {
			continue
		}

		if result.Status == ResearchStatusFailed {
			return nil, errors.New("research task failed")
		}

		if result.Status == ResearchStatusCanceled {
			return nil, errors.New("research task canceled")
		}

		if result.Status == ResearchStatusCompleted {
			return &researcher.Result{
				Content: result.Output.Content,
			}, nil
		}
	}
}

func (c *Client) CreateTask(ctx context.Context, body CreateTaskRequest) (*TaskResponse, error) {
	url := strings.TrimRight(c.url, "/") + "/research/v1"

	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))

	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.token)

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result TaskResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) Task(ctx context.Context, researchID string) (*TaskResponse, error) {
	url := strings.TrimRight(c.url, "/") + "/research/v1/" + researchID

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)

	if err != nil {
		return nil, err
	}

	req.Header.Set("x-api-key", c.token)

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result TaskResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}
