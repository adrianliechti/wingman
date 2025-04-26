package unstructured

import (
	"io"
	"net/http"
	"strconv"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/segmenter"
	"github.com/google/uuid"
)

func (h *Handler) handlePartition(w http.ResponseWriter, r *http.Request) {
	e, err := h.Extractor("")

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	input := extractor.Input{}

	if url := r.FormValue("url"); url != "" {
		input.URL = &url
	} else {
		file, header, err := r.FormFile("file")

		if err != nil {
			file, header, err = r.FormFile("files")
		}

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		data, err := io.ReadAll(file)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		defer file.Close()

		input.File = &provider.File{
			Name: header.Filename,

			Content:     data,
			ContentType: header.Header.Get("Content-Type"),
		}
	}

	outputFormat := r.FormValue("output_format")

	chunkStrategy := parseChunkingStrategy(r.FormValue("chunking_strategy"))
	chunkLength, _ := strconv.Atoi(r.FormValue("max_characters"))
	chunkOverlap, _ := strconv.Atoi(r.FormValue("overlap"))

	if chunkLength <= 0 {
		chunkLength = 500
	}

	if chunkOverlap <= 0 {
		chunkOverlap = 0
	}

	document, err := e.Extract(r.Context(), input, nil)

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result := []Partition{
		{
			ID:   uuid.NewString(),
			Text: string(document.Content),
		},
	}

	if chunkStrategy != ChunkingStrategyNone {
		s, err := h.Segmenter("")

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		segments, err := s.Segment(r.Context(), string(document.Content), &segmenter.SegmentOptions{
			SegmentLength:  &chunkLength,
			SegmentOverlap: &chunkOverlap,
		})

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		result = []Partition{}

		for _, s := range segments {
			partition := Partition{
				ID:   uuid.NewString(),
				Text: s.Text,
			}

			result = append(result, partition)
		}
	}

	_ = outputFormat

	writeJson(w, result)
}

func parseChunkingStrategy(value string) ChunkingStrategy {
	switch value {
	case "none", "":
		return ChunkingStrategyNone
	}

	return ChunkingStrategyUnknown
}
