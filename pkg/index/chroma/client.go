package chroma

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/adrianliechti/wingman/pkg/index"

	"github.com/google/uuid"
)

var _ index.Provider = &Client{}

type Client struct {
	client *http.Client

	url string

	namespace string

	embedder index.Embedder
	reranker index.Reranker
}

func New(url, namespace string, options ...Option) (*Client, error) {
	c := &Client{
		client: http.DefaultClient,

		url: url,

		namespace: namespace,
	}

	for _, option := range options {
		option(c)
	}

	if c.url == "" {
		return nil, errors.New("url is required")
	}

	if c.embedder == nil {
		return nil, errors.New("embedder is required")
	}

	if c.namespace == "" {
		return nil, errors.New("namespace is required")
	}

	return c, nil
}

func (c *Client) List(ctx context.Context, options *index.ListOptions) (*index.Page[index.Document], error) {
	col, err := c.createCollection(c.namespace)

	if err != nil {
		return nil, err
	}

	u, _ := url.JoinPath(c.url, "/api/v1/collections/"+col.ID+"/get")

	body := map[string]any{}

	resp, err := c.client.Post(u, "application/json", jsonReader(body))

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var result getResult

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	items := make([]index.Document, 0)

	for i := range result.IDs {
		id := result.IDs[i]
		content := result.Documents[i]

		metadata := result.Metadatas[i]

		if metadata == nil {
			metadata = make(map[string]string)
		}

		title := metadata["_title"]
		delete(metadata, "_title")

		source := metadata["_source"]
		delete(metadata, "_source")

		d := index.Document{
			ID: id,

			Title:   title,
			Source:  source,
			Content: content,

			Metadata: metadata,
		}

		items = append(items, d)
	}

	page := index.Page[index.Document]{
		Items: items,
	}

	return &page, nil
}

func (c *Client) Index(ctx context.Context, documents ...index.Document) error {
	if len(documents) == 0 {
		return nil
	}

	col, err := c.createCollection(c.namespace)

	if err != nil {
		return err
	}

	u, _ := url.JoinPath(c.url, "/api/v1/collections/"+col.ID+"/upsert")

	body := embeddings{
		IDs: make([]string, len(documents)),

		Embeddings: make([][]float32, len(documents)),

		Documents: make([]string, len(documents)),
		Metadatas: make([]map[string]string, len(documents)),
	}

	for i, d := range documents {
		if d.ID == "" {
			d.ID = uuid.NewString()
		}

		metadata := d.Metadata

		if metadata == nil {
			metadata = make(map[string]string)
		}

		if len(d.Embedding) == 0 && c.embedder != nil {
			embedding, err := c.embedder.Embed(ctx, []string{d.Content})

			if err != nil {
				return err
			}

			d.Embedding = embedding.Embeddings[0]
		}

		body.IDs[i] = d.ID

		body.Embeddings[i] = d.Embedding

		body.Documents[i] = d.Content
		body.Metadatas[i] = metadata
	}

	resp, err := c.client.Post(u, "application/json", jsonReader(body))

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return convertError(resp)
	}

	return nil
}

func (c *Client) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}

	col, err := c.createCollection(c.namespace)

	if err != nil {
		return err
	}

	u, _ := url.JoinPath(c.url, "/api/v1/collections/"+col.ID+"/delete")

	body := map[string]any{
		"ids": ids,
	}

	resp, err := c.client.Post(u, "application/json", jsonReader(body))

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	return nil
}

func (c *Client) Query(ctx context.Context, query string, options *index.QueryOptions) ([]index.Result, error) {
	if options == nil {
		options = &index.QueryOptions{}
	}

	col, err := c.createCollection(c.namespace)

	if err != nil {
		return nil, err
	}

	embedding, err := c.embedder.Embed(ctx, []string{query})

	if err != nil {
		return nil, err
	}

	u, _ := url.JoinPath(c.url, "/api/v1/collections/"+col.ID+"/query")

	body := map[string]any{
		"query_embeddings": [][]float32{
			embedding.Embeddings[0],
		},

		"include": []string{
			"documents",
			"metadatas",
			"distances",
		},
	}

	if len(options.Filters) > 0 {
		body["where"] = options.Filters
	}

	if options.Limit != nil {
		body["n_results"] = *options.Limit
	}

	resp, err := c.client.Post(u, "application/json", jsonReader(body))

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var result queryResult

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	results := make([]index.Result, 0)

	for i := range result.IDs {
		for j := range result.IDs[i] {
			id := result.IDs[i][j]

			content := result.Documents[i][j]
			metadata := result.Metadatas[i][j]

			score := 1 - result.Distances[i][j]

			if metadata == nil {
				metadata = make(map[string]string)
			}

			title := metadata["_title"]
			delete(metadata, "_title")

			source := metadata["_source"]
			delete(metadata, "_source")

			r := index.Result{
				Score: score,

				Document: index.Document{
					ID: id,

					Title:   title,
					Source:  source,
					Content: content,

					Metadata: metadata,
				},
			}

			results = append(results, r)
		}
	}

	return results, nil
}

func (c *Client) createCollection(name string) (*collection, error) {
	u, _ := url.JoinPath(c.url, "/api/v1/collections")

	body := map[string]any{
		"name":          name,
		"get_or_create": true,

		"metadata": map[string]any{
			"hnsw:space": "cosine",
		},
	}

	resp, err := c.client.Post(u, "application/json", jsonReader(body))

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	var result collection

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func convertError(resp *http.Response) error {
	type resultType struct {
		Errors []errorDetail `json:"detail"`
	}

	var result resultType

	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		var errs []error

		for _, e := range result.Errors {
			errs = append(errs, errors.New(e.Message))
		}

		return errors.Join(errs...)
	}

	return errors.New(http.StatusText(resp.StatusCode))
}

func jsonReader(v any) io.Reader {
	b := new(bytes.Buffer)

	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)

	enc.Encode(v)
	return b
}
