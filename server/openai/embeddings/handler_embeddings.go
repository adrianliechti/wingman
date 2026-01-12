package embeddings

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
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

	var options *provider.EmbedOptions

	if req.Dimensions != nil {
		options = &provider.EmbedOptions{
			Dimensions: req.Dimensions,
		}
	}

	embedding, err := embedder.Embed(r.Context(), inputs, options)

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

	useBase64 := req.EncodingFormat == "base64"

	for i, e := range embedding.Embeddings {
		embedding := Embedding{
			Object: "embedding",

			Index:     i,
			Embedding: e,
		}

		if useBase64 {
			embedding.Embedding = floatsToBase64(e)
		}

		result.Data = append(result.Data, embedding)
	}

	if embedding.Usage != nil {
		result.Usage = &Usage{
			PromptTokens: embedding.Usage.InputTokens,
			TotalTokens:  embedding.Usage.InputTokens + embedding.Usage.OutputTokens,
		}
	}

	writeJson(w, result)
}

func floatsToBase64(floats []float32) string {
	buf := make([]byte, len(floats)*4)

	for i, f := range floats {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}

	return base64.StdEncoding.EncodeToString(buf)
}
