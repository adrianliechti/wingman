package image

import (
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/openai/shared"
)

func (h *Handler) handleImageGeneration(w http.ResponseWriter, r *http.Request) {
	var req ImageCreateRequest

	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, req.Model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, shared.ModelNotFound(req.Model))
		return
	}

	renderer, err := h.Renderer(req.Model)

	if err != nil {
		writeError(w, http.StatusNotFound, shared.ModelNotFound(req.Model))
		return
	}

	options := &provider.RenderOptions{
		Aspect:     parseAspect(req.Size, req.AspectRatio),
		Quality:    parseQuality(req.Quality),
		Resolution: provider.ParseResolution(req.Resolution),
		Complexity: req.Complexity,

		FrameCount: req.FrameCount,

		Background: provider.ParseBackground(req.Background),
		Format:     provider.ParseFormat(req.OutputFormat),
	}
	if req.FrameDurationMS != nil {
		if *req.FrameDurationMS <= 0 || *req.FrameDurationMS > math.MaxInt64/int64(time.Millisecond) {
			writeError(w, http.StatusBadRequest, errors.New("frame_duration_ms must be a positive integer within duration range"))
			return
		}
		options.FrameDuration = new(time.Duration(*req.FrameDurationMS) * time.Millisecond)
	}
	if req.SVG != nil {
		options.SVGModel = req.SVG.Model
	}

	image, err := renderer.Render(r.Context(), req.Prompt, options)

	if err != nil {
		writeError(w, provider.CodeFromError(err, http.StatusBadRequest), err)
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
