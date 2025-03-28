package rag

import (
	_ "embed"
)

var (
	//go:embed prompt.tmpl
	promptTemplate string
)

type promptData struct {
	Input   string
	Results []promptResult
}

type promptResult struct {
	Title   string
	Source  string
	Content string

	Metadata map[string]string
}
