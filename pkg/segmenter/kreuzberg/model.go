package kreuzberg

type ExtractionConfig struct {
	Chunking *ChunkingConfig `json:"chunking,omitempty"`
}

type ChunkingConfig struct {
	ChunkingStrategy string `json:"chunking_strategy,omitempty"`

	ChunkSize    *int `json:"chunk_size,omitempty"`
	ChunkOverlap *int `json:"chunk_overlap,omitempty"`
}

type ExtractionResult struct {
	Content string `json:"content"`

	Chunks []string `json:"chunks"`
}
