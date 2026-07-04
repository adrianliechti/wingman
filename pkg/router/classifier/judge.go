package classifier

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const (
	judgeTimeout   = 10 * time.Second
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
				"description": "the index of the chosen candidate model",
			},
		},

		"required":             []string{judgeSchemaKey},
		"additionalProperties": false,
	},
}

// judgePick asks the judge model to choose among the eligible candidates. The
// options are renumbered 0..n-1 (models reliably answer with the ordinal
// position, so gaps in the original indices invite off-by-position picks) and
// each line carries the candidate's relative cost, since the judge is asked
// for the cheapest sufficient option. Reasoning effort is pinned to minimal so
// the pick fits its timeout. It never errors: any failure (timeout, transport,
// bad JSON, out-of-range index) returns -1 and the caller keeps its prior
// pick.
func (c *Completer) judgePick(ctx context.Context, s signals, eligible []int) int {
	if c.judge == nil || s.queryText == "" {
		return -1
	}

	var prompt strings.Builder

	prompt.WriteString("Task:\n")
	prompt.WriteString(truncateText(s.queryText, maxJudgeChars))
	prompt.WriteString("\n\n")

	if s.hasImage {
		prompt.WriteString("The task includes an image.\n")
	}

	if s.toolCount > 0 {
		prompt.WriteString("The task involves tool use.\n")
	}

	prompt.WriteString("\nCandidate models:\n")

	for k, i := range eligible {
		card := c.candidates[i].Card

		if card == "" {
			card = c.candidates[i].Model
		}

		prompt.WriteString("[")
		prompt.WriteString(strconv.Itoa(k))
		prompt.WriteString("] ")
		prompt.WriteString(card)
		prompt.WriteString(" (relative cost: ")
		prompt.WriteString(strconv.FormatFloat(c.candidates[i].Cost, 'f', -1, 64))
		prompt.WriteString(")\n")
	}

	messages := []provider.Message{
		provider.SystemMessage("You route a task to a model. Choose the lowest-cost candidate that can still handle the task well and reply with its index."),
		provider.UserMessage(prompt.String()),
	}

	options := &provider.CompleteOptions{
		Schema: judgeSchema,

		ReasoningOptions: &provider.ReasoningOptions{
			Effort: provider.EffortMinimal,
		},
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

	if data.ModelIndex < 0 || data.ModelIndex >= len(eligible) {
		return -1
	}

	return eligible[data.ModelIndex]
}

// truncateText caps s at n bytes without splitting a UTF-8 rune.
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}

	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}

	return s[:n]
}
