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
func CompareSSEStructure(t *testing.T, expected, actual []*SSEEvent, rules map[string]FieldRule) {
	t.Helper()

	minLen := min(len(expected), len(actual))

	for i := range minLen {
		if expected[i].Data == nil || actual[i].Data == nil {
			continue
		}

		eventType := expected[i].Event
		if eventType == "" {
			if t, ok := expected[i].Data["type"].(string); ok {
				eventType = t
			}
		}

		CompareStructure(t, eventType, expected[i].Data, actual[i].Data, CompareOption{Rules: rules})
	}
}

func eventTypes(events []*SSEEvent) []string {
	types := make([]string, 0, len(events))
	for _, e := range events {
		name := e.Event
		if name == "" {
			if t, ok := e.Data["type"].(string); ok {
				name = t
			}
		}
		// Skip [DONE] sentinel
		if e.Raw == "[DONE]" {
			continue
		}
		types = append(types, name)
	}
	return types
}
