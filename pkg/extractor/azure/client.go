package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

var _ extractor.Provider = &Client{}

type Client struct {
	client *http.Client

	url   string
	token string
}

func New(endpoint string, options ...Option) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, errors.New("azure: invalid http(s) URL")
	}

	c := &Client{
		client: http.DefaultClient,

		url: endpoint,
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

	if !c.Capabilities().MaySupport(file) {
		return nil, extractor.ErrUnsupported
	}

	model := "prebuilt-layout"

	content := bytes.NewReader(file.Content)

	u, err := url.Parse(strings.TrimRight(c.url, "/") + "/documentintelligence/documentModels/" + model + ":analyze")
	if err != nil {
		return nil, err
	}

	query := u.Query()
	query.Set("api-version", "2024-11-30")
	query.Set("outputContentFormat", "markdown")

	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), content)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Ocp-Apim-Subscription-Key", c.token)

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusAccepted {
		err := convertError(resp)
		resp.Body.Close()
		return nil, err
	}
	resp.Body.Close()

	operationURL := resp.Header.Get("Operation-Location")

	if operationURL == "" {
		return nil, errors.New("missing operation location")
	}

	for {
		operation, err := c.readOperation(ctx, operationURL)
		if err != nil {
			return nil, err
		}

		if operation.Status == OperationStatusRunning || operation.Status == OperationStatusNotStarted {
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			continue
		}

		if operation.Status != OperationStatusSucceeded {
			return nil, errors.New("operation " + string(operation.Status))
		}

		result := &extractor.Document{
			Text: strings.TrimSpace(operation.Result.Content),

			Pages:  []extractor.Page{},
			Blocks: []extractor.Block{},
		}

		for _, page := range operation.Result.Pages {
			result.Pages = append(result.Pages, extractor.Page{
				Page: page.PageNumber,

				Unit:   page.Unit,
				Width:  page.Width,
				Height: page.Height,
			})

			for _, word := range page.Words {
				block := extractor.Block{
					Text: word.Content,

					Page: page.PageNumber,

					Score:   word.Confidence,
					Polygon: convertPolygon(word.Polygon),
				}

				result.Blocks = append(result.Blocks, block)
			}

			for _, selection := range page.SelectionMarks {
				var state extractor.BlockState

				if strings.EqualFold(selection.State, "selected") {
					state = extractor.BlockStateChecked
				}

				if strings.EqualFold(selection.State, "unselected") {
					state = extractor.BlockStateUnchecked
				}

				if state == "" {
					continue
				}

				block := extractor.Block{
					Page: page.PageNumber,

					State: state,

					Score:   selection.Confidence,
					Polygon: convertPolygon(selection.Polygon),
				}

				result.Blocks = append(result.Blocks, block)
			}
		}

		return result, nil
	}
}

func (c *Client) readOperation(ctx context.Context, operationURL string) (*AnalyzeOperation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, operationURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}
	var operation AnalyzeOperation
	if err := json.NewDecoder(resp.Body).Decode(&operation); err != nil {
		return nil, err
	}
	return &operation, nil
}

func (c *Client) Capabilities() extractor.Capabilities {
	return extractor.Capabilities{
		MediaTypes: slices.Clone(SupportedMimeTypes),
		Extensions: slices.Clone(SupportedExtensions),
	}
}

func convertPolygon(polygon []float64) [][2]float64 {
	if len(polygon)%2 != 0 {
		return nil
	}

	result := make([][2]float64, 0, len(polygon)/2)

	for i := 0; i < len(polygon); i += 2 {
		result = append(result, [2]float64{
			polygon[i],
			polygon[i+1],
		})
	}

	return result
}

func convertError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	if len(data) == 0 {
		return errors.New(http.StatusText(resp.StatusCode))
	}

	return errors.New(string(data))
}
