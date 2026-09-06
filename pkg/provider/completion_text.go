package provider

// FinalMessageIndex selects the last explicit final answer, or the last
// unphased message when the provider does not label its output. Commentary
// alone is never a final answer. Pass only assistant message items.
func FinalMessageIndex(phases []MessagePhase) int {
	fallback := -1
	for i := len(phases) - 1; i >= 0; i-- {
		if phases[i] == MessagePhaseFinalAnswer {
			return i
		}
		if fallback < 0 && phases[i] == "" {
			fallback = i
		}
	}
	return fallback
}

// Text selects the final answer from an accumulated completion. Missing answers
// and selected refusals return empty text rather than an earlier draft.
// Message.Text reads message contents without selecting an answer.
func (c Completion) Text() string {
	if c.Message == nil {
		return ""
	}
	messages := c.Message.SplitMessages()
	phases := make([]MessagePhase, len(messages))
	for i, message := range messages {
		phases[i] = message.Phase
	}
	i := FinalMessageIndex(phases)
	if i < 0 || messages[i].Refusal() != "" {
		return ""
	}
	return messages[i].Text()
}
