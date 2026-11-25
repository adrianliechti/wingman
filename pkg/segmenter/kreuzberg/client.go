package kreuzberg

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/segmenter"
)

var _ segmenter.Provider = &Client{}

type Client struct {
	client *http.Client

	url   string
	token string
}

func New(url string, options ...Option) (*Client, error) {
	c := &Client{
		client: http.DefaultClient,

		url: url,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Segment(ctx context.Context, input string, options *segmenter.SegmentOptions) ([]segmenter.Segment, error) {
	if options == nil {
		options = new(segmenter.SegmentOptions)
	}

	segmentLength := 2000
	if options.SegmentLength != nil {
		segmentLength = *options.SegmentLength
	}

	u, err := url.Parse(strings.TrimRight(c.url, "/") + "/extract")
	if err != nil {
		return nil, err
	}

	query := u.Query()

	query.Set("chunk_content", "true")
	query.Set("max_chars", fmt.Sprintf("%d", segmentLength))

	if options.SegmentOverlap != nil {
		query.Set("max_overlap", fmt.Sprintf("%d", *options.SegmentOverlap))
	}

	u.RawQuery = query.Encode()

	req, err := provider.NewMultipartRequest(ctx, u.String(), provider.MultipartPart{
		Header: textproto.MIMEHeader{
			"Content-Disposition": {multipart.FileContentDisposition("files", "content.txt")},
			"Content-Type":        {"text/plain"},
		},
		Content: strings.NewReader(input),
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, convertError(resp)
	}

	var results []ExtractionResult

	if err := json.UnmarshalRead(resp.Body, &results); err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, errors.New("kreuzberg returned no extraction results")
	}

	result := []segmenter.Segment{}

	for _, r := range results[0].Chunks {
		result = append(result, segmenter.Segment{
			Text: r,
		})
	}

	return result, nil
}

func convertError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	if len(data) == 0 {
		return errors.New(http.StatusText(resp.StatusCode))
	}

	return errors.New(string(data))
}
