package xai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/google/uuid"
)

var _ provider.Renderer = (*Renderer)(nil)

type Renderer struct {
	*Config
}

func NewRenderer(model string, options ...Option) (*Renderer, error) {
	cfg := &Config{
		url:   "https://api.x.ai/v1",
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	if cfg.client == nil {
		cfg.client = http.DefaultClient
	}

	return &Renderer{
		Config: cfg,
	}, nil
}

func (r *Renderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	if options == nil {
		options = new(provider.RenderOptions)
	}

	result := &provider.Rendering{
		ID:    uuid.NewString(),
		Model: r.model,
	}

	if len(options.Images) == 0 {
		data, err := r.generate(ctx, input)

		if err != nil {
			return nil, err
		}

		result.Content = data
		result.ContentType = "image/png"
	} else {
		data, err := r.edit(ctx, input, options.Images)

		if err != nil {
			return nil, err
		}

		result.Content = data
		result.ContentType = "image/png"
	}

	return result, nil
}

type generateRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

type imageResponse struct {
	Data []imageData `json:"data"`
}

type imageData struct {
	URL     string `json:"url,omitempty"`
	B64JSON string `json:"b64_json,omitempty"`
}

func (r *Renderer) generate(ctx context.Context, prompt string) ([]byte, error) {
	body := generateRequest{
		Model:          r.model,
		Prompt:         prompt,
		N:              1,
		ResponseFormat: "b64_json",
	}

	var result imageResponse

	if err := r.do(ctx, r.url+"/images/generations", body, &result); err != nil {
		return nil, err
	}

	if len(result.Data) == 0 {
		return nil, errors.New("no image data in response")
	}

	return r.getData(ctx, result.Data[0])
}

type editRequest struct {
	Model          string     `json:"model"`
	Prompt         string     `json:"prompt"`
	Image          imageInput `json:"image"`
	ResponseFormat string     `json:"response_format,omitempty"`
}

type imageInput struct {
	URL  string `json:"url"`
	Type string `json:"type"`
}

func (r *Renderer) edit(ctx context.Context, prompt string, images []provider.File) ([]byte, error) {
	img := images[0]

	contentType := img.ContentType

	if contentType == "" {
		contentType = "image/png"
	}

	dataURL := fmt.Sprintf("data:%s;base64,%s", contentType, base64.StdEncoding.EncodeToString(img.Content))

	body := editRequest{
		Model:  r.model,
		Prompt: prompt,
		Image: imageInput{
			URL:  dataURL,
			Type: "image_url",
		},
		ResponseFormat: "b64_json",
	}

	var result imageResponse

	if err := r.do(ctx, r.url+"/images/edits", body, &result); err != nil {
		return nil, err
	}

	if len(result.Data) == 0 {
		return nil, errors.New("no image data in response")
	}

	return r.getData(ctx, result.Data[0])
}

func (r *Renderer) do(ctx context.Context, url string, body any, result any) error {
	data, err := json.Marshal(body)

	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))

	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}

	resp, err := r.client.Do(req)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return &provider.ProviderError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
		}
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

func (r *Renderer) getData(ctx context.Context, image imageData) ([]byte, error) {
	if image.B64JSON != "" {
		return base64.StdEncoding.DecodeString(image.B64JSON)
	}

	if image.URL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, image.URL, nil)

		if err != nil {
			return nil, err
		}

		resp, err := r.client.Do(req)

		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		return io.ReadAll(resp.Body)
	}

	return nil, errors.New("no image data in response")
}
