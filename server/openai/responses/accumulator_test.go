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

// reasoningChunk creates a completion chunk with reasoning content
func reasoningChunk(text, summary, signature string) provider.Completion {
	return provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{
					Text:      text,
					Summary:   summary,
					Signature: signature,
				}),
			},
		},
	}
}

func TestStreamingAccumulatorReasoningText(t *testing.T) {
	acc, events := newTestAccumulator()

	require.NoError(t, acc.Add(reasoningChunk("thinking step 1", "", "")))
	require.NoError(t, acc.Add(reasoningChunk(" thinking step 2", "", "")))
	require.NoError(t, acc.Complete())

	// Should emit reasoning_item.added
	reasoningAddedEvent := findEvent(*events, StreamEventReasoningItemAdded)
	require.NotNil(t, reasoningAddedEvent, "should have reasoning_item.added event")
	require.NotEmpty(t, reasoningAddedEvent.ReasoningID)

	// Should emit reasoning_text.delta for each chunk
	var textDeltas []StreamEvent
	for _, e := range *events {
		if e.Type == StreamEventReasoningTextDelta {
			textDeltas = append(textDeltas, e)
		}
	}
	require.Len(t, textDeltas, 2, "should have 2 reasoning text delta events")
	require.Equal(t, "thinking step 1", textDeltas[0].Delta)
	require.Equal(t, " thinking step 2", textDeltas[1].Delta)

	// Should emit reasoning_text.done with full text
	reasoningTextDoneEvent := findEvent(*events, StreamEventReasoningTextDone)
	require.NotNil(t, reasoningTextDoneEvent)
	require.Equal(t, "thinking step 1 thinking step 2", reasoningTextDoneEvent.ReasoningText)

	// Should emit reasoning_item.done
	reasoningItemDoneEvent := findEvent(*events, StreamEventReasoningItemDone)
	require.NotNil(t, reasoningItemDoneEvent)
	require.Equal(t, "thinking step 1 thinking step 2", reasoningItemDoneEvent.ReasoningText)
}

func TestStreamingAccumulatorReasoningSummary(t *testing.T) {
	acc, events := newTestAccumulator()

	require.NoError(t, acc.Add(reasoningChunk("", "summary part 1", "")))
	require.NoError(t, acc.Add(reasoningChunk("", " summary part 2", "")))
	require.NoError(t, acc.Complete())

	// Should emit reasoning_item.added
	reasoningAddedEvent := findEvent(*events, StreamEventReasoningItemAdded)
	require.NotNil(t, reasoningAddedEvent, "should have reasoning_item.added event")

	// Should emit reasoning_summary_text.delta for each chunk
	var summaryDeltas []StreamEvent
	for _, e := range *events {
		if e.Type == StreamEventReasoningSummaryDelta {
			summaryDeltas = append(summaryDeltas, e)
		}
	}
	require.Len(t, summaryDeltas, 2, "should have 2 reasoning summary delta events")
	require.Equal(t, "summary part 1", summaryDeltas[0].Delta)
	require.Equal(t, " summary part 2", summaryDeltas[1].Delta)

	// Should emit reasoning_summary_text.done with full summary
	reasoningSummaryDoneEvent := findEvent(*events, StreamEventReasoningSummaryDone)
	require.NotNil(t, reasoningSummaryDoneEvent)
	require.Equal(t, "summary part 1 summary part 2", reasoningSummaryDoneEvent.ReasoningSummary)

	// Should emit reasoning_item.done with summary
	reasoningItemDoneEvent := findEvent(*events, StreamEventReasoningItemDone)
	require.NotNil(t, reasoningItemDoneEvent)
	require.Equal(t, "summary part 1 summary part 2", reasoningItemDoneEvent.ReasoningSummary)
}

func TestStreamingAccumulatorReasoningSignature(t *testing.T) {
	acc, events := newTestAccumulator()

	// Signature is typically sent with the final reasoning chunk
	require.NoError(t, acc.Add(reasoningChunk("thinking", "", "")))
	require.NoError(t, acc.Add(reasoningChunk("", "summary", "encrypted_signature_data")))
	require.NoError(t, acc.Complete())

	// Should emit reasoning_item.done with signature
	reasoningItemDoneEvent := findEvent(*events, StreamEventReasoningItemDone)
	require.NotNil(t, reasoningItemDoneEvent)
	require.Equal(t, "encrypted_signature_data", reasoningItemDoneEvent.ReasoningSignature)
}

func TestStreamingAccumulatorReasoningAndText(t *testing.T) {
	acc, events := newTestAccumulator()

	// First: reasoning content
	require.NoError(t, acc.Add(reasoningChunk("let me think...", "", "")))
	require.NoError(t, acc.Add(reasoningChunk("", "brief summary", "sig123")))

	// Then: regular text content
	require.NoError(t, acc.Add(textChunk("The answer is 42.")))

	require.NoError(t, acc.Complete())

	// Should have both reasoning and text events
	reasoningAddedEvent := findEvent(*events, StreamEventReasoningItemAdded)
	require.NotNil(t, reasoningAddedEvent, "should have reasoning_item.added")

	outputItemAddedEvent := findEvent(*events, StreamEventOutputItemAdded)
	require.NotNil(t, outputItemAddedEvent, "should have output_item.added for message")

	// Check reasoning done
	reasoningItemDoneEvent := findEvent(*events, StreamEventReasoningItemDone)
	require.NotNil(t, reasoningItemDoneEvent)
	require.Equal(t, "let me think...", reasoningItemDoneEvent.ReasoningText)
	require.Equal(t, "brief summary", reasoningItemDoneEvent.ReasoningSummary)
	require.Equal(t, "sig123", reasoningItemDoneEvent.ReasoningSignature)

	// Check text done
	textDoneEvent := findEvent(*events, StreamEventTextDone)
	require.NotNil(t, textDoneEvent)
	require.Equal(t, "The answer is 42.", textDoneEvent.Text)

	// Check response.completed has text
	completedEvent := findEvent(*events, StreamEventResponseCompleted)
	require.NotNil(t, completedEvent)
	require.Equal(t, "The answer is 42.", completedEvent.Text)
}

func TestStreamingAccumulatorReasoningOutputIndex(t *testing.T) {
	acc, events := newTestAccumulator()

	// Reasoning comes first, should get output_index 0
	require.NoError(t, acc.Add(reasoningChunk("thinking", "", "")))
	// Text comes second, should get output_index 1
	require.NoError(t, acc.Add(textChunk("response")))
	require.NoError(t, acc.Complete())

	reasoningAddedEvent := findEvent(*events, StreamEventReasoningItemAdded)
	require.NotNil(t, reasoningAddedEvent)
	require.Equal(t, 0, reasoningAddedEvent.OutputIndex, "reasoning should be at output_index 0")

	reasoningItemDoneEvent := findEvent(*events, StreamEventReasoningItemDone)
	require.NotNil(t, reasoningItemDoneEvent)
	require.Equal(t, 0, reasoningItemDoneEvent.OutputIndex)

	// Message should be at output_index 1
	outputItemAddedEvent := findEvent(*events, StreamEventOutputItemAdded)
	require.NotNil(t, outputItemAddedEvent)
	require.Equal(t, 1, outputItemAddedEvent.OutputIndex, "message should be at output_index 1 when reasoning is at 0")

	// Text delta should reference the message's output_index
	textDeltaEvent := findEvent(*events, StreamEventTextDelta)
	require.NotNil(t, textDeltaEvent)
	require.Equal(t, 1, textDeltaEvent.OutputIndex, "text delta should reference output_index 1")

	// Output item done should reference the message's output_index
	outputItemDoneEvent := findEvent(*events, StreamEventOutputItemDone)
	require.NotNil(t, outputItemDoneEvent)
	require.Equal(t, 1, outputItemDoneEvent.OutputIndex, "output_item.done should reference output_index 1")
}

// applyPatchCallChunk creates a completion chunk with an apply_patch tool call
func applyPatchCallChunk(id, name, args string) provider.Completion {
	return provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        id,
					Name:      name,
					Arguments: args,
				}),
			},
		},
	}
}

func TestStreamingAccumulatorApplyPatchCall(t *testing.T) {
	acc, events := newTestAccumulator()

	// First chunk: tool call with ID and name
	require.NoError(t, acc.Add(applyPatchCallChunk("call_1", "apply_patch", "")))
	// Second chunk: patch content delta
	require.NoError(t, acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					Arguments: "--- a/main.py\n",
				}),
			},
		},
	}))
	// Third chunk: more patch content
	require.NoError(t, acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					Arguments: "+++ b/main.py\n",
				}),
			},
		},
	}))
	require.NoError(t, acc.Complete())

	// Should emit apply_patch_call.added (not function_call.added)
	applyPatchAddedEvent := findEvent(*events, StreamEventApplyPatchCallAdded)
	require.NotNil(t, applyPatchAddedEvent, "should have apply_patch_call.added event")
	require.Equal(t, "call_1", applyPatchAddedEvent.ToolCallID)

	// Should NOT emit function_call.added
	functionCallAddedEvent := findEvent(*events, StreamEventFunctionCallAdded)
	require.Nil(t, functionCallAddedEvent, "should not have function_call.added for apply_patch")

	// Should emit apply_patch_call.patch.delta events
	var patchDeltas []StreamEvent
	for _, e := range *events {
		if e.Type == StreamEventApplyPatchCallPatchDelta {
			patchDeltas = append(patchDeltas, e)
		}
	}
	require.Len(t, patchDeltas, 2)
	require.Equal(t, "--- a/main.py\n", patchDeltas[0].Delta)
	require.Equal(t, "+++ b/main.py\n", patchDeltas[1].Delta)

	// Should emit apply_patch_call.patch.done
	patchDoneEvent := findEvent(*events, StreamEventApplyPatchCallPatchDone)
	require.NotNil(t, patchDoneEvent)
	require.Equal(t, "--- a/main.py\n+++ b/main.py\n", patchDoneEvent.Arguments)

	// Should emit apply_patch_call.done
	applyPatchDoneEvent := findEvent(*events, StreamEventApplyPatchCallDone)
	require.NotNil(t, applyPatchDoneEvent)
	require.Equal(t, "call_1", applyPatchDoneEvent.ToolCallID)
}

func TestStreamingAccumulatorApplyPatchCallOutputIndex(t *testing.T) {
	acc, events := newTestAccumulator()

	// Text first
	require.NoError(t, acc.Add(textChunk("Let me fix that.")))
	// Then apply_patch call
	require.NoError(t, acc.Add(applyPatchCallChunk("call_1", "apply_patch", "patch content")))
	require.NoError(t, acc.Complete())

	// Message should be at output_index 0
	outputItemAddedEvent := findEvent(*events, StreamEventOutputItemAdded)
	require.NotNil(t, outputItemAddedEvent)
	require.Equal(t, 0, outputItemAddedEvent.OutputIndex)

	// Apply patch call should be at output_index 1
	applyPatchAddedEvent := findEvent(*events, StreamEventApplyPatchCallAdded)
	require.NotNil(t, applyPatchAddedEvent)
	require.Equal(t, 1, applyPatchAddedEvent.OutputIndex)
}

func TestStreamingAccumulatorMixedFunctionAndApplyPatch(t *testing.T) {
	acc, events := newTestAccumulator()

	// Function call
	require.NoError(t, acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        "call_func",
					Name:      "get_weather",
					Arguments: `{"location":"London"}`,
				}),
			},
		},
	}))
	// Apply patch call
	require.NoError(t, acc.Add(applyPatchCallChunk("call_patch", "apply_patch", "patch data")))
	require.NoError(t, acc.Complete())

	// Should have function_call.added for the function call
	functionCallAddedEvent := findEvent(*events, StreamEventFunctionCallAdded)
	require.NotNil(t, functionCallAddedEvent)
	require.Equal(t, "call_func", functionCallAddedEvent.ToolCallID)
	require.Equal(t, "get_weather", functionCallAddedEvent.ToolCallName)

	// Should have apply_patch_call.added for the patch call
	applyPatchAddedEvent := findEvent(*events, StreamEventApplyPatchCallAdded)
	require.NotNil(t, applyPatchAddedEvent)
	require.Equal(t, "call_patch", applyPatchAddedEvent.ToolCallID)

	// Function call done should use function events
	functionCallDoneEvent := findEvent(*events, StreamEventFunctionCallDone)
	require.NotNil(t, functionCallDoneEvent)
	require.Equal(t, "call_func", functionCallDoneEvent.ToolCallID)

	// Apply patch done should use apply_patch events
	applyPatchDoneEvent := findEvent(*events, StreamEventApplyPatchCallDone)
	require.NotNil(t, applyPatchDoneEvent)
	require.Equal(t, "call_patch", applyPatchDoneEvent.ToolCallID)
}
