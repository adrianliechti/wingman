package openai

import (
	"encoding/json"
	"errors"
	"net/http"
)

func (h *Handler) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req EmbeddingsRequest

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

	switch v := req.Input.(type) {
	case string:
		inputs = []string{v}
	case []string:
		inputs = v
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

	result := &EmbeddingList{
		Object: "list",

		Model: embedding.Model,
	}

	if result.Model == "" {
		result.Model = req.Model
	}

	for i, e := range embedding.Embeddings {
		result.Data = append(result.Data, Embedding{
			Object: "embedding",

			Index:     i,
			Embedding: e,
		})
	}

	if embedding.Usage != nil {
		result.Usage = &Usage{
			PromptTokens: embedding.Usage.InputTokens,
			TotalTokens:  embedding.Usage.InputTokens + embedding.Usage.OutputTokens,
		}
	}

	writeJson(w, result)
}
