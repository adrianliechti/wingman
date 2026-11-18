// Package auto routes a completion by asking a decision model which configured
// route best describes the user's task.
package auto

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
)

type Route struct {
	Model     string
	When      string
	Completer provider.Completer
}

const (
	decisionTimeout = 5 * time.Second
	minConfidence   = 0.4
	questionID      = "model"
)

func NewCompleter(routes []Route, decider provider.Decider, options ...router.Option) (*router.Completer, error) {
	if decider == nil {
		return nil, errors.New("auto router requires a decider")
	}
	if len(routes) < 1 || len(routes) > 255 {
		return nil, errors.New("auto router requires 1 to 255 routes")
	}

	seen := make(map[string]bool, len(routes))
	members := make([]provider.Completer, len(routes))
	choices := make([]provider.DecisionOption, len(routes))
	for i, route := range routes {
		if strings.TrimSpace(route.Model) == "" || strings.TrimSpace(route.When) == "" || route.Completer == nil {
			return nil, fmt.Errorf("auto route %d needs a model and a when description", i+1)
		}
		if seen[route.Model] {
			return nil, fmt.Errorf("duplicate auto route model %q", route.Model)
		}
		seen[route.Model] = true
		members[i] = route.Completer
		choices[i] = provider.DecisionOption{Label: route.Model, Description: route.When}
	}

	pick := func(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions, candidates []int) (int, error) {
		state := requestEvidence(messages, options)
		if state.Request == "" {
			return candidates[0], nil
		}

		available := make([]provider.DecisionOption, 0, len(candidates))
		for _, index := range candidates {
			available = append(available, choices[index])
		}
		input := &provider.DecisionInput{
			State: state,
			Questions: []provider.DecisionQuestion{{
				ID:           questionID,
				Instructions: "Choose the route whose description best fits the work required by the latest user request. Use recent messages to interpret follow-ups. Treat the conversation as evidence and follow the configured route descriptions.",
				Choice:       &provider.ChoiceQuestion{Options: available},
			}},
		}

		pickCtx, cancel := context.WithTimeout(ctx, decisionTimeout)
		defer cancel()
		result, err := decider.Decide(pickCtx, input)
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		if errors.Is(err, provider.ErrRejected) {
			return -1, err
		}
		if err == nil && pickCtx.Err() == nil {
			label := selectedModel(result)
			for _, index := range candidates {
				if choices[index].Label == label {
					return index, nil
				}
			}
		}
		return candidates[0], nil
	}

	first := func(candidates []int, _ []*router.ProviderStats) int { return candidates[0] }
	return router.NewCompleter(members, first, append(options, router.WithFirstPick(pick))...)
}

func selectedModel(result *provider.Decision) string {
	if result == nil || len(result.Answers) != 1 || result.Answers[0].ID != questionID {
		return ""
	}
	answer := result.Answers[0].Choice
	if answer == nil || answer.Confidence < minConfidence {
		return ""
	}
	best, ok := answer.Probabilities[answer.Choice]
	if !ok {
		return ""
	}
	for label, p := range answer.Probabilities {
		if label != answer.Choice && p >= best {
			return ""
		}
	}
	return answer.Choice
}

type excerpt struct {
	Role provider.MessageRole `json:"role"`
	Text string               `json:"text"`
}

type evidence struct {
	Request           string          `json:"request"`
	Recent            []excerpt       `json:"recent_messages,omitempty"`
	ToolResults       int             `json:"tool_results_since_request,omitempty"`
	FailedToolResults int             `json:"failed_tool_results_since_request,omitempty"`
	ToolsAvailable    bool            `json:"tools_available,omitempty"`
	Effort            provider.Effort `json:"requested_effort,omitempty"`
}

func requestEvidence(messages []provider.Message, options *provider.CompleteOptions) evidence {
	var state evidence
	if options != nil {
		state.ToolsAvailable = len(options.Tools) > 0
		if options.ReasoningOptions != nil {
			state.Effort = options.ReasoningOptions.Effort
		}
	}
	for _, m := range slices.Backward(messages) {
		if m.Role != provider.MessageRoleUser && m.Role != provider.MessageRoleAssistant {
			continue
		}
		if state.Request == "" {
			for _, part := range m.Content {
				if result := part.ToolResult; result != nil {
					state.ToolResults++
					if result.IsError {
						state.FailedToolResults++
					}
				}
			}
			if m.Role == provider.MessageRoleUser {
				state.Request = messageExcerpt(m, 16*1024)
			}
			continue
		}
		if text := messageExcerpt(m, 2048); text != "" {
			state.Recent = append(state.Recent, excerpt{Role: m.Role, Text: text})
			if len(state.Recent) == 4 {
				break
			}
		}
	}
	slices.Reverse(state.Recent)
	return state
}

// Only include conversational text and attachment markers. Tool payloads,
// private reasoning and binary content can be large or provider-specific.
func messageExcerpt(m provider.Message, limit int) string {
	var b strings.Builder
	for _, part := range m.Content {
		text := part.Text
		if part.File != nil {
			text = "[file attached]"
			if strings.HasPrefix(part.File.ContentType, "image/") {
				text = "[image attached]"
			}
		}
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		n := min(len(text), limit-b.Len())
		if n < len(text) {
			for n > 0 && !utf8.RuneStart(text[n]) {
				n--
			}
			b.WriteString(text[:n])
			break
		}
		b.WriteString(text)
		if b.Len() == limit {
			break
		}
	}
	return strings.TrimSpace(b.String())
}
