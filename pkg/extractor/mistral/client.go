package mistral

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

var _ extractor.Provider = &Client{}

type Client struct {
	client *http.Client

	url   string
	token string

	model string
}

func New(options ...Option) (*Client, error) {
	c := &Client{
		client: http.DefaultClient,

		url: "https://api.mistral.ai/v1/",

		model: "mistral-ocr-latest",
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Extract(ctx context.Context, file extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	if !isSupported(file) {
		return nil, extractor.ErrUnsupported
	}

	dataurl := "data:" + file.ContentType + ";base64," + base64.StdEncoding.EncodeToString(file.Content)

	body := map[string]any{
		"model": c.model,

		"document": map[string]any{
			"type":          "document_url",
			"document_name": "test.pdf",
			"document_url":  dataurl,
		},
	}

	data, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.url, "/")+"/ocr", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var response Response

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return convertResult(&response), nil
}

func convertResult(response *Response) *extractor.Document {
	result := &extractor.Document{
		Pages:  []extractor.Page{},
		Blocks: []extractor.Block{},
	}

	var builder strings.Builder

	for _, p := range response.Pages {
		page := extractor.Page{
			Page: p.Index + 1,
		}

		if p.Dimensions != nil {
			page.Unit = "pixel"
			page.Width = float64(p.Dimensions.Width)
			page.Height = float64(p.Dimensions.Height)
		}

		if p.Markdown != "" {
			result.Blocks = append(result.Blocks, extractor.Block{
				Page: page.Page,
				Text: p.Markdown,
			})

			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}

			builder.WriteString(p.Markdown)
		}

		result.Pages = append(result.Pages, page)
	}

	result.Text = strings.TrimSpace(builder.String())

	return result
}

func isSupported(file extractor.File) bool {
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
