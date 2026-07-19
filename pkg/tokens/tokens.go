// Package tokens estimates LLM token counts without tokenizer data files.
//
// Text is reduced to character-class features (letter runs, digits,
// punctuation, whitespace runs, CJK chars, camelCase / letter-digit
// transitions, non-ASCII letters, long-run extra chars) and mapped to a token
// count with per-tokenizer-family coefficients fitted against ground truth:
// the Anthropic count_tokens endpoint for Claude families and exact tiktoken
// encoders for GPT families. Message framing and media formulas were measured
// against the live Anthropic count_tokens and OpenAI /responses/input_tokens
// endpoints (2026-07-19).
//
// Accuracy on the calibration corpus (see testdata/calibration.json): median
// error ≤ ~10%, worst case ~±30% (German-style compound-heavy prose and
// unusual byte content). Estimates — not billing-accurate counts.
package tokens

import "unicode"

// Family identifies a tokenizer family. Models in a family segment text
// (approximately) identically.
type Family string

const (
	// Claude2026 is the tokenizer of Claude Sonnet 5, Opus 4.7/4.8, and
	// Fable/Mythos 5.
	Claude2026 Family = "claude-2026"
	// ClaudeLegacy is the tokenizer of Claude Opus ≤ 4.6, Sonnet ≤ 4.6, and
	// all Haiku models.
	ClaudeLegacy Family = "claude-legacy"
	// GPTO200k is OpenAI's o200k_base: GPT-5.x, GPT-4o, GPT-4.1, o-series.
	GPTO200k Family = "gpt-o200k"
	// GPTCl100k is OpenAI's cl100k_base: GPT-4, GPT-3.5, embeddings.
	GPTCl100k Family = "gpt-cl100k"
)

// familyPrefixes maps model-ID prefixes to families; longest prefix wins.
// Dated snapshots (claude-haiku-4-5-20251001, gpt-4o-2024-08-06) match via
// their base prefix.
var familyPrefixes = map[string]Family{
	"claude-fable-5":  Claude2026,
	"claude-mythos":   Claude2026,
	"claude-opus-4-8": Claude2026,
	"claude-opus-4-7": Claude2026,
	"claude-sonnet-5": Claude2026,
	"claude":          ClaudeLegacy,

	"gpt-5":   GPTO200k,
	"gpt-4o":  GPTO200k,
	"gpt-4.1": GPTO200k,
	"chatgpt": GPTO200k,
	"o1":      GPTO200k,
	"o3":      GPTO200k,
	"o4":      GPTO200k,

	"gpt-4":          GPTCl100k,
	"gpt-3.5":        GPTCl100k,
	"text-embedding": GPTCl100k,
}

// FamilyFor resolves a model ID to its tokenizer family. Unknown models
// (other providers proxied through wingman) fall back to GPTO200k, a
// reasonable stand-in for any modern BPE tokenizer.
func FamilyFor(model string) Family {
	best, bestLen := GPTO200k, 0
	for prefix, fam := range familyPrefixes {
		if len(prefix) > bestLen && len(model) >= len(prefix) && model[:len(prefix)] == prefix {
			best, bestLen = fam, len(prefix)
		}
	}
	return best
}

// weights are the fitted per-family coefficients (non-negative least squares,
// ridge-stabilized; refit with the tokencounter calibration harness).
type weights struct {
	LetterRun, Letter, Digit, Punct, WordSpace, SpaceRun, Wide, CaseFlip, AlnumFlip, NonAscii, RunExtra float64
}

var familyWeights = map[Family]weights{
	Claude2026:   {LetterRun: 1.8811, Digit: 0.4447, Punct: 0.6575, SpaceRun: 1.1044, Wide: 1.0868, AlnumFlip: 1.2664, NonAscii: 0.0288, RunExtra: 0.6539},
	ClaudeLegacy: {LetterRun: 1.2693, Digit: 0.4543, Punct: 0.5055, SpaceRun: 1.1589, Wide: 1.1134, CaseFlip: 0.1662, AlnumFlip: 1.1779, NonAscii: 0.1693, RunExtra: 0.3520},
	GPTO200k:     {LetterRun: 0.7291, Letter: 0.0734, Digit: 0.5323, Punct: 0.3880, SpaceRun: 1.0345, Wide: 0.8328, AlnumFlip: 0.8507, NonAscii: 0.1146, RunExtra: 0.0777},
	GPTCl100k:    {LetterRun: 0.8286, Letter: 0.0543, Digit: 0.5316, Punct: 0.3325, WordSpace: 0.0994, SpaceRun: 1.0163, Wide: 1.3143, AlnumFlip: 0.8990, NonAscii: 0.3307, RunExtra: 0.1026},
}

// Text estimates the token count of a piece of text for the given model.
func Text(model, text string) int {
	return textForFamily(FamilyFor(model), text)
}

func textForFamily(family Family, text string) int {
	if text == "" {
		return 0
	}
	w, ok := familyWeights[family]
	if !ok {
		w = familyWeights[GPTO200k]
	}

	var letterRuns, letters, digits, punct, wordSpaces, spaceRuns, wide, caseFlips, alnumFlips, nonAscii, runExtra float64

	runes := []rune(text)
	inLetters, inSpace := false, false
	runLen := 0
	var prev rune
	for idx, r := range runes {
		isSpace := unicode.IsSpace(r)
		isDigit := unicode.IsDigit(r)
		isLetter := unicode.IsLetter(r) && !isDigit
		isWide := isLetter && r >= 0x2E80 // CJK, kana, hangul

		switch {
		case isSpace:
			if !inSpace {
				nextIsLetter := idx+1 < len(runes) && unicode.IsLetter(runes[idx+1]) && runes[idx+1] < 0x2E80
				endsRun := idx+1 >= len(runes) || !unicode.IsSpace(runes[idx+1])
				if r == ' ' && endsRun && nextIsLetter {
					wordSpaces++
				} else {
					spaceRuns++
				}
			}
		case isWide:
			wide++
		case isLetter:
			if !inLetters {
				letterRuns++
				runLen = 0
			} else if unicode.IsUpper(r) && unicode.IsLower(prev) {
				caseFlips++
			}
			if unicode.IsDigit(prev) {
				alnumFlips++
			}
			letters++
			if r >= 0x80 {
				nonAscii++
			}
			runLen++
			if runLen > 8 {
				runExtra++
			}
		case isDigit:
			if unicode.IsLetter(prev) && prev < 0x2E80 {
				alnumFlips++
			}
			digits++
		default:
			punct++
		}
		inSpace = isSpace
		inLetters = isLetter && !isWide
		prev = r
	}

	n := w.LetterRun*letterRuns + w.Letter*letters + w.Digit*digits + w.Punct*punct +
		w.WordSpace*wordSpaces + w.SpaceRun*spaceRuns + w.Wide*wide +
		w.CaseFlip*caseFlips + w.AlnumFlip*alnumFlips + w.NonAscii*nonAscii + w.RunExtra*runExtra

	if n < 1 {
		return 1
	}
	return int(n + 0.5)
}

// Message framing overheads, measured against the live count endpoints
// (2026-07-19). Values are tokens added on top of the content estimate.
const (
	// Anthropic count_tokens: fixed per-request base plus per-user-message
	// wrapping; assistant messages add no measurable framing.
	AnthropicRequestOverhead     = 2
	AnthropicUserMessageOverhead = 5 // measured: 4 (2026 family) / 6 (legacy)
	AnthropicAssistantOverhead   = 0
	// OpenAI /responses/input_tokens: fixed base plus per-message wrapping
	// (measured totals: 7-8 for one message, +5..7 per additional message).
	OpenAIRequestOverhead      = 2
	OpenAIMessageOverhead      = 5
	OpenAIInstructionsOverhead = 4
)
