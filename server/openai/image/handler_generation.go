package image

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/openai/openai-go/v2"
)

func (h *Handler) handleImageGeneration(w http.ResponseWriter, r *http.Request) {
	var req openai.ImageGenerateParams

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	renderer, err := h.Renderer(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &provider.RenderOptions{}

	image, err := renderer.Render(r.Context(), req.Prompt, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := openai.ImagesResponse{
		Created: time.Now().Unix(),
	}

	if req.ResponseFormat == "url" {
		result.Data = []openai.Image{
			{
				URL: "data:" + image.ContentType + ";base64," + base64.StdEncoding.EncodeToString(image.Content),
			},
		}
	} else {
		result.Data = []openai.Image{
			{
				B64JSON: base64.StdEncoding.EncodeToString(image.Content),
			},
		}

	}

	writeJson(w, result)
}
