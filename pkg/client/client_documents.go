package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/adrianliechti/wingman/server/index"
)

type Document = index.Document
type DocumentPage = index.Page[Document]

type DocumentService struct {
	Options []RequestOption
}

func NewDocumentService(opts ...RequestOption) DocumentService {
	return DocumentService{
		Options: opts,
	}
}

func (r *DocumentService) New(ctx context.Context, index string, input []Document, opts ...RequestOption) ([]Document, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	var data bytes.Buffer

	if err := json.NewEncoder(&data).Encode(input); err != nil {
		return nil, err
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", c.URL+"/v1/index/"+index, &data)
	req.Header.Set("Content-Type", "application/json")

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.Client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return nil, errors.New(resp.Status)
	}

	return input, nil
}

func (r *DocumentService) List(ctx context.Context, index string, opts ...RequestOption) ([]Document, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	var cursor string

	var items []Document

	for {
		url := c.URL + "/v1/index/" + index

		if cursor != "" {
			url += "?cursor=" + cursor
		}

		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

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

		var page DocumentPage

		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			return nil, err
		}

		items = append(items, page.Items...)

		if page.Cursor == "" {
			break
		}

		cursor = page.Cursor
	}

	return items, nil
}

func (r *DocumentService) Delete(ctx context.Context, index string, ids []string, opts ...RequestOption) error {
	c := newRequestConfig(append(r.Options, opts...)...)

	var body bytes.Buffer

	if err := json.NewEncoder(&body).Encode(ids); err != nil {
		return err
	}

	req, _ := http.NewRequestWithContext(ctx, "DELETE", c.URL+"/v1/index/"+index, &body)
	req.Header.Set("Content-Type", "application/json")

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.Client.Do(req)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return errors.New(resp.Status)
	}

	return nil
}
