package classifier

import (
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// maxLevel is the difficulty ceiling. Difficulty is reported on a 0..maxLevel
// scale and compared against each candidate's MaxDifficulty.
const maxLevel = 4

// signals are the cheap, local features extracted from a request. None require
// a network call.
type signals struct {
	hasImage        bool
	hasNonImageFile bool

	approxTokens int
	turnCount    int
	toolCount    int
	hasSchema    bool

	reasoningEffort provider.Effort

	codeFences int
	hardWords  int

	// queryText is the text used for embedding similarity / the judge prompt.
	queryText string
}

// hardWords are lexical markers of a demanding task. Substrings (not whole
// words) so "optimize"/"optimization", "architect"/"architecture" both hit.
var hardWords = []string{
	"prove", "algorithm", "optimi", "refactor", "architect", "debug",
	"step by step", "concurren", "distributed", "benchmark", "race condition",
	"design a", "migrate", "root cause", "trade-off", "tradeoff",
}

func extractSignals(messages []provider.Message, options *provider.CompleteOptions) signals {
	var s signals

	s.turnCount = len(messages)

	var sb strings.Builder

	for _, m := range messages {
		for _, content := range m.Content {
			if content.Text != "" {
				sb.WriteString(content.Text)
				sb.WriteByte('\n')

				s.approxTokens += len(content.Text) / 4
			}

			if content.File != nil {
				if isImageFile(content.File) {
					s.hasImage = true
				} else {
					s.hasNonImageFile = true
				}
			}

			if content.ToolResult != nil {
				for _, p := range content.ToolResult.Parts {
					if p.Text != "" {
						s.approxTokens += len(p.Text) / 4
					}

					if p.File != nil {
						if isImageFile(p.File) {
							s.hasImage = true
						} else {
							s.hasNonImageFile = true
						}
					}
				}
			}
		}
	}

	all := sb.String()
	lower := strings.ToLower(all)

	s.codeFences = strings.Count(all, "```")

	for _, w := range hardWords {
		if strings.Contains(lower, w) {
			s.hardWords++
		}
	}

	if options != nil {
		s.toolCount = len(options.Tools)
		s.hasSchema = options.Schema != nil

		if options.ReasoningOptions != nil {
			s.reasoningEffort = options.ReasoningOptions.Effort
		}
	}

	s.queryText = lastUserText(messages)

	if s.queryText == "" {
		s.queryText = strings.TrimSpace(all)
	}

	return s
}

func isImageFile(f *provider.File) bool {
	return f != nil && strings.HasPrefix(f.ContentType, "image/")
}

func lastUserText(messages []provider.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != provider.MessageRoleUser {
			continue
		}

		if t := strings.TrimSpace(messages[i].Text()); t != "" {
			return t
		}
	}

	return ""
}

// isEligible enforces the hard constraints a candidate must satisfy regardless
// of difficulty: vision for image inputs, and a context window large enough for
// the request.
func isEligible(c Candidate, s signals) bool {
	if s.hasImage && !c.Vision {
		return false
	}

	if c.MaxContext > 0 && s.approxTokens > c.MaxContext {
		return false
	}

	return true
}

// difficultyScore maps the signals to a continuous 0..maxLevel difficulty. The
// continuous value (not just the rounded level) drives the ambiguity check, so
// a task sitting near a candidate's MaxDifficulty boundary escalates.
func difficultyScore(s signals) float64 {
	score := 1.0

	switch s.reasoningEffort {
	case provider.EffortMinimal, provider.EffortLow:
		// no contribution
	case provider.EffortMedium:
		score += 1
	case provider.EffortHigh:
		score += 2
	case provider.EffortXHigh, provider.EffortMax:
		score += 3
	}

	if s.approxTokens > 8000 {
		score += 1
	}

	if s.approxTokens > 32000 {
		score += 1
	}

	if s.codeFences > 0 {
		score += 1
	}

	switch {
	case s.hardWords >= 2:
		score += 1
	case s.hardWords == 1:
		score += 0.5
	}

	if s.toolCount > 0 {
		score += 1
	}

	if s.turnCount > 8 {
		score += 1
	}

	if s.hasNonImageFile {
		score += 0.5
	}

	if score < 0 {
		score = 0
	}

	if score > maxLevel {
		score = maxLevel
	}

	return score
}

func roundLevel(score float64) int {
	level := int(score + 0.5)

	if level < 0 {
		level = 0
	}

	if level > maxLevel {
		level = maxLevel
	}

	return level
}
