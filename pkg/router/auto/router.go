// Package auto routes a completion by asking a decision model which configured
// route best describes the user's task.
package auto

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"hash/maphash"
	"log/slog"
	"slices"
	"strings"
	"sync"
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

	// A turn whose decision has left the cache asks the decider again.
	decisionCacheSize = 4096
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

	// The decider's circuit uses the failure threshold and recovery timeout
	// configured for the routes.
	health := router.NewProviderStats()
	var failureThreshold int
	var recoveryTimeout time.Duration

	decisions := newDecisionCache(decisionCacheSize)

	pick := func(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions, candidates []int) (int, error) {
		state := requestEvidence(messages, options)
		if state.Request == "" {
			return candidates[0], nil
		}

		// One user turn spans many requests when the model calls tools. They
		// share a decision, so the turn stays on one model and is decided once.
		key := decisions.key(state)
		if index, ok := decisions.get(key); ok && slices.Contains(candidates, index) {
			return index, nil
		}

		acquired, probe := health.Acquire(recoveryTimeout)
		if !acquired {
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
		if err == nil {
			err = pickCtx.Err()
		}
		switch {
		case ctx.Err() != nil:
			health.Release(probe)
			return -1, ctx.Err()
		case errors.Is(err, provider.ErrRejected):
			health.Release(probe)
			return -1, err
		case err != nil:
			// Not remembered: the turn is decided once the decider answers.
			health.RecordFailure(failureThreshold, probe, err)
			slog.WarnContext(ctx, "auto router decision failed, using the first healthy route", "route", choices[candidates[0]].Label, "error", err)
			return candidates[0], nil
		}
		health.RecordSuccess(0, probe)

		index := candidates[0]
		if label := selectedModel(result); label != "" {
			for _, candidate := range candidates {
				if choices[candidate].Label == label {
					index = candidate
				}
			}
		}
		decisions.put(key, index)
		return index, nil
	}

	first := func(candidates []int, _ []*router.ProviderStats) int { return candidates[0] }
	// The routes are different models: what one rejects, another may accept.
	c, err := router.NewCompleter(members, first, append(slices.Clip(options), router.WithFirstPick(pick), router.WithDistinctMembers())...)
	if err != nil {
		return nil, err
	}
	failureThreshold, recoveryTimeout = c.Circuit()
	return c, nil
}

// decisionCache remembers the route of recent turns, least recently used first out.
type decisionCache struct {
	mu    sync.Mutex
	seed  maphash.Seed
	size  int
	order *list.List
	items map[uint64]*list.Element
}

type decisionEntry struct {
	key   uint64
	route int
}

func newDecisionCache(size int) *decisionCache {
	return &decisionCache{seed: maphash.MakeSeed(), size: size, order: list.New(), items: make(map[uint64]*list.Element)}
}

// key identifies a turn by what the decider is asked about it. The tool
// result counts are left out: they change with every step of the turn.
func (c *decisionCache) key(state evidence) uint64 {
	var h maphash.Hash
	h.SetSeed(c.seed)
	field := func(value string) {
		h.WriteString(value)
		h.WriteByte(0)
	}
	field(state.Request)
	for _, message := range state.Recent {
		field(string(message.Role))
		field(message.Text)
	}
	field(string(state.Effort))
	if state.ToolsAvailable {
		h.WriteByte(1)
	}
	return h.Sum64()
}

func (c *decisionCache) get(key uint64) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return 0, false
	}
	c.order.MoveToFront(element)
	return element.Value.(*decisionEntry).route, true
}

func (c *decisionCache) put(key uint64, route int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		element.Value.(*decisionEntry).route = route
		c.order.MoveToFront(element)
		return
	}
	c.items[key] = c.order.PushFront(&decisionEntry{key: key, route: route})
	if c.order.Len() > c.size {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*decisionEntry).key)
	}
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
	ToolResults       int             `json:"tool_results_since_request,omitzero"`
	FailedToolResults int             `json:"failed_tool_results_since_request,omitzero"`
	ToolsAvailable    bool            `json:"tools_available,omitzero"`
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

// omission marks the text left out of an excerpt.
const omission = "\n[...]\n"

// Only include conversational text and attachment markers. Tool payloads,
// private reasoning and binary content can be large or provider-specific.
// Text over the limit keeps its beginning and its end, where a request is
// usually stated.
func messageExcerpt(m provider.Message, limit int) string {
	var parts []string
	for _, part := range m.Content {
		text := part.Text
		if part.File != nil {
			text = "[file attached]"
			if strings.HasPrefix(part.File.ContentType, "image/") {
				text = "[image attached]"
			}
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if len(text) <= limit {
		return text
	}

	head := (limit - len(omission)) / 2
	tail := len(text) - (limit - len(omission) - head)
	for head > 0 && !utf8.RuneStart(text[head]) {
		head--
	}
	for tail < len(text) && !utf8.RuneStart(text[tail]) {
		tail++
	}
	return text[:head] + omission + text[tail:]
}
