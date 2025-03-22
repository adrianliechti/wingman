package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type SummaryService struct {
	Options []RequestOption
}

func NewSummaryService(opts ...RequestOption) SummaryService {
	return SummaryService{
		Options: opts,
	}
}

type SummaryRequest struct {
	Content string `json:"content"`
}

type Summary struct {
	Text string `json:"content"`
}

func (r *SummaryService) New(ctx context.Context, body SummaryRequest, opts ...RequestOption) (*Summary, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	var data bytes.Buffer

	if err := json.NewEncoder(&data).Encode(body); err != nil {
		return nil, err
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", c.URL+"/v1/summarize", &data)
	req.Header.Set("Content-Type", "application/json")

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.Client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}

	result, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	return &Summary{
		Text: string(result),
	}, nil
}
