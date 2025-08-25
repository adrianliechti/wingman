package embeddings

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/openai/openai-go/v2"
)

func (h *Handler) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req openai.EmbeddingNewParams

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	embedder, err := h.Embedder(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var inputs []string

	if len(req.Input.OfArrayOfStrings) > 0 {
		inputs = req.Input.OfArrayOfStrings
	} else if req.Input.OfString.Valid() {
		inputs = []string{req.Input.OfString.Value}
	}

	if len(inputs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no input provided"))
		return
	}

	embedding, err := embedder.Embed(r.Context(), inputs)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := openai.CreateEmbeddingResponse{
		Model: embedding.Model,
	}
	if result.Model == "" {
		result.Model = req.Model
	}

	for i, e := range embedding.Embeddings {
		result.Data = append(result.Data, openai.Embedding{
			Index:     int64(i),
			Embedding: toFloat64(e),
		})
	}

	if embedding.Usage != nil {
		result.Usage = openai.CreateEmbeddingResponseUsage{
			PromptTokens: int64(embedding.Usage.InputTokens),
			TotalTokens:  int64(embedding.Usage.InputTokens + embedding.Usage.OutputTokens),
		}
	}

	writeJson(w, result)
}

func toFloat64(embedding []float32) []float64 {
	result := make([]float64, len(embedding))

	for i, v := range embedding {
		result[i] = float64(v)
	}

	return result
}
