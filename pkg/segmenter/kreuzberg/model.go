package kreuzberg

type ExtractionConfig struct {
	Chunking *ChunkingConfig `json:"chunking,omitzero"`
}

type ChunkingConfig struct {
	ChunkingStrategy string `json:"chunking_strategy,omitzero"`

	ChunkSize    *int `json:"chunk_size,omitzero"`
	ChunkOverlap *int `json:"chunk_overlap,omitzero"`
}

type ExtractionResult struct {
	Content string `json:"content"`

	Chunks []string `json:"chunks"`
}
