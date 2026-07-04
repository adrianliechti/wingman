package classifier

import (
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// maxLevel is the difficulty ceiling. Difficulty is reported on a 0..maxLevel
// scale and compared against each candidate's MaxDifficulty.
const maxLevel = 4

// historyWeight discounts lexical signals from earlier turns relative to the
// current user message, so a conversation de-escalates once the demanding part
// is over instead of being pinned to its hardest historical turn.
const historyWeight = 0.5

// historyScanBytes caps how much conversation text the lexical pass reads
// (newest-first). The signals are presence-based and saturate quickly, so
// scanning megabytes of old turns buys nothing.
const historyScanBytes = 64 * 1024

// triggerBoost is the score delta applied when the user explicitly asks for
// depth (escalateWords) or brevity (deescalateWords) in the current message.
const triggerBoost = 1.5

// signals are the cheap, local features extracted from a request. None require
// a network call.
type signals struct {
	hasImage        bool
	hasNonImageFile bool

	// approxTokens covers all messages and drives the MaxContext hard
	// constraint. taskTokens excludes system messages, so a large static
	// system prompt (agent platforms routinely ship 30KB+ of instructions and
	// tool guidance) doesn't inflate the difficulty of a trivial user turn.
	approxTokens int
	taskTokens   int

	toolCount int

	reasoningEffort provider.Effort

	// recent* is extracted from the current (last non-empty) user message and
	// scores at full weight; history* covers the earlier non-system turns and
	// is discounted by historyWeight.
	recentFences  int
	historyFences int
	recentHard    int
	historyHard   int

	// escalate/deescalate reflect explicit user cues in the current message
	// ("think hard" / "quick question") and override the lexical estimate in
	// their direction.
	escalate   bool
	deescalate bool

	// queryText is the text used for embedding similarity / the judge prompt.
	queryText string
}

// hardWords are lexical markers of a demanding task. Entries match at word
// starts only ("prove" must not hit "improve") but remain prefixes, so
// "architect"/"architecture" and "concurrent"/"concurrency" both hit.
var hardWords = []string{
	"prove", "algorithm", "optimiz", "optimise", "refactor", "architect",
	"debug", "step by step", "concurren", "distributed", "benchmark",
	"race condition", "design a", "migrate", "root cause", "trade-off",
	"tradeoff",
}

// escalateWords are explicit user requests for depth or rigor.
var escalateWords = []string{
	"think hard", "think deeply", "think carefully", "be thorough",
	"in depth", "in-depth", "deep dive", "comprehensive", "meticulous",
	"double-check", "take your time",
}

// deescalateWords are explicit user requests for a quick, light answer.
var deescalateWords = []string{
	"quick question", "quickly", "briefly", "in short", "short answer",
	"one sentence", "one-liner", "tl;dr", "tldr", "keep it simple",
	"simple answer", "no details", "just tell me",
}

func extractSignals(messages []provider.Message, options *provider.CompleteOptions) signals {
	var s signals

	recentIndex := -1

	var recent string

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != provider.MessageRoleUser {
			continue
		}

		if t := strings.TrimSpace(messages[i].Text()); t != "" {
			recentIndex = i
			recent = t

			break
		}
	}

	for _, m := range messages {
		system := m.Role == provider.MessageRoleSystem

		for _, content := range m.Content {
			if content.Text != "" {
				tokens := len(content.Text) / 4
				s.approxTokens += tokens

				if !system {
					s.taskTokens += tokens
				}
			}

			if content.File != nil {
				if isImageFile(content.File) {
					s.hasImage = true
				} else if !system {
					s.hasNonImageFile = true
				}
			}

			if content.ToolResult != nil {
				for _, p := range content.ToolResult.Parts {
					if p.Text != "" {
						tokens := len(p.Text) / 4
						s.approxTokens += tokens
						s.taskTokens += tokens
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

	recentLower := strings.ToLower(recent)

	s.recentFences = strings.Count(recent, "```")
	s.recentHard = countWords(recentLower, hardWords)

	s.escalate = countWords(recentLower, escalateWords) > 0
	s.deescalate = countWords(recentLower, deescalateWords) > 0

	// Lexical history pass: newest turns first, bounded, counting each hard
	// word once across the whole history.
	matched := make([]bool, len(hardWords))
	budget := historyScanBytes

	for i := len(messages) - 1; i >= 0 && budget > 0; i-- {
		if i == recentIndex || messages[i].Role == provider.MessageRoleSystem {
			continue
		}

		for _, content := range messages[i].Content {
			if content.Text == "" || budget <= 0 {
				continue
			}

			text := content.Text

			if len(text) > budget {
				text = text[:budget]
			}

			budget -= len(text)

			s.historyFences += strings.Count(text, "```")

			lower := strings.ToLower(text)

			for w := range hardWords {
				if !matched[w] && containsWord(lower, hardWords[w]) {
					matched[w] = true
					s.historyHard++
				}
			}
		}
	}

	if options != nil {
		s.toolCount = len(options.Tools)

		if options.ReasoningOptions != nil {
			s.reasoningEffort = options.ReasoningOptions.Effort
		}
	}

	s.queryText = recent

	if s.queryText == "" {
		var sb strings.Builder

		for _, m := range messages {
			if m.Role == provider.MessageRoleSystem {
				continue
			}

			if t := strings.TrimSpace(m.Text()); t != "" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}

				sb.WriteString(t)
			}

			if sb.Len() >= maxQueryChars {
				break
			}
		}

		s.queryText = truncateText(sb.String(), maxQueryChars)
	}

	return s
}

// countWords returns how many of words occur in text at a word start.
func countWords(text string, words []string) int {
	count := 0

	for _, w := range words {
		if containsWord(text, w) {
			count++
		}
	}

	return count
}

// containsWord reports whether text contains word beginning at a word
// boundary, so "prove" doesn't match inside "improve" while prefixes like
// "architect" in "architecture" still do.
func containsWord(text, word string) bool {
	from := 0

	for {
		i := strings.Index(text[from:], word)

		if i < 0 {
			return false
		}

		i += from

		if i == 0 || !isWordByte(text[i-1]) {
			return true
		}

		from = i + 1
	}
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

func isImageFile(f *provider.File) bool {
	return f != nil && strings.HasPrefix(f.ContentType, "image/")
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
// current user message dominates: history contributes at historyWeight so the
// estimate follows the conversation both up and down, and explicit user cues
// (escalate/deescalate) shift the result in their direction. The continuous
// value (not just the rounded level) drives the ambiguity check, so a task
// sitting near a candidate's MaxDifficulty boundary escalates.
func difficultyScore(s signals) float64 {
	score := 1.0

	// Medium is the default many SDKs attach to every request, so it carries
	// half the weight of a deliberate high/xhigh choice.
	switch s.reasoningEffort {
	case provider.EffortMinimal, provider.EffortLow:
		// no contribution
	case provider.EffortMedium:
		score += 0.5
	case provider.EffortHigh:
		score += 2
	case provider.EffortXHigh, provider.EffortMax:
		score += 3
	}

	// Context size is a weak difficulty signal but a strong cost multiplier:
	// input is billed per token, so overweighting size routes exactly the most
	// expensive requests to the most expensive models. Half-point bumps push
	// borderline long-context tasks into the ambiguous band, where the judge
	// sees the actual instruction and decides.
	if s.taskTokens > 8000 {
		score += 0.5
	}

	if s.taskTokens > 32000 {
		score += 0.5
	}

	switch {
	case s.recentFences > 0:
		score += 1
	case s.historyFences > 0:
		score += historyWeight
	}

	hard := float64(s.recentHard) + historyWeight*float64(s.historyHard)

	switch {
	case hard >= 2:
		score += 1
	case hard >= 1:
		score += 0.5
	}

	if s.toolCount > 0 {
		score += 1
	}

	if s.hasNonImageFile {
		score += 0.5
	}

	if s.escalate {
		score += triggerBoost
	}

	if s.deescalate {
		score -= triggerBoost
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
