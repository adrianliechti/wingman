package openai

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
)

func (d *Decider) convertDecisionRequest(input *provider.DecisionInput) (*openai.DecisionNewParams, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	req := &openai.DecisionNewParams{Model: d.model}
	if cache := input.CacheOptions; cache != nil {
		if cache.Key != "" {
			req.PromptCacheKey = openai.String(cache.Key)
		}
		var cacheOpts openai.DecisionNewParamsPromptCacheOptions
		hasCacheOpts := false
		if cache.Mode == provider.CacheModeExplicit {
			cacheOpts.Mode = openai.String("explicit")
			hasCacheOpts = true
		}
		switch cache.Retention {
		case provider.CacheRetentionStandard:
			cacheOpts.Ttl = "30m"
			hasCacheOpts = true
		case provider.CacheRetentionExtended:
			cacheOpts.Ttl = "12h"
			hasCacheOpts = true
		}
		if hasCacheOpts {
			req.PromptCacheOptions = cacheOpts
		}
	}
	if err := convertDecisionInput(req, input); err != nil {
		return nil, err
	}
	for _, q := range input.Questions {
		if q.Box != nil {
			return nil, &provider.ProviderError{
				Code:    400,
				Message: "openai decisions provider does not support box questions",
			}
		}
		name := openai.String(q.ID)
		switch {
		case q.Noul != nil:
			instr, err := convertPredicateInstructions(q)
			if err != nil {
				return nil, err
			}
			req.Questions = append(req.Questions, openai.DecisionNewParamsQuestionUnion{OfPredicate: &openai.DecisionNewParamsQuestionPredicate{
				Name: name, Instructions: instr,
			}})
		case q.Choice != nil:
			instr, err := convertChoiceInstructions(q)
			if err != nil {
				return nil, err
			}
			question := &openai.DecisionNewParamsQuestionChoice{Name: name, Instructions: instr}
			for _, o := range q.Choice.Options {
				value := openai.DecisionNewParamsQuestionChoiceChoiceValueUnion{OfString: openai.String(o.Label)}
				switch v := o.Value.(type) {
				case string:
					value.OfString = openai.String(v)
				case bool:
					value = openai.DecisionNewParamsQuestionChoiceChoiceValueUnion{OfBool: openai.Bool(v)}
				}
				question.Choices = append(question.Choices, openai.DecisionNewParamsQuestionChoiceChoice{
					Value: value, Description: openai.String(decisionText(o.Description)),
				})
			}
			req.Questions = append(req.Questions, openai.DecisionNewParamsQuestionUnion{OfChoice: question})
		case q.Score != nil:
			if len(q.Score.Levels) < 2 || len(q.Score.Levels) > 10 {
				return nil, &provider.ProviderError{Code: 400, Message: "OpenAI score questions require 2 to 10 levels"}
			}
			instr, err := convertScoreInstructions(q)
			if err != nil {
				return nil, err
			}
			question := &openai.DecisionNewParamsQuestionScore{Name: name, Instructions: instr}
			for j, level := range q.Score.Levels {
				label := strconv.Itoa(j)
				if q.Score.Labels != nil && q.Score.Labels[j] != "" {
					label = q.Score.Labels[j]
				} else if j < len(q.Score.Values) {
					label = strconv.Itoa(q.Score.Values[j])
				}
				question.Levels = append(question.Levels, openai.DecisionNewParamsQuestionScoreLevel{Label: label, Description: openai.String(decisionText(level))})
			}
			req.Questions = append(req.Questions, openai.DecisionNewParamsQuestionUnion{OfScore: question})
		}
	}
	return req, nil
}

func convertDecisionInput(req *openai.DecisionNewParams, input *provider.DecisionInput) error {
	if len(input.Messages) > 0 && len(input.Assets) == 0 {
		// The server already validated this envelope. Preserve JSON numbers, image
		// details, empty parts, tool ordering, and explicit cache boundaries.
		if raw, ok := input.State.(jsontext.Value); ok {
			req.Input = param.Override[openai.DecisionNewParamsInputUnion](raw)
			return nil
		}
	}
	if len(input.Messages) == 0 && len(input.Assets) == 0 {
		req.Input.OfString = openai.String(decisionText(input.State))
		return nil
	}
	var items []any
	for _, m := range input.EvidenceMessages() {
		var parts []any
		flush := func() {
			if len(parts) == 0 {
				return
			}
			role := string(m.Role)
			if role == "system" {
				role = "developer"
			}
			items = append(items, map[string]any{"role": role, "content": parts})
			parts = nil
		}
		for _, c := range m.Content {
			if c.ToolCall != nil {
				flush()
				call := c.ToolCall
				item := map[string]any{"call_id": call.ID, "name": call.Name}
				if call.Kind == provider.ToolKindCustom {
					item["type"] = "custom_tool_call"
					item["input"] = call.Arguments
				} else {
					item["type"] = "function_call"
					item["arguments"] = call.Arguments
				}
				items = append(items, item)
				continue
			}
			if c.ToolResult != nil {
				flush()
				var output []any
				for _, p := range c.ToolResult.Parts {
					if p.File != nil {
						output = append(output, decisionImagePart(p.File))
					} else {
						output = append(output, map[string]any{"type": "input_text", "text": p.Text})
					}
				}
				typ := "function_call_output"
				if c.ToolResult.Kind == provider.ToolKindCustom {
					typ = "custom_tool_call_output"
				}
				items = append(items, map[string]any{"type": typ, "call_id": c.ToolResult.ID, "output": output})
				continue
			}
			part := decisionContentPart(c)
			if m.Role == provider.MessageRoleAssistant {
				if c.Refusal != "" {
					part = map[string]any{"type": "refusal", "refusal": c.Refusal}
				} else {
					part = map[string]any{"type": "output_text", "text": c.Text}
				}
			}
			parts = append(parts, part)
		}
		flush()
	}
	req.Input = param.Override[openai.DecisionNewParamsInputUnion](items)
	return nil
}

func decisionImagePart(file *provider.File) map[string]any {
	part := map[string]any{"type": "input_image", "image_url": "data:" + file.ContentType + ";base64," + base64.StdEncoding.EncodeToString(file.Content)}
	if file.ImageDetail != provider.ImageDetailDefault {
		part["detail"] = string(file.ImageDetail)
	}
	return part
}

func decisionContentPart(c provider.Content) map[string]any {
	if len(c.JSON) > 0 {
		return map[string]any{"type": "input_json", "data": jsontext.Value(c.JSON)}
	}
	part := map[string]any{"type": "input_text", "text": c.Text}
	if c.File != nil {
		part = decisionImagePart(c.File)
	}
	if c.CacheControl != nil {
		part["prompt_cache_breakpoint"] = map[string]any{"mode": "explicit"}
	}
	return part
}

func convertQuestionInstructionParts(q provider.DecisionQuestion) ([]shared.DecisionQuestionInputPartUnionParam, string, bool) {
	if raw, ok := q.Instructions.(jsontext.Value); ok && len(raw) > 0 {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return nil, text, false
		}
	}
	if contents, ok := q.Instructions.([]provider.Content); ok {
		parts := make([]shared.DecisionQuestionInputPartUnionParam, 0, len(contents))
		for _, c := range contents {
			parts = append(parts, param.Override[shared.DecisionQuestionInputPartUnionParam](decisionContentPart(c)))
		}
		return parts, "", true
	}
	return nil, decisionText(q.Instructions), false
}

func convertPredicateInstructions(q provider.DecisionQuestion) (openai.DecisionNewParamsQuestionPredicateInstructionsUnion, error) {
	parts, text, isParts := convertQuestionInstructionParts(q)
	if isParts {
		if q.Noul != nil && (q.Noul.True != nil || q.Noul.False != nil) {
			parts = append(parts, shared.DecisionQuestionInputPartUnionParam{
				OfInputText: &shared.DecisionQuestionInputTextParam{
					Text: "Criteria: " + decisionText(map[string]any{"true": q.Noul.True, "false": q.Noul.False}),
				},
			})
		}
		return openai.DecisionNewParamsQuestionPredicateInstructionsUnion{
			OfDecisionQuestionInputPartArray: parts,
		}, nil
	}
	if q.Noul != nil && (q.Noul.True != nil || q.Noul.False != nil) {
		text += "\nCriteria: " + decisionText(map[string]any{"true": q.Noul.True, "false": q.Noul.False})
	}
	return openai.DecisionNewParamsQuestionPredicateInstructionsUnion{
		OfString: openai.String(text),
	}, nil
}

func convertChoiceInstructions(q provider.DecisionQuestion) (openai.DecisionNewParamsQuestionChoiceInstructionsUnion, error) {
	parts, text, isParts := convertQuestionInstructionParts(q)
	if isParts {
		return openai.DecisionNewParamsQuestionChoiceInstructionsUnion{
			OfDecisionQuestionInputPartArray: parts,
		}, nil
	}
	return openai.DecisionNewParamsQuestionChoiceInstructionsUnion{
		OfString: openai.String(text),
	}, nil
}

func convertScoreInstructions(q provider.DecisionQuestion) (openai.DecisionNewParamsQuestionScoreInstructionsUnion, error) {
	parts, text, isParts := convertQuestionInstructionParts(q)
	if isParts {
		return openai.DecisionNewParamsQuestionScoreInstructionsUnion{
			OfDecisionQuestionInputPartArray: parts,
		}, nil
	}
	return openai.DecisionNewParamsQuestionScoreInstructionsUnion{
		OfString: openai.String(text),
	}, nil
}

// Values have already passed DecisionInput.Validate, including JSON encoding.
func decisionText(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	data, _ := json.Marshal(value, json.Deterministic(true))
	var text string
	if json.Unmarshal(data, &text) == nil {
		return text
	}
	return string(data)
}

func convertDecision(decision *openai.Decision, input *provider.DecisionInput) (*provider.Decision, error) {
	if decision == nil || decision.Model == "" || len(decision.Answers) != len(input.Questions) {
		return nil, errors.New("incomplete decision response")
	}
	result := &provider.Decision{Model: decision.Model, Usage: &provider.Usage{
		InputTokens: int(decision.Usage.InputTokens), OutputTokens: int(decision.Usage.OutputTokens),
		CacheReadInputTokens:     int(decision.Usage.InputTokensDetails.CachedTokens),
		CacheCreationInputTokens: int(decision.Usage.InputTokensDetails.CacheWriteTokens),
		CacheWriteTokensDetails:  cacheWriteDetails(decision.Usage.InputTokensDetails.JSON.ExtraFields["cache_write_tokens_details"].Raw()),
	}}
	if decision.Usage.OutputTokensDetails.JSON.ReasoningTokens.Valid() {
		tokens := int(decision.Usage.OutputTokensDetails.ReasoningTokens)
		result.Usage.ReasoningTokens = &tokens
	}
	for i, answer := range decision.Answers {
		q := input.Questions[i]
		if answer.Name != q.ID {
			return nil, errors.New("decision answer order does not match questions")
		}
		value := provider.DecisionAnswer{ID: q.ID}
		switch a := answer.AsAny().(type) {
		case openai.DecisionAnswerPredicate:
			if q.Noul == nil || !a.JSON.Probability.Valid() || !decisionProbability(a.Probability) {
				return nil, errors.New("invalid predicate answer")
			}
			value.Noul = &provider.NoulAnswer{Probability: a.Probability}
		case openai.DecisionAnswerChoice:
			if q.Choice == nil || !a.JSON.Choice.Valid() || !a.JSON.Confidence.Valid() || !decisionProbability(a.Confidence) || len(a.Probabilities) != len(q.Choice.Options) {
				return nil, errors.New("invalid choice answer")
			}
			choice := any(a.Choice.OfString)
			if !a.Choice.JSON.OfString.Valid() && !a.Choice.JSON.OfBool.Valid() {
				return nil, errors.New("invalid choice value type")
			}
			if a.Choice.JSON.OfBool.Valid() {
				choice = a.Choice.OfBool
			}
			distribution := make(map[string]float64, len(a.Probabilities))
			selected := false
			value.Choice = &provider.ChoiceAnswer{Confidence: a.Confidence, Probabilities: distribution}
			for _, p := range a.Probabilities {
				if !p.JSON.Value.Valid() || !p.JSON.Probability.Valid() || !decisionProbability(p.Probability) {
					return nil, errors.New("invalid choice probability")
				}
				optionValue := any(p.Value.OfString)
				if !p.Value.JSON.OfString.Valid() && !p.Value.JSON.OfBool.Valid() {
					return nil, errors.New("invalid choice probability value type")
				}
				if p.Value.JSON.OfBool.Valid() {
					optionValue = p.Value.OfBool
				}
				for _, option := range q.Choice.Options {
					expected := option.Value
					if expected == nil {
						expected = option.Label
					}
					if optionValue != expected {
						continue
					}
					distribution[option.Label] = p.Probability
					if choice == expected {
						value.Choice.Choice, selected = option.Label, true
					}
				}
			}
			if !selected || len(distribution) != len(q.Choice.Options) {
				return nil, errors.New("unknown decision choice")
			}
		case openai.DecisionAnswerScore:
			if q.Score == nil || !a.JSON.Score.Valid() || !a.JSON.Confidence.Valid() || !decisionProbability(a.Confidence) || a.Score < 0 || a.Score > float64(len(q.Score.Levels)-1) || len(a.Probabilities) != len(q.Score.Levels) {
				return nil, errors.New("invalid score answer")
			}
			value.Score = &provider.ScoreAnswer{Score: a.Score, Confidence: a.Confidence, Levels: make([]provider.ScoreLevel, len(q.Score.Levels))}
			seen := make(map[int64]bool)
			var customScore float64
			for _, p := range a.Probabilities {
				if !p.JSON.Value.Valid() || !p.JSON.Probability.Valid() || !decisionProbability(p.Probability) || p.Value < 0 || p.Value >= int64(len(q.Score.Levels)) || seen[p.Value] {
					return nil, errors.New("invalid score level")
				}
				seen[p.Value] = true
				idx := int(p.Value)
				val := idx
				if idx < len(q.Score.Values) {
					val = q.Score.Values[idx]
				}
				var label string
				if idx < len(q.Score.Labels) {
					label = q.Score.Labels[idx]
				}
				customScore += float64(val) * p.Probability
				value.Score.Levels[idx] = provider.ScoreLevel{
					Value:       val,
					Label:       label,
					Description: q.Score.Levels[idx],
					Probability: p.Probability,
				}
			}
			if len(q.Score.Values) > 0 {
				value.Score.Score = customScore
			}
		case openai.DecisionAnswerRefusal:
			value.Refusal = &provider.RefusalAnswer{}
		default:
			return nil, fmt.Errorf("unsupported decision answer: %s", answer.Type)
		}
		result.Answers = append(result.Answers, value)
	}
	return result, nil
}

func decisionProbability(p float64) bool {
	return !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p <= 1
}
