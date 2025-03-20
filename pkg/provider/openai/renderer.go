package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/google/uuid"

	"github.com/openai/openai-go"
)

var _ provider.Renderer = (*Renderer)(nil)

type Renderer struct {
	*Config
	images openai.ImageService
}

func NewRenderer(url, model string, options ...Option) (*Renderer, error) {
	cfg := &Config{
		url:   url,
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Renderer{
		Config: cfg,
		images: openai.NewImageService(cfg.Options()...),
	}, nil
}

func (r *Renderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Image, error) {
	if options == nil {
		options = new(provider.RenderOptions)
	}

	req, err := r.convertImageGenerateRequest(input, options)

	if err != nil {
		return nil, err
	}

	image, err := r.images.Generate(ctx, *req)

	if err != nil {
		return nil, convertError(err)
	}

	data, err := base64.StdEncoding.DecodeString(image.Data[0].B64JSON)

	if err != nil {
		return nil, errors.New("invalid image data")
	}

	id := uuid.NewString()

	return &provider.Image{
		ID:   id,
		Name: id + ".png",

		Reader: io.NopCloser(bytes.NewReader(data)),
	}, nil
}

func (r *Renderer) convertImageGenerateRequest(input string, options *provider.RenderOptions) (*openai.ImageGenerateParams, error) {
	if options == nil {
		options = new(provider.RenderOptions)
	}

	req := &openai.ImageGenerateParams{
		Model:  r.model,
		Prompt: input,

		ResponseFormat: openai.ImageGenerateParamsResponseFormatB64JSON,
	}

	if options.Style == provider.ImageStyleNatural {
		req.Style = openai.ImageGenerateParamsStyleNatural
	}

	if options.Style == provider.ImageStyleVivid {
		req.Style = openai.ImageGenerateParamsStyleVivid
	}

	return req, nil
}
