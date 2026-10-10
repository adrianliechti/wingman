package custom

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/segmenter"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ segmenter.Provider = (*Client)(nil)
)

type Client struct {
	url    string
	client SegmenterClient
}

func New(url string, options ...Option) (*Client, error) {
	if !strings.HasPrefix(url, "grpc://") || strings.TrimSpace(strings.TrimPrefix(url, "grpc://")) == "" {
		return nil, errors.New("invalid url")
	}

	c := &Client{
		url: url,
	}

	for _, option := range options {
		option(c)
	}

	client, err := grpc.NewClient(strings.TrimPrefix(c.url, "grpc://"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100*1024*1024)), // 100MB max receive message size
	)

	if err != nil {
		return nil, err
	}

	c.client = NewSegmenterClient(client)

	return c, nil
}

func (c *Client) Segment(ctx context.Context, text string, options *segmenter.SegmentOptions) ([]segmenter.Segment, error) {
	if options == nil {
		options = new(segmenter.SegmentOptions)
	}

	req := &SegmentRequest{
		File: &File{
			Name: options.FileName,

			Content:     []byte(text),
			ContentType: "text/plain",
		},
	}

	if options.SegmentLength != nil {
		val := int32(*options.SegmentLength)
		req.SegmentLength = &val
	}

	if options.SegmentOverlap != nil {
		val := int32(*options.SegmentOverlap)
		req.SegmentOverlap = &val
	}

	resp, err := c.client.Segment(ctx, req)

	if err != nil {
		return nil, err
	}

	var result []segmenter.Segment

	for _, s := range resp.Segments {
		segment := segmenter.Segment{
			Text: s.Text,
		}

		result = append(result, segment)
	}

	return result, nil
}
