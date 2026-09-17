package provider

// ResolveConfigurationUpdates lowers conversation updates into request options
// for APIs without positional configuration. The latest effort controls the next
// response; cache behavior may differ from APIs that retain updates in history.
// Inputs are never modified, so the same history can be sent to another provider.
func ResolveConfigurationUpdates(messages []Message, options *CompleteOptions) ([]Message, *CompleteOptions) {
	if options == nil {
		options = new(CompleteOptions)
	}
	var result []Message
	var effort Effort
	for i, message := range messages {
		var content []Content
		updated := false
		for _, part := range message.Content {
			if part.ConfigurationUpdate == nil {
				content = append(content, part)
				continue
			}
			updated = true
			if value := part.ConfigurationUpdate.ReasoningEffort; value != "" {
				effort = value
			}
		}
		if updated && result == nil {
			result = append(make([]Message, 0, len(messages)), messages[:i]...)
		}
		if result != nil {
			if updated {
				message.Content = content
			}
			if !updated || len(content) > 0 {
				result = append(result, message)
			}
		}
	}
	if result == nil {
		return messages, options
	}
	if effort != "" {
		cloned := *options
		reasoning := ReasoningOptions{}
		if options.ReasoningOptions != nil {
			reasoning = *options.ReasoningOptions
		}
		reasoning.Effort = effort
		cloned.ReasoningOptions = &reasoning
		options = &cloned
	}
	return result, options
}
