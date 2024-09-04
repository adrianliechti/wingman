package unstructured

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/adrianliechti/llama/pkg/extractor"
)

var _ extractor.Provider = &Client{}

type Client struct {
	*Config
}

func New(options ...Option) (*Client, error) {
	c := &Config{
		client: http.DefaultClient,

		url: "https://api.unstructured.io/general/v0/general",

		chunkSize:     4000,
		chunkOverlap:  500,
		chunkStrategy: "by_title",
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

	url, _ := url.JoinPath(c.url, "/general/v0/general")

	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	w.WriteField("strategy", "auto")

	if c.chunkStrategy != "" {
		w.WriteField("chunking_strategy", c.chunkStrategy)
	}

	if c.chunkSize > 0 {
		w.WriteField("max_characters", fmt.Sprintf("%d", c.chunkSize))
	}

	if c.chunkOverlap > 0 {
		w.WriteField("overlap", fmt.Sprintf("%d", c.chunkOverlap))
	}

	file, err := w.CreateFormFile("files", input.Name)

	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(file, input.Content); err != nil {
		return nil, err
	}

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", url, &b)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var elements []Element

	if err := json.NewDecoder(resp.Body).Decode(&elements); err != nil {
		return nil, err
	}

	result := extractor.Document{
		Name: input.Name,
	}

	if len(elements) > 0 {
		result.Name = elements[0].Metadata.FileName
	}

	for _, e := range elements {
		block := extractor.Block{
			ID:      e.ID,
			Content: e.Text,
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
