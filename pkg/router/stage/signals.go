package stage

import (
	"math"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const deepConversationMessages = 8

type toolCategory int

const (
	toolOther toolCategory = iota
	toolWrite
	toolEdit
	toolRead
	toolPlan
)

// Signals is the bounded, explainable feature set used for stage routing.
type Signals struct {
	Severity          float64 `json:"severity"`
	RecentWrites      int     `json:"recent_writes"`
	RecentEdits       int     `json:"recent_edits"`
	RecentReads       int     `json:"recent_reads"`
	RecentPlans       int     `json:"recent_plans"`
	RecentOther       int     `json:"recent_other"`
	RecentToolResults int     `json:"recent_tool_results"`
	Production        float64 `json:"production"`
	Exploring         bool    `json:"exploring"`
	Spinning          bool    `json:"spinning"`
	TestsPassed       bool    `json:"tests_passed"`
	Compacted         bool    `json:"compacted"`
	ConversationDepth int     `json:"conversation_depth"`
}

type observedCall struct {
	name      string
	arguments string
}

func extractSignals(messages []provider.Message, recentWindow int) Signals {
	var calls []observedCall
	var results []string
	signals := Signals{ConversationDepth: len(messages)}

	for _, message := range messages {
		for _, content := range message.Content {
			if content.Compaction != nil {
				signals.Compacted = true
			}
			if strings.Contains(strings.ToLower(content.Text), "session is being continued") {
				signals.Compacted = true
			}
			if content.ToolCall != nil {
				calls = append(calls, observedCall{name: content.ToolCall.Name, arguments: content.ToolCall.Arguments})
			}
			if content.ToolResult != nil {
				var text strings.Builder
				for _, part := range content.ToolResult.Parts {
					if part.Text == "" {
						continue
					}
					if text.Len() > 0 {
						text.WriteByte('\n')
					}
					text.WriteString(part.Text)
				}
				results = append(results, text.String())
			}
		}
	}

	callStart := max(0, len(calls)-recentWindow)
	for _, call := range calls[callStart:] {
		switch classifyTool(call) {
		case toolWrite:
			signals.RecentWrites++
		case toolEdit:
			signals.RecentEdits++
		case toolRead:
			signals.RecentReads++
		case toolPlan:
			signals.RecentPlans++
		default:
			signals.RecentOther++
		}
	}

	resultStart := max(0, len(results)-recentWindow)
	recentResults := results[resultStart:]
	signals.RecentToolResults = len(recentResults)
	for _, result := range recentResults {
		signals.Severity = math.Max(signals.Severity, errorSeverity(result))
		if cleanTestPass(result) {
			signals.TestsPassed = true
		}
	}

	recognized := signals.RecentWrites + signals.RecentEdits + signals.RecentReads + signals.RecentPlans
	production := signals.RecentWrites + signals.RecentEdits
	if recognized > 0 {
		signals.Production = float64(production) / float64(recognized)
	}

	deep := signals.ConversationDepth >= deepConversationMessages
	investigating := signals.RecentReads+signals.RecentPlans > 0
	signals.Exploring = deep && production == 0 && investigating
	signals.Spinning = deep && production == 0 && !investigating && signals.RecentOther > 0

	return signals
}

func classifyTool(call observedCall) toolCategory {
	name := strings.ToLower(call.name)
	arguments := strings.ToLower(call.arguments)

	if oneOf(name, "write", "create_file", "new_file", "write_file") {
		return toolWrite
	}
	if oneOf(name, "edit", "multiedit", "notebookedit", "str_replace", "str_replace_based_edit_tool", "text_editor", "patch", "apply_patch") {
		return toolEdit
	}
	if oneOf(name, "read", "view", "read_file", "search_files", "view_image") {
		return toolRead
	}
	if oneOf(name, "todowrite", "todo_write", "todo", "update_plan") {
		return toolPlan
	}

	if oneOf(name, "bash", "shell_command", "exec_command", "shell", "local_shell_call", "terminal") {
		if containsAny(arguments, "cat >", "cat >>", "echo >", "echo >>", "tee ", "printf >", "printf >>", "<<eof", "<<'eof'", "<< eof") {
			return toolWrite
		}
		if containsAny(arguments, "sed -i", "sed --in-place", "patch ", "perl -i", "perl -pi", "apply_patch") {
			return toolEdit
		}
		if containsAny(arguments, "cat ", "grep ", "rg ", "ls ", "find ", "head ", "tail ", "wc ", "diff ", "which ", "stat ", "file ") {
			return toolRead
		}
	}

	return toolOther
}

func errorSeverity(text string) float64 {
	lower := strings.ToLower(text)

	if containsAny(lower, "out of memory", "memoryerror", "cannot allocate memory", "connection refused", "econnrefused") {
		return 1
	}
	if containsAny(lower,
		"traceback (most recent call last)", "modulenotfounderror:", "importerror:", "no module named ",
		"command not found", "assertionerror", "valueerror:", "syntaxerror:", "timed out", "timeouterror",
		"timeout expired", "deadline exceeded", "filenotfounderror:", "no such file or directory", "file does not exist",
	) {
		return 0.7
	}
	if containsAny(lower, "exit code 1", "exit code 2", "exit status 1", "returned non-zero", "exited with code") {
		return 0.3
	}

	return 0
}

func cleanTestPass(text string) bool {
	lower := strings.ToLower(text)
	passed := containsAny(lower, "tests passed", "all tests passed", "passed in", "test result: ok", "test ok", "\nok ", "✓ ")
	if !passed || containsAny(lower, "assertionerror", "fatal:", "✗ ") {
		return false
	}

	fields := strings.Fields(lower)
	for i := 1; i < len(fields); i++ {
		word := strings.Trim(fields[i], ",.:;()[]")
		if !oneOf(word, "failed", "failure", "failures", "error", "errors") {
			continue
		}
		count, err := strconv.Atoi(strings.Trim(fields[i-1], ",.:;()[]"))
		if err == nil && count > 0 {
			return false
		}
	}

	return true
}

func oneOf(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
