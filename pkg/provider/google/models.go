package google

import "strings"

// LiveThinkingModels require an explicit thinking level. Entries match model
// families, including models/ prefixes and dated variants.
var LiveThinkingModels = []string{
	"gemini-3.8-live-extended-thinking",
}

var LiveTranscriptionModels = []string{
	"transcribe-live",
}

var AffectiveDialogModels = []string{
	"gemini-2.5-flash-native-audio",
	"gemini-2.5-flash-live",
}

// ClientContentInterruptModels support cancellation with an empty clientContent
// update. This is distinct from affective dialog and native voice barge-in.
var ClientContentInterruptModels = []string{
	"gemini-2.5-flash-native-audio",
	"gemini-2.5-flash-live",
}

func matchesModel(model string, patterns []string) bool {
	model = strings.ToLower(model)
	for _, pattern := range patterns {
		if strings.Contains(model, pattern) {
			return true
		}
	}
	return false
}
