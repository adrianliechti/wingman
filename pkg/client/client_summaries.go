package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
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

type Summary struct {
	Text string `json:"content"`
}

type SummaryRequest struct {
	Name   string
	Reader io.Reader
}

func (r *SummaryService) New(ctx context.Context, input SummaryRequest, opts ...RequestOption) (*Summary, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	var data bytes.Buffer
	w := multipart.NewWriter(&data)

	file, err := w.CreateFormFile("file", input.Name)

	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(file, input.Reader); err != nil {
		return nil, err
	}

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", c.URL+"/v1/summarize", &data)
	req.Header.Set("Content-Type", w.FormDataContentType())

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
