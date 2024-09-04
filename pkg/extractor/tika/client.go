package tika

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/adrianliechti/llama/pkg/extractor"
	"github.com/adrianliechti/llama/pkg/text"
)

var _ extractor.Provider = &Client{}

type Client struct {
	*Config
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" {
		return nil, errors.New("invalid url")
	}

	c := &Config{
		client: http.DefaultClient,

		chunkSize:    4000,
		chunkOverlap: 200,
	}

	for _, option := range options {
		option(c)
	}

	return &Client{
		Config: c,
	}, nil
}

func (c *Client) Extract(ctx context.Context, input extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = &extractor.ExtractOptions{}
	}

	if !isSupported(input) {
		return nil, extractor.ErrUnsupported
	}

	url, _ := url.JoinPath(c.url, "/tika/text")
	req, _ := http.NewRequestWithContext(ctx, "PUT", url, input.Content)

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var response TikaResponse

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	result := extractor.Document{
		Name: input.Name,
	}

	content := text.Normalize(response.Content)

	splitter := text.NewSplitter()
	splitter.ChunkSize = c.chunkSize
	splitter.ChunkOverlap = c.chunkOverlap

	blocks := splitter.Split(content)

	for i, b := range blocks {
		block := extractor.Block{
			ID:      fmt.Sprintf("%s#%d", input.Name, i+1),
			Content: b,
		}

		result.Blocks = append(result.Blocks, block)
	}

	return &result, nil
}

func isSupported(input extractor.File) bool {
	ext := strings.ToLower(path.Ext(input.Name))
	return slices.Contains(SupportedExtensions, ext)
}

func convertError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	if len(data) == 0 {
		return errors.New(http.StatusText(resp.StatusCode))
	}

	return errors.New(string(data))
}
