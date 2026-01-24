package responses

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/stretchr/testify/require"
)

// findEvent returns the first event matching the given type
func findEvent(events []StreamEvent, eventType StreamEventType) *StreamEvent {
	for i := range events {
		if events[i].Type == eventType {
			return &events[i]
		}
	}
	return nil
}

// newTestAccumulator creates an accumulator that collects events
func newTestAccumulator() (*StreamingAccumulator, *[]StreamEvent) {
	events := &[]StreamEvent{}
	acc := NewStreamingAccumulator(func(event StreamEvent) error {
		*events = append(*events, event)
		return nil
	})
	return acc, events
}

// textChunk creates a completion chunk with text content
func textChunk(text string) provider.Completion {
	return provider.Completion{
		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: []provider.Content{{Text: text}},
		},
	}
}

func TestStreamingAccumulatorTextTracking(t *testing.T) {
	acc, events := newTestAccumulator()

	require.NoError(t, acc.Add(textChunk("Hello")))
	require.NoError(t, acc.Add(textChunk(" world!")))
	require.NoError(t, acc.Complete())

	completedEvent := findEvent(*events, StreamEventResponseCompleted)
	require.NotNil(t, completedEvent, "should have response.completed event")
	require.Equal(t, "Hello world!", completedEvent.Text)
}

func TestStreamingAccumulatorEmptyFinalChunk(t *testing.T) {
	acc, events := newTestAccumulator()

	// First chunk with text
	require.NoError(t, acc.Add(textChunk("Hello!")))

	// Final chunk with NO text (simulates stop event from some providers)
	require.NoError(t, acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
		},
	}))
	require.NoError(t, acc.Complete())

	completedEvent := findEvent(*events, StreamEventResponseCompleted)
	require.NotNil(t, completedEvent)
	require.Equal(t, "Hello!", completedEvent.Text, "should preserve text even when final chunk is empty")
}

func TestStreamingAccumulatorTextDoneHasText(t *testing.T) {
	acc, events := newTestAccumulator()

	require.NoError(t, acc.Add(textChunk("Test")))
	require.NoError(t, acc.Complete())

	textDoneEvent := findEvent(*events, StreamEventTextDone)
	require.NotNil(t, textDoneEvent)
	require.Equal(t, "Test", textDoneEvent.Text)
}

func TestStreamingAccumulatorOutputItemDoneHasText(t *testing.T) {
	acc, events := newTestAccumulator()

	require.NoError(t, acc.Add(textChunk("Test")))
	require.NoError(t, acc.Complete())

	outputItemDoneEvent := findEvent(*events, StreamEventOutputItemDone)
	require.NotNil(t, outputItemDoneEvent)
	require.Equal(t, "Test", outputItemDoneEvent.Text)
}
