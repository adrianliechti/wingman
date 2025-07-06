package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ extractor.Provider = &Client{}

type Client struct {
	client *http.Client

	url   string
	token string
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" {
		return nil, errors.New("invalid url")
	}

	c := &Client{
		client: http.DefaultClient,

		url: url,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Extract(ctx context.Context, input extractor.Input, options *extractor.ExtractOptions) (*provider.File, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	if !isSupported(input) {
		return nil, extractor.ErrUnsupported
	}

	if options.Format != nil {
		if *options.Format != extractor.FormatText {
			return nil, extractor.ErrUnsupported
		}
	}

	content := bytes.NewReader(input.File.Content)

	u, _ := url.Parse(strings.TrimRight(c.url, "/") + "/documentintelligence/documentModels/prebuilt-layout:analyze")

	query := u.Query()
	query.Set("api-version", "2024-11-30")
	query.Set("outputContentFormat", "markdown")

	u.RawQuery = query.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), content)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Ocp-Apim-Subscription-Key", c.token)

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return nil, convertError(resp)
	}

	operationURL := resp.Header.Get("Operation-Location")

	if operationURL == "" {
		return nil, errors.New("missing operation location")
	}

	var operation AnalyzeOperation

	for {
		req, _ := http.NewRequestWithContext(ctx, "GET", operationURL, nil)
		req.Header.Set("Ocp-Apim-Subscription-Key", c.token)

		resp, err := c.client.Do(req)

		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, convertError(resp)
		}

		if err := json.NewDecoder(resp.Body).Decode(&operation); err != nil {
			return nil, err
		}

		if operation.Status == OperationStatusRunning || operation.Status == OperationStatusNotStarted {
			time.Sleep(5 * time.Second)
			continue
		}

		if operation.Status != OperationStatusSucceeded {
			return nil, errors.New("operation " + string(operation.Status))
		}

		text := strings.TrimSpace(operation.Result.Content)

		return &provider.File{
			Content:     []byte(text),
			ContentType: "text/plain",
		}, nil
	}
}

func isSupported(input extractor.Input) bool {
	if input.File == nil {
		return false
	}

	if input.File.Name != "" {
		ext := strings.ToLower(path.Ext(input.File.Name))

		if slices.Contains(SupportedExtensions, ext) {
			return true
		}
	}

	if input.File.ContentType != "" {
		if slices.Contains(SupportedMimeTypes, input.File.ContentType) {
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
