package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
)

type RenderingService struct {
	Options []RequestOption
}

func NewRenderingService(opts ...RequestOption) RenderingService {
	return RenderingService{
		Options: opts,
	}
}

type Rendering struct {
	Content     []byte
	ContentType string
}

type Image struct {
	Name   string
	Reader io.Reader
}

type RenderingRequest struct {
	Model string
	Input string

	Images []Image
}

func (r *RenderingService) New(ctx context.Context, input RenderingRequest, opts ...RequestOption) (*Rendering, error) {
	cfg := newRequestConfig(append(r.Options, opts...)...)

	var data bytes.Buffer
	w := multipart.NewWriter(&data)

	if input.Model != "" {
		w.WriteField("model", input.Model)
	}

	w.WriteField("input", input.Input)

	for _, img := range input.Images {
		f, err := w.CreateFormFile("image", img.Name)

		if err != nil {
			return nil, err
		}

		if _, err := io.Copy(f, img.Reader); err != nil {
			return nil, err
		}
	}

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL+"/v1/render", &data)
	req.Header.Set("Content-Type", w.FormDataContentType())

	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}

	resp, err := cfg.Client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}

	content, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	return &Rendering{
		Content:     content,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}
