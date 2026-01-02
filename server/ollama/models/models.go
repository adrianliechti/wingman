package models

import "time"

type ShowRequest struct {
	Model  string `json:"model"`
	System string `json:"system"`

	Verbose bool `json:"verbose"`
}

type ShowResponse struct {
	// License       string         `json:"license,omitempty"`
	// Modelfile     string         `json:"modelfile,omitempty"`
	// Parameters    string         `json:"parameters,omitempty"`
	// Template      string         `json:"template,omitempty"`
	// System        string         `json:"system,omitempty"`
	// Renderer      string         `json:"renderer,omitempty"`
	// Parser        string         `json:"parser,omitempty"`
	// Details       ModelDetails   `json:"details,omitempty"`
	// Messages      []Message      `json:"messages,omitempty"`
	// RemoteModel   string         `json:"remote_model,omitempty"`
	// RemoteHost    string         `json:"remote_host,omitempty"`
	ModelInfo map[string]any `json:"model_info,omitempty"`
	// ProjectorInfo map[string]any `json:"projector_info,omitempty"`
	// Tensors       []Tensor       `json:"tensors,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	// ModifiedAt    time.Time      `json:"modified_at,omitempty"`
	// Requires      string         `json:"requires,omitempty"`
}

type ModelsResponse struct {
	Models []Model `json:"models"`
}

type Model struct {
	Name  string `json:"name"`
	Model string `json:"model"`

	//RemoteModel string       `json:"remote_model,omitempty"`
	//RemoteHost  string       `json:"remote_host,omitempty"`
	ModifiedAt time.Time `json:"modified_at"`
	//Size        int64        `json:"size"`
	//Digest      string       `json:"digest"`
	//Details     ModelDetails `json:"details,omitempty"`
}

// type ModelDetails struct {
// 	ParentModel       string   `json:"parent_model"`
// 	Format            string   `json:"format"`
// 	Family            string   `json:"family"`
// 	Families          []string `json:"families"`
// 	ParameterSize     string   `json:"parameter_size"`
// 	QuantizationLevel string   `json:"quantization_level"`
// }
