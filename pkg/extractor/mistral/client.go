package mistral

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
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
	if c.model == "" {
		return nil, errors.New("mistral: missing OCR model")
	}

	return c, nil
}

func (c *Client) Extract(ctx context.Context, file extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	mediaType, _, _ := mime.ParseMediaType(strings.TrimSpace(file.ContentType))
	mediaType = strings.ToLower(mediaType)
	if !slices.Contains(SupportedMimeTypes, mediaType) {
		mediaType = supportedFormats[strings.ToLower(filepath.Ext(file.Name))]
	}
	if mediaType == "" {
		return nil, extractor.ErrUnsupported
	}

	dataurl := "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(file.Content)
	document := map[string]any{
		"type":         "document_url",
		"document_url": dataurl,
	}
	if file.Name != "" {
		document["document_name"] = file.Name
	}
	if strings.HasPrefix(mediaType, "image/") {
		document = map[string]any{
			"type":      "image_url",
			"image_url": dataurl,
		}
	}

	body := map[string]any{
		"model": c.model,

		"document": document,
	}

	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.url, "/")+"/ocr", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
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

func (c *Client) Capabilities() extractor.Capabilities {
	return extractor.Capabilities{
		MediaTypes: slices.Clone(SupportedMimeTypes),
		Extensions: slices.Clone(SupportedExtensions),
	}
}

func convertError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	if len(data) == 0 {
		return errors.New(http.StatusText(resp.StatusCode))
	}

	return errors.New(string(data))
}
