package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
)

type ExtractionService struct {
	Options []RequestOption
}

func NewExtractionService(opts ...RequestOption) ExtractionService {
	return ExtractionService{
		Options: opts,
	}
}

type Extraction struct {
	Text string `json:"text"`

	Content     []byte
	ContentType string
}

type ExtractionRequest struct {
	Model string

	Name   string
	Reader io.Reader

	Schema *Schema
}

func (r *ExtractionService) New(ctx context.Context, input ExtractionRequest, opts ...RequestOption) (*Extraction, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	var data bytes.Buffer
	w := multipart.NewWriter(&data)

	if input.Model != "" {
		w.WriteField("model", input.Model)
	}

	if input.Schema != nil {
		properties, err := json.Marshal(input.Schema.Properties)

		if err != nil {
			return nil, err
		}

		w.WriteField("schema", string(properties))
	}

	if err := writeFormFile(w, "file", input.Name, input.Reader); err != nil {
		return nil, err
	}

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint(c.URL, "/v1/extract"), &data)
	req.Header.Set("Content-Type", w.FormDataContentType())

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.Client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	content, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	result := &Extraction{
		Text:        string(content),
		Content:     content,
		ContentType: resp.Header.Get("Content-Type"),
	}

	return result, nil
}
