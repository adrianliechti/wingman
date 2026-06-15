package classifier

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const (
	judgeTimeout   = 3 * time.Second
	maxJudgeChars  = 1500
	judgeSchemaKey = "model_index"
)

var judgeSchema = &provider.Schema{
	Name:        "model_choice",
	Description: "Selects the best model to handle the task",

	Properties: map[string]any{
		"type": "object",

		"properties": map[string]any{
			judgeSchemaKey: map[string]any{
				"type":        "integer",
				"description": "the index of the cheapest candidate model that can handle the task well",
			},
		},

		"required":             []string{judgeSchemaKey},
		"additionalProperties": false,
	},
}

// judgePick asks the judge model to choose among the eligible candidates. It is
// bounded by a short timeout and never errors: any failure (timeout, transport,
// bad JSON, out-of-range index) returns -1 and the caller keeps its prior pick.
func (c *Completer) judgePick(ctx context.Context, s signals, eligible []int) int {
	if c.judge == nil {
		return -1
	}

	var prompt strings.Builder

	prompt.WriteString("Task:\n")
	prompt.WriteString(truncate(s.queryText, maxJudgeChars))
	prompt.WriteString("\n\n")

	if s.hasImage {
		prompt.WriteString("The task includes an image.\n")
	}

	if s.toolCount > 0 {
		prompt.WriteString("The task involves tool use.\n")
	}

	prompt.WriteString("\nCandidate models:\n")

	for _, i := range eligible {
		prompt.WriteString("[")
		prompt.WriteString(strconv.Itoa(i))
		prompt.WriteString("] ")
		prompt.WriteString(c.candidates[i].Card)
		prompt.WriteByte('\n')
	}

	messages := []provider.Message{
		provider.SystemMessage("You route a task to a model. Choose the cheapest candidate that can handle the task well and reply with its index."),
		provider.UserMessage(prompt.String()),
	}

	options := &provider.CompleteOptions{
		Schema: judgeSchema,
	}

	ctx, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()

	acc := provider.CompletionAccumulator{}

	for completion, err := range c.judge.Complete(ctx, messages, options) {
		if err != nil {
			return -1
		}

		if completion != nil {
			acc.Add(*completion)
		}
	}

	result := acc.Result()

	if result.Message == nil {
		return -1
	}

	var data struct {
		ModelIndex int `json:"model_index"`
	}

	if err := json.Unmarshal([]byte(result.Message.Text()), &data); err != nil {
		return -1
	}

	if !containsInt(eligible, data.ModelIndex) {
		return -1
	}

	return data.ModelIndex
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}

	return s
}
