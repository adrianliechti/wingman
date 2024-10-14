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

	"github.com/adrianliechti/llama/pkg/segmenter"
)

var _ segmenter.Provider = &Client{}

type Client struct {
	client *http.Client

	url   string
	token string
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" {
		url = "https://api.unstructured.io"
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

func (c *Client) Segment(ctx context.Context, input segmenter.File, options *segmenter.SegmentOptions) ([]segmenter.Segment, error) {
	if options == nil {
		options = new(segmenter.SegmentOptions)
	}

	url, _ := url.JoinPath(c.url, "/general/v0/general")

	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	w.WriteField("strategy", "fast")
	w.WriteField("chunking_strategy", "by_title")

	if options.SegmentLength != nil {
		w.WriteField("max_characters", fmt.Sprintf("%d", *options.SegmentLength))
	}

	if options.SegmentOverlap != nil {
		w.WriteField("overlap", fmt.Sprintf("%d", *options.SegmentOverlap))
	}

	name := input.Name

	if name == "" {
		name = "file.txt"
	}

	file, err := w.CreateFormFile("files", name)

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

	var segments []segmenter.Segment

	for _, chunk := range elements {
		segment := segmenter.Segment{
			Content: chunk.Text,
		}

		segments = append(segments, segment)
	}

	return segments, nil
}

func convertError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	if len(data) == 0 {
		return errors.New(http.StatusText(resp.StatusCode))
	}

	return errors.New(string(data))
}
