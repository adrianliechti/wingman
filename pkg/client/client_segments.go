package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/adrianliechti/wingman/server/api"
)

type SegmentService struct {
	Options []RequestOption
}

func NewSegmentService(opts ...RequestOption) SegmentService {
	return SegmentService{
		Options: opts,
	}
}

type Segment = api.Segment

type SegmentRequest struct {
	Name   string
	Reader io.Reader

	SegmentLength  *int
	SegmentOverlap *int
}

func (r *SegmentService) New(ctx context.Context, input SegmentRequest, opts ...RequestOption) ([]Segment, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	var data bytes.Buffer
	w := multipart.NewWriter(&data)

	file, err := w.CreateFormFile("file", input.Name)

	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(file, input.Reader); err != nil {
		return nil, err
	}

	if input.SegmentLength != nil {
		w.WriteField("segment_length", fmt.Sprintf("%d", *input.SegmentLength))
	}

	if input.SegmentOverlap != nil {
		w.WriteField("segment_overlap", fmt.Sprintf("%d", *input.SegmentOverlap))
	}

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", c.URL+"/v1/segment", &data)
	req.Header.Set("Content-Type", w.FormDataContentType())

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.Client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}

	var result []Segment

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}
