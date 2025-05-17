package api

import (
	"net/http"
	"strconv"

	"github.com/adrianliechti/wingman/pkg/segmenter"
)

func (h *Handler) handleSegment(w http.ResponseWriter, r *http.Request) {
	model := valueModel(r)

	p, err := h.Segmenter(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	text, err := h.readText(r)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &segmenter.SegmentOptions{
		SegmentLength:  valueSegmentLength(r),
		SegmentOverlap: valueSegmentOverlap(r),
	}

	segments, err := p.Segment(r.Context(), text, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := make([]Segment, 0)

	for _, s := range segments {
		segment := Segment{
			Text: s.Text,
		}

		result = append(result, segment)
	}

	writeJson(w, result)
}

func valueSegmentLength(r *http.Request) *int {
	if val := r.FormValue("segment_length"); val != "" {
		if val, err := strconv.Atoi(val); err == nil {
			return &val
		}
	}

	return nil
}

func valueSegmentOverlap(r *http.Request) *int {
	if val := r.FormValue("segment_overlap"); val != "" {
		if val, err := strconv.Atoi(val); err == nil {
			return &val
		}
	}

	return nil
}
