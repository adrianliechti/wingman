package openai

import (
	"encoding/base64"
	"io"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleImageEdit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	model := r.FormValue("model")
	prompt := r.FormValue("prompt")

	file, header, err := r.FormFile("image")

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	defer file.Close()

	data, err := io.ReadAll(file)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	renderer, err := h.Renderer(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &provider.RenderOptions{
		Images: []provider.File{
			{
				Name: header.Filename,

				Content:     data,
				ContentType: header.Header.Get("Content-Type"),
			},
		},
	}

	image, err := renderer.Render(r.Context(), prompt, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := ImageList{}

	if r.FormValue("response_format") == "url" {
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
