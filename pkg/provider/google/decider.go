package google

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Decider = (*Decider)(nil)

type Decider struct {
	*Config
	endpoint string
}

// NewDecider accepts a base URL, optionally ending in /v1beta or /decisions.
// The empty URL uses the Gemini Developer API; Vertex AI is not supported.
func NewDecider(endpoint, model string, options ...Option) (*Decider, error) {
	model = strings.TrimPrefix(model, "models/")
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("decision model is required")
	}
	if endpoint == "" {
		endpoint = "https://generativelanguage.googleapis.com/v1beta"
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(endpoint, "/decisions") {
		if !strings.HasSuffix(endpoint, "/v1beta") {
			endpoint += "/v1beta"
		}
		endpoint += "/decisions"
	}
	cfg := &Config{model: model, maxRetries: 2}
	for _, option := range options {
		option(cfg)
	}
	if cfg.token == "" {
		cfg.token = os.Getenv("GOOGLE_API_KEY")
		if cfg.token == "" {
			cfg.token = os.Getenv("GEMINI_API_KEY")
		}
	}
	if cfg.client == nil {
		cfg.client = provider.DefaultClient
	}
	return &Decider{Config: cfg, endpoint: endpoint}, nil
}

func (d *Decider) Decide(ctx context.Context, input *provider.DecisionInput) (*provider.Decision, error) {
	req, err := d.decisionRequest(input)
	if err != nil {
		return nil, provider.InvalidRequest(err)
	}
	data, err := json.Marshal(req, json.Deterministic(true))
	if err != nil {
		return nil, provider.InvalidRequest(err)
	}
	for attempt := 0; ; attempt++ {
		response, err := d.evaluateDecision(ctx, data)
		if err == nil {
			return response.decision(d.model, input)
		}
		upstream, ok := errors.AsType[*provider.ProviderError](err)
		if !ok || attempt >= d.maxRetries || upstream.Code != 408 && upstream.Code != 429 && upstream.Code < 500 {
			return nil, err
		}
		delay := upstream.RetryAfter
		if delay <= 0 {
			delay = time.Duration(1<<min(attempt, 4)) * 500 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (d *Decider) evaluateDecision(ctx context.Context, data []byte) (*DecisionResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if d.token != "" {
		req.Header.Set("x-goog-api-key", d.token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, interactionProviderError(fmt.Errorf("Gemini decisions returned HTTP %d", resp.StatusCode), resp.StatusCode, "", "", string(body), resp)
	}
	var result DecisionResponse
	if err := jsonutil.UnmarshalRead(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("invalid Gemini decision response: %w", err)
	}
	return &result, nil
}

func (d *Decider) decisionRequest(input *provider.DecisionInput) (*DecisionRequest, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	r := &DecisionRequest{Model: d.model, Questions: make(map[string]GeminiDecisionQuestion, len(input.Questions))}
	if raw, ok := input.State.(decisionContext); ok {
		r.Input = jsontext.Value(raw)
	} else {
		var blocks []decisionContent
		appendParts := func(parts []provider.Content) error {
			for _, part := range parts {
				if part.ToolCall != nil || part.ToolResult != nil || part.Reasoning != nil || part.Compaction != nil || part.Refusal != "" || part.Instructions != nil || part.ConfigurationUpdate != nil {
					return errors.New("Gemini decisions do not support history, tool, or thought blocks")
				}
				if part.Text != "" || part.TextPresent {
					blocks = append(blocks, decisionContent{Type: "text", Text: new(part.Text)})
				}
				if part.File != nil {
					media, err := fileContent(part.File)
					if err != nil {
						return err
					}
					data, err := json.Marshal(media)
					if err != nil {
						return err
					}
					var block decisionContent
					if err := json.Unmarshal(data, &block); err != nil {
						return err
					}
					switch part.File.ImageDetail {
					case provider.ImageDetailLow:
						block.Resolution = "low"
					case provider.ImageDetailHigh:
						block.Resolution = "high"
					case provider.ImageDetailOriginal:
						block.Resolution = "ultra_high"
					}
					blocks = append(blocks, block)
				}
			}
			return nil
		}
		if len(input.Messages) > 0 {
			if len(input.Messages) != 1 || input.Messages[0].Role != provider.MessageRoleUser {
				return nil, errors.New("Gemini decisions require one shared user input; conversation history is not supported")
			}
			if err := appendParts(input.Messages[0].Content); err != nil {
				return nil, err
			}
		} else if parts, ok := input.State.([]provider.Content); ok {
			if err := appendParts(parts); err != nil {
				return nil, err
			}
		} else {
			text, err := decisionText(input.State)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, decisionContent{Type: "text", Text: new(text)})
		}
		for _, key := range slices.Sorted(maps.Keys(input.Assets)) {
			if err := appendParts([]provider.Content{provider.TextContent("\nAsset replacing placeholder " + key + ":\n"), provider.FileContent(input.Assets[key])}); err != nil {
				return nil, err
			}
		}
		var err error
		r.Input, err = json.Marshal(blocks)
		if err != nil {
			return nil, err
		}
	}
	for _, q := range input.Questions {
		text, err := decisionText(q.Instructions)
		if err != nil {
			return nil, fmt.Errorf("questions[%q].instructions: %w", q.ID, err)
		}
		instructions, err := json.Marshal(text)
		if err != nil {
			return nil, err
		}
		question := GeminiDecisionQuestion{Instructions: instructions}
		var criteria any
		switch {
		case q.Noul != nil:
			question.Type = "noul"
			values := make(map[string]string)
			for key, v := range map[string]any{"true_description": q.Noul.True, "false_description": q.Noul.False} {
				if v == nil {
					continue
				}
				text, err := decisionText(v)
				if err != nil {
					return nil, err
				}
				values[key] = text
			}
			if len(values) > 0 {
				criteria = values
			}
		case q.Choice != nil:
			question.Type = "choice"
			values := make(map[string]string)
			for _, option := range q.Choice.Options {
				text, err := decisionText(option.Description)
				if err != nil {
					return nil, err
				}
				values[option.Label] = text
			}
			criteria = values
		case q.Score != nil:
			question.Type = "score"
			levels := make([]string, len(q.Score.Levels))
			for i, level := range q.Score.Levels {
				text, err := decisionText(level)
				if err != nil {
					return nil, err
				}
				levels[i] = text
			}
			criteria = levels
		default:
			return nil, errors.New("Gemini decisions do not support box questions")
		}
		if criteria != nil {
			question.Criteria, err = json.Marshal(criteria)
			if err != nil {
				return nil, err
			}
		}
		r.Questions[q.ID] = question
	}
	// Apply Google's own limits even when called through another API format.
	if _, err := r.ProviderInput(); err != nil {
		return nil, err
	}
	return r, nil
}

func decisionText(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	if parts, ok := v.([]provider.Content); ok {
		var text strings.Builder
		for _, part := range parts {
			if part.File != nil || part.ToolCall != nil || part.ToolResult != nil || part.Reasoning != nil {
				return "", errors.New("Gemini decision questions support text only")
			}
			text.WriteString(part.Text)
		}
		return text.String(), nil
	}
	data, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	var text string
	if json.Unmarshal(data, &text) == nil {
		return text, nil
	}
	return string(data), nil
}
