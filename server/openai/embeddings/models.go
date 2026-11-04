package embeddings

// https://platform.openai.com/docs/api-reference/embeddings/create
type EmbeddingsRequest struct {
	Model string `json:"model"`

	Input any `json:"input"`

	EncodingFormat string `json:"encoding_format,omitzero"`
	Dimensions     *int   `json:"dimensions,omitzero"`
}

// https://platform.openai.com/docs/api-reference/embeddings/object
type Embedding struct {
	Object string `json:"object"` // "embedding"

	Index     int `json:"index"`
	Embedding any `json:"embedding"` // []float32 or base64 string
}

// https://platform.openai.com/docs/api-reference/embeddings/create
type EmbeddingList struct {
	Object string `json:"object"` // "list"

	Model string      `json:"model"`
	Data  []Embedding `json:"data"`

	Usage *Usage `json:"usage,omitzero"`
}

type Usage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}
