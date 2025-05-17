package openai

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleImageGeneration(w http.ResponseWriter, r *http.Request) {
	var req ImageCreateRequest

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

	result := ImageList{}

	if req.ResponseFormat == "url" {
		result.Images = []Image{
			{
				URL: "data:" + image.ContentType + ";base64," + base64.StdEncoding.EncodeToString(image.Content),
			},
		}
	} else {
		result.Images = []Image{
			{
				B64JSON: base64.StdEncoding.EncodeToString(image.Content),
			},
		}

	}

	writeJson(w, result)
}
