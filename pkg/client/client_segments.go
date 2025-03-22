package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	Text string `json:"text"`

	SegmentLength  *int `json:"segment_length"`
	SegmentOverlap *int `json:"segment_overlap"`
}

func (r *SegmentService) New(ctx context.Context, input SegmentRequest, opts ...RequestOption) ([]Segment, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	var data bytes.Buffer

	if err := json.NewEncoder(&data).Encode(input); err != nil {
		return nil, err
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", c.URL+"/v1/segment", &data)
	req.Header.Set("Content-Type", "application/json")

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
