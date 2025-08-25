package models

// https://platform.openai.com/docs/api-reference/models/object
type Model struct {
	Object string `json:"object"` // "model"

	ID      string `json:"id"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// https://platform.openai.com/docs/api-reference/models
type ModelList struct {
	Object string `json:"object"` // "list"

	Models []Model `json:"data"`
}
