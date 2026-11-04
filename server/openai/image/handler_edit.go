package image

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/files"
	"github.com/adrianliechti/wingman/server/openai/shared"
)

func (h *Handler) handleImageEdit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	model := r.FormValue("model")
	prompt := r.FormValue("prompt")

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, shared.ModelNotFound(model))
		return
	}

	renderer, err := h.Renderer(model)

	if err != nil {
		writeError(w, http.StatusNotFound, shared.ModelNotFound(model))
		return
	}

	var images []provider.File

	if r.MultipartForm != nil && r.MultipartForm.File != nil {
		for _, field := range []string{"image", "image[]"} {
			for _, header := range r.MultipartForm.File[field] {
				file, err := files.FromMultipart(header)

				if err != nil {
					writeError(w, http.StatusBadRequest, err)
					return
				}

				images = append(images, *file)
			}
		}
	}

	if len(images) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no image provided"))
		return
	}

	mask, err := formFile(r, "mask")

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	markup, err := formFile(r, "markup")

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &provider.RenderOptions{
		Images: images,
		Mask:   mask,
		Markup: markup,

		Aspect:     parseAspect(r.FormValue("size"), r.FormValue("aspect_ratio")),
		Quality:    parseQuality(r.FormValue("quality")),
		Resolution: provider.ParseResolution(r.FormValue("resolution")),

		Background: provider.ParseBackground(r.FormValue("background")),
		Format:     provider.ParseFormat(r.FormValue("output_format")),
	}

	image, err := renderer.Render(r.Context(), prompt, options)

	if err != nil {
		writeError(w, provider.CodeFromError(err, http.StatusBadRequest), err)
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

// formFile returns the first file uploaded under the given form field, or
// nil when the field is absent.
func formFile(r *http.Request, field string) (*provider.File, error) {
	if r.MultipartForm == nil || len(r.MultipartForm.File[field]) == 0 {
		return nil, nil
	}

	return files.FromMultipart(r.MultipartForm.File[field][0])
}
