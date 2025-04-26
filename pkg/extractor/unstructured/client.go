package unstructured

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ extractor.Provider = &Client{}

type Client struct {
	client *http.Client

	url   string
	token string

	strategy Strategy
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" {
		url = "https://api.unstructured.io/general/v0/general"
	}

	c := &Client{
		client: http.DefaultClient,

		url: url,

		strategy: StrategyFast,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Extract(ctx context.Context, input extractor.Input, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	if input.File == nil {
		return nil, extractor.ErrUnsupported
	}

	file := *input.File

	if !isSupported(file) {
		return nil, extractor.ErrUnsupported
	}

	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	w.WriteField("strategy", string(c.strategy))
	w.WriteField("include_page_breaks", "true")

	f, err := w.CreateFormFile("files", file.Name)

	if err != nil {
		return nil, err
	}

	if _, err := f.Write(file.Content); err != nil {
		return nil, err
	}

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", c.url, &b)
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

	var builder strings.Builder

	for _, e := range elements {
		builder.WriteString(e.Text)
		builder.WriteString("\n")
	}

	text := strings.TrimSpace(builder.String())

	return &extractor.Document{
		Content:     []byte(text),
		ContentType: "text/plain",
	}, nil
}

func isSupported(file provider.File) bool {
	if file.Name != "" {
		ext := strings.ToLower(path.Ext(file.Name))

		if slices.Contains(SupportedExtensions, ext) {
			return true
		}
	}

	if file.ContentType != "" {
		if slices.Contains(SupportedMimeTypes, file.ContentType) {
			return true
		}
	}

	return false
}

func convertError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	if len(data) == 0 {
		return errors.New(http.StatusText(resp.StatusCode))
	}

	return errors.New(string(data))
}
