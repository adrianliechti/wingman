package harness

import (
	"testing"
)

// CompareSSEEventTypes checks that both streams emitted the same sequence of event types.
func CompareSSEEventTypes(t *testing.T, expected, actual []*SSEEvent) {
	t.Helper()

	expectedTypes := eventTypes(expected)
	actualTypes := eventTypes(actual)

	if len(expectedTypes) != len(actualTypes) {
		t.Errorf("SSE event count mismatch: expected %d events %v, actual %d events %v",
			len(expectedTypes), expectedTypes, len(actualTypes), actualTypes)
		return
	}

	for i := range expectedTypes {
		if expectedTypes[i] != actualTypes[i] {
			t.Errorf("SSE event type mismatch at index %d: expected %q, actual %q",
				i, expectedTypes[i], actualTypes[i])
		}
	}
}

// CompareSSEStructure compares the structural shape of matching SSE events.
// For streams with the same number of events, it compares index-by-index.
func CompareSSEStructure(t *testing.T, expected, actual []*SSEEvent, rules map[string]FieldRule) {
	t.Helper()

	minLen := min(len(expected), len(actual))

	for i := range minLen {
		if expected[i].Data == nil || actual[i].Data == nil {
			continue
		}

		eventType := eventName(expected[i])

		CompareStructure(t, eventType, expected[i].Data, actual[i].Data, CompareOption{Rules: rules})
	}
}

// CompareSSEStructureByType compares the structural shape of SSE events grouped by type.
// For each unique event type, it compares the first occurrence from each stream.
// This is useful when delta counts differ between endpoints.
func CompareSSEStructureByType(t *testing.T, expected, actual []*SSEEvent, rules map[string]FieldRule) {
	t.Helper()

	expectedByType := firstEventByType(expected)
	actualByType := firstEventByType(actual)

	for eventType, expEvent := range expectedByType {
		actEvent, ok := actualByType[eventType]
		if !ok {
			continue // pattern comparison already caught missing types
		}

		if expEvent.Data == nil || actEvent.Data == nil {
			continue
		}

		CompareStructure(t, eventType, expEvent.Data, actEvent.Data, CompareOption{Rules: rules})
	}
}

func firstEventByType(events []*SSEEvent) map[string]*SSEEvent {
	m := make(map[string]*SSEEvent)
	for _, e := range events {
		if e.Raw == "[DONE]" {
			continue
		}
		name := eventName(e)
		if _, ok := m[name]; !ok {
			m[name] = e
		}
	}
	return m
}

// CompareSSEEventPattern checks that both streams emitted the same pattern of event types,
// collapsing consecutive runs of delta events into a single entry. This allows comparison
// when the number of delta chunks differs between endpoints.
func CompareSSEEventPattern(t *testing.T, expected, actual []*SSEEvent) {
	t.Helper()

	expectedPattern := eventPattern(expected)
	actualPattern := eventPattern(actual)

	if len(expectedPattern) != len(actualPattern) {
		t.Errorf("SSE event pattern mismatch:\n  expected: %v\n  actual:   %v",
			expectedPattern, actualPattern)
		return
	}

	for i := range expectedPattern {
		if expectedPattern[i] != actualPattern[i] {
			t.Errorf("SSE event pattern mismatch at index %d: expected %q, actual %q\n  full expected: %v\n  full actual:   %v",
				i, expectedPattern[i], actualPattern[i], expectedPattern, actualPattern)
		}
	}
}

func eventTypes(events []*SSEEvent) []string {
	types := make([]string, 0, len(events))
	for _, e := range events {
		name := eventName(e)
		if e.Raw == "[DONE]" {
			continue
		}
		types = append(types, name)
	}
	return types
}

// eventPattern returns event types with consecutive duplicates collapsed.
func eventPattern(events []*SSEEvent) []string {
	var pattern []string
	var prev string

	for _, e := range events {
		if e.Raw == "[DONE]" {
			continue
		}

		name := eventName(e)
		if name != prev {
			pattern = append(pattern, name)
			prev = name
		}
	}

	return pattern
}

func eventName(e *SSEEvent) string {
	name := e.Event
	if name == "" {
		if t, ok := e.Data["type"].(string); ok {
			name = t
		}
	}
	return name
}
