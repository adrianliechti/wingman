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

func (c *Client) Extract(ctx context.Context, file extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	if !isSupported(file) {
		return nil, extractor.ErrUnsupported
	}

	//model := "prebuilt-read"
	model := "prebuilt-layout"

	content := bytes.NewReader(file.Content)

	u, _ := url.Parse(strings.TrimRight(c.url, "/") + "/documentintelligence/documentModels/" + model + ":analyze")

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

		// data, _ := io.ReadAll(resp.Body)
		// resp.Body = io.NopCloser(bytes.NewReader(data))

		// println(string(data))

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
