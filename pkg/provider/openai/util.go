package openai

import (
	"errors"

	"github.com/openai/openai-go/v3"
)

func convertError(err error) error {
	var apierr *openai.Error

	if errors.As(err, &apierr) {
		//println(string(apierr.DumpRequest(true)))  // Prints the serialized HTTP request
		//println(string(apierr.DumpResponse(true))) // Prints the serialized HTTP response
	}

	return err
}

var ReasoningModels = []string{
	// GPT 5.1 Family
	"gpt-5.1",
	"gpt-5.1-mini",
	"gpt-5.1-nano",

	"gpt-5.1-codex",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex-mini",

	// GPT 5 Family
	"gpt-5",
	"gpt-5-mini",
	"gpt-5-nano",

	"gpt-5-codex",

	// GPT o Family
	"o1",
	"o1-mini",
	"o3",
	"o3-mini",
	"o4-mini",
}
