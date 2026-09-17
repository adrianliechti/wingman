package provider

import (
	"reflect"
	"testing"
)

func TestResolveConfigurationUpdates(t *testing.T) {
	options := &CompleteOptions{ReasoningOptions: &ReasoningOptions{
		Effort: EffortHigh, IncludeSummary: true, IncludeSignature: true,
	}}
	messages := []Message{
		UserMessage("Plan"),
		{Content: []Content{ConfigurationUpdateContent(ConfigurationUpdate{ReasoningEffort: EffortMedium})}},
		AssistantMessage("Ready"),
		{Role: MessageRoleSystem, Content: []Content{
			ConfigurationUpdateContent(ConfigurationUpdate{ReasoningEffort: EffortLow}),
			TextContent("Keep it brief"),
		}},
		UserMessage("Continue"),
	}
	resolved, updated := ResolveConfigurationUpdates(messages, options)
	want := []Message{UserMessage("Plan"), AssistantMessage("Ready"), SystemMessage("Keep it brief"), UserMessage("Continue")}
	if !reflect.DeepEqual(resolved, want) {
		t.Fatalf("history changed: %#v", resolved)
	}
	if updated.ReasoningOptions.Effort != EffortLow || !updated.ReasoningOptions.IncludeSignature || !updated.ReasoningOptions.IncludeSummary {
		t.Fatalf("lost configuration: %+v", updated.ReasoningOptions)
	}
	if options.ReasoningOptions.Effort != EffortHigh || messages[1].Content[0].ConfigurationUpdate == nil || len(messages[3].Content) != 2 {
		t.Fatal("modified inputs needed for another provider's positional replay")
	}
	resolved, updated = ResolveConfigurationUpdates(messages[1:2], nil)
	if len(resolved) != 0 || updated.ReasoningOptions.Effort != EffortMedium {
		t.Fatalf("effort-only history: %v %+v", resolved, updated)
	}
}
