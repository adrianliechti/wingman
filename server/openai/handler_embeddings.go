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

	result := &EmbeddingList{
		Object: "list",

		Model: req.Model,
	}

	embedding, err := embedder.Embed(r.Context(), inputs)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	for i, e := range embedding.Embeddings {
		result.Data = append(result.Data, Embedding{
			Object: "embedding",

			Index:     i,
			Embedding: e,
		})
	}

	if embedding.Usage != nil {
		result.Usage = &EmbeddingUsage{
			PromptTokens: embedding.Usage.InputTokens,
			TotalTokens:  embedding.Usage.InputTokens + embedding.Usage.OutputTokens,
		}
	}

	writeJson(w, result)
}

// https://platform.openai.com/docs/api-reference/embeddings/create
type EmbeddingsRequest struct {
	Model string `json:"model"`

	Input any `json:"input"`

	// encoding_format string: float, base64
	// dimensions int
	// user string
}

func (r *EmbeddingsRequest) UnmarshalJSON(data []byte) error {
	type1 := struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}{}

	if err := json.Unmarshal(data, &type1); err == nil {
		*r = EmbeddingsRequest{
			Model: type1.Model,
			Input: type1.Input,
		}

		return nil
	}

	type2 := struct {
		Model string `json:"model"`

		Input []string `json:"input"`
	}{}

	if err := json.Unmarshal(data, &type2); err == nil {
		*r = EmbeddingsRequest{
			Model: type2.Model,
			Input: type2.Input,
		}

		return nil
	}

	return nil
}

// https://platform.openai.com/docs/api-reference/embeddings/create
type EmbeddingList struct {
	Object string `json:"object"` // "list"

	Model string      `json:"model"`
	Data  []Embedding `json:"data"`

	Usage *EmbeddingUsage `json:"usage,omitempty"`
}

// https://platform.openai.com/docs/api-reference/embeddings/object
type Embedding struct {
	Object string `json:"object"` // "embedding"

	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type EmbeddingUsage struct {
	PromptTokens int `json:"prompt_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}
