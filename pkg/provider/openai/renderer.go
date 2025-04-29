package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"regexp"
	"strings"

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

func (r *Renderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	if options == nil {
		options = new(provider.RenderOptions)
	}

	result := &provider.Rendering{
		ID: uuid.NewString(),
	}

	if len(options.Images) == 0 {
		image, err := r.images.Generate(ctx, openai.ImageGenerateParams{
			Model:  r.model,
			Prompt: input,
		})

		if err != nil {
			return nil, convertError(err)
		}

		data, err := r.getData(ctx, image.Data[0])

		if err != nil {
			return nil, err
		}

		result.Content = data
		result.ContentType = "image/png"
	} else {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)

		w.WriteField("model", r.model)
		w.WriteField("prompt", input)

		escapeQuotes := func(s string) string {
			return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(s)
		}

		for _, image := range options.Images {
			imageName := image.Name
			imageType := image.ContentType

			if imageName == "" {
				switch image.ContentType {
				case "image/jpeg":
					imageName = "image.jpg"
				case "image/png":
					imageName = "image.png"
				case "image/webp":
					imageName = "image.webp"
				}
			}

			if imageType == "" {
				switch path.Ext(imageName) {
				case ".jpg", ".jpeg", ".jpe":
					imageType = "image/jpeg"
				case ".png":
					imageType = "image/png"
				case ".webp":
					imageType = "image/webp"
				}
			}

			h := textproto.MIMEHeader{}
			h.Set("Content-Type", image.ContentType)
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image[]"; filename="%s"`, escapeQuotes(imageName)))

			writer, _ := w.CreatePart(h)

			if _, err := writer.Write(image.Content); err != nil {
				return nil, err
			}
		}

		w.Close()

		req, _ := http.NewRequest("POST", r.url+"images/edits", &b)
		req.Header.Set("Content-Type", w.FormDataContentType())

		if r.token != "" {
			req.Header.Set("Authorization", "Bearer "+r.token)
		}

		resp, err := r.client.Do(req)

		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to generate image: %s", resp.Status)
		}

		var response openai.ImagesResponse

		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return nil, err
		}

		data, err := r.getData(ctx, response.Data[0])

		if err != nil {
			return nil, err
		}

		result.Content = data
		result.ContentType = "image/png"
	}

	return result, nil
}

func (r *Renderer) getData(ctx context.Context, image openai.Image) ([]byte, error) {
	if image.URL != "" {
		if strings.HasPrefix(image.URL, "data:") {
			re := regexp.MustCompile(`data:([a-zA-Z]+\/[a-zA-Z0-9.+_-]+);base64,\s*(.+)`)

			match := re.FindStringSubmatch(image.URL)

			if len(match) != 3 {
				return nil, fmt.Errorf("invalid data url")
			}

			return base64.StdEncoding.DecodeString(match[2])
		}

		req, err := http.NewRequestWithContext(ctx, "GET", image.URL, nil)

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

	if image.B64JSON != "" {
		return base64.StdEncoding.DecodeString(image.B64JSON)
	}

	return nil, errors.New("invalid image data")
}
