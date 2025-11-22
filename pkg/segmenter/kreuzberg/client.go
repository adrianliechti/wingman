package kreuzberg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

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

	if options.SegmentLength == nil {
		options.SegmentLength = Ptr(2000)
	}

	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", multipart.FileContentDisposition("files", "content.txt"))
	h.Set("Content-Type", "text/plain")

	f, _ := w.CreatePart(h)
	f.Write([]byte(input))

	w.Close()

	u, _ := url.Parse(strings.TrimRight(c.url, "/") + "/extract")

	query := u.Query()

	query.Set("chunk_content", "true")

	if options.SegmentLength != nil {
		query.Set("max_chars", fmt.Sprintf("%d", *options.SegmentLength))
	}

	if options.SegmentOverlap != nil {
		query.Set("max_overlap", fmt.Sprintf("%d", *options.SegmentOverlap))
	}

	u.RawQuery = query.Encode()

	req, _ := http.NewRequestWithContext(ctx, "POST", u.String(), &b)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, convertError(resp)
	}

	var results []ExtractionResult

	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, err
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

func Ptr[T any](v T) *T {
	return &v
}
