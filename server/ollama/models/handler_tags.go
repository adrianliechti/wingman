package models

import (
	"net/http"
	"time"
)

func (h *Handler) handleTags(w http.ResponseWriter, r *http.Request) {
	result := &ModelsResponse{
		Models: []Model{},
	}

	for _, m := range h.Models() {
		result.Models = append(result.Models, Model{
			Name: m.ID,

			Model:      m.ID,
			ModifiedAt: time.Now(),

			// Size:   0,
			// Digest: "",

			// Details: ModelDetails{
			// 	Format:            "gguf",
			// 	Family:            "llama",
			// 	Families:          nil,
			// 	ParameterSize:     "7B",
			// 	QuantizationLevel: "Q4_0",
			// },
		})
	}

	writeJson(w, result)
}
