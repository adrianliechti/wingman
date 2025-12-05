package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"

	"github.com/adrianliechti/wingman/server/api"
)

type SearchService struct {
	Options []RequestOption
}

func NewSearchService(opts ...RequestOption) SearchService {
	return SearchService{
		Options: opts,
	}
}

type SearchResult = api.SearchResult

type SearchRequest struct {
	Query string
}

func (r *SearchService) New(ctx context.Context, input SearchRequest, opts ...RequestOption) ([]SearchResult, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	var data bytes.Buffer
	w := multipart.NewWriter(&data)

	w.WriteField("query", string(input.Query))

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", c.URL+"/v1/search", &data)
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

	var result []SearchResult

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}
