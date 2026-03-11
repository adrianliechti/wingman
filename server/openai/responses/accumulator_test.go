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

// findEvents returns all events matching the given type
func findEvents(events []StreamEvent, eventType StreamEventType) []StreamEvent {
	var result []StreamEvent
	for _, e := range events {
		if e.Type == eventType {
			result = append(result, e)
		}
	}
	return result
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

// toolCallChunk creates a completion chunk for a function call.
// Pass a non-empty id+name for the opening chunk, then id+arguments for delta chunks.
func toolCallChunk(id, name, arguments string) provider.Completion {
	return provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        id,
					Name:      name,
					Arguments: arguments,
				}),
			},
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

func TestStreamingAccumulatorSignatureOnlyReasoning(t *testing.T) {
	acc, events := newTestAccumulator()

	require.NoError(t, acc.Add(reasoningChunk("", "", "encrypted_signature_data")))
	require.NoError(t, acc.Complete())

	reasoningAddedEvent := findEvent(*events, StreamEventReasoningItemAdded)
	require.NotNil(t, reasoningAddedEvent, "should have reasoning_item.added event")

	reasoningItemDoneEvent := findEvent(*events, StreamEventReasoningItemDone)
	require.NotNil(t, reasoningItemDoneEvent)
	require.Equal(t, "encrypted_signature_data", reasoningItemDoneEvent.ReasoningSignature)
	require.Equal(t, reasoningAddedEvent.OutputIndex, reasoningItemDoneEvent.OutputIndex)
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

func TestStreamingAccumulatorSingleToolCall(t *testing.T) {
	acc, events := newTestAccumulator()

	require.NoError(t, acc.Add(toolCallChunk("call_1", "get_weather", "")))
	require.NoError(t, acc.Add(toolCallChunk("call_1", "", `{"city":`)))
	require.NoError(t, acc.Add(toolCallChunk("call_1", "", `"London"}`)))
	require.NoError(t, acc.Complete())

	// function_call.added with name
	addedEvent := findEvent(*events, StreamEventFunctionCallAdded)
	require.NotNil(t, addedEvent)
	require.Equal(t, "call_1", addedEvent.ToolCallID)
	require.Equal(t, "get_weather", addedEvent.ToolCallName)

	// two function_call_arguments.delta events
	argDeltas := findEvents(*events, StreamEventFunctionCallArgumentsDelta)
	require.Len(t, argDeltas, 2)
	require.Equal(t, `{"city":`, argDeltas[0].Delta)
	require.Equal(t, `"London"}`, argDeltas[1].Delta)

	// function_call_arguments.done with full accumulated arguments
	argsDone := findEvent(*events, StreamEventFunctionCallArgumentsDone)
	require.NotNil(t, argsDone)
	require.Equal(t, "call_1", argsDone.ToolCallID)
	require.Equal(t, `{"city":"London"}`, argsDone.Arguments)

	// function_call.done
	callDone := findEvent(*events, StreamEventFunctionCallDone)
	require.NotNil(t, callDone)
	require.Equal(t, "call_1", callDone.ToolCallID)
	require.Equal(t, "get_weather", callDone.ToolCallName)
	require.Equal(t, `{"city":"London"}`, callDone.Arguments)
}

func TestStreamingAccumulatorParallelToolCalls(t *testing.T) {
	acc, events := newTestAccumulator()

	// Two interleaved tool calls (ID distinguishes them)
	require.NoError(t, acc.Add(toolCallChunk("call_1", "get_weather", "")))
	require.NoError(t, acc.Add(toolCallChunk("call_1", "", `{"city":"London"}`)))
	require.NoError(t, acc.Add(toolCallChunk("call_2", "get_calendar", "")))
	require.NoError(t, acc.Add(toolCallChunk("call_2", "", `{"date":"today"}`)))
	require.NoError(t, acc.Complete())

	addedEvents := findEvents(*events, StreamEventFunctionCallAdded)
	require.Len(t, addedEvents, 2)
	require.Equal(t, "call_1", addedEvents[0].ToolCallID)
	require.Equal(t, "call_2", addedEvents[1].ToolCallID)

	// Each call gets a unique output index
	require.NotEqual(t, addedEvents[0].OutputIndex, addedEvents[1].OutputIndex)

	doneEvents := findEvents(*events, StreamEventFunctionCallDone)
	require.Len(t, doneEvents, 2)

	argsDoneEvents := findEvents(*events, StreamEventFunctionCallArgumentsDone)
	require.Len(t, argsDoneEvents, 2)
	require.Equal(t, `{"city":"London"}`, argsDoneEvents[0].Arguments)
	require.Equal(t, `{"date":"today"}`, argsDoneEvents[1].Arguments)
}

func TestStreamingAccumulatorToolCallOutputIndex(t *testing.T) {
	acc, events := newTestAccumulator()

	// Tool call should start at output_index 0 (no reasoning or message before it)
	require.NoError(t, acc.Add(toolCallChunk("call_1", "get_weather", `{}`)))
	require.NoError(t, acc.Complete())

	addedEvent := findEvent(*events, StreamEventFunctionCallAdded)
	require.NotNil(t, addedEvent)
	require.Equal(t, 0, addedEvent.OutputIndex)

	doneEvent := findEvent(*events, StreamEventFunctionCallDone)
	require.NotNil(t, doneEvent)
	require.Equal(t, 0, doneEvent.OutputIndex)
}

func TestStreamingAccumulatorEventOrdering_TextFlow(t *testing.T) {
	acc, events := newTestAccumulator()

	require.NoError(t, acc.Add(textChunk("Hello")))
	require.NoError(t, acc.Complete())

	types := make([]StreamEventType, len(*events))
	for i, e := range *events {
		types[i] = e.Type
	}

	// Verify canonical ordering
	require.Equal(t, []StreamEventType{
		StreamEventResponseCreated,
		StreamEventResponseInProgress,
		StreamEventOutputItemAdded,
		StreamEventContentPartAdded,
		StreamEventTextDelta,
		StreamEventTextDone,
		StreamEventContentPartDone,
		StreamEventOutputItemDone,
		StreamEventResponseCompleted,
	}, types)
}

func TestStreamingAccumulatorIncompleteTerminalEvent(t *testing.T) {
	acc, events := newTestAccumulator()

	require.NoError(t, acc.Add(provider.Completion{
		Status: provider.CompletionStatusIncomplete,
		Usage: &provider.Usage{
			InputTokens:  3,
			OutputTokens: 5,
		},
	}))
	require.NoError(t, acc.Complete())

	require.Nil(t, findEvent(*events, StreamEventResponseCompleted))
	incompleteEvent := findEvent(*events, StreamEventResponseIncomplete)
	require.NotNil(t, incompleteEvent, "should have response.incomplete event")
	require.NotNil(t, incompleteEvent.Completion)
	require.Equal(t, provider.CompletionStatusIncomplete, incompleteEvent.Completion.Status)
	require.NotNil(t, incompleteEvent.Completion.Usage)
	require.Equal(t, 3, incompleteEvent.Completion.Usage.InputTokens)
	require.Equal(t, 5, incompleteEvent.Completion.Usage.OutputTokens)
}

// ── apply_patch accumulator tests ────────────────────────────────────────────

func TestStreamingAccumulatorApplyPatchCall(t *testing.T) {
	acc, events := newTestAccumulator()

	// First chunk: tool call with ID and name
	require.NoError(t, acc.Add(toolCallChunk("call_1", "apply_patch", "")))
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
	require.NoError(t, acc.Add(toolCallChunk("call_1", "apply_patch", "patch content")))
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
	require.NoError(t, acc.Add(toolCallChunk("call_patch", "apply_patch", "patch data")))
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
