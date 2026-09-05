// Package hardtest holds prompts, fixtures and assertions shared by the
// protocol-specific hard suites so every surface exercises the same
// scenarios.
package hardtest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

const Concurrency = 4

const ParallelPrompt = "Get the current weather for London, Paris and Tokyo. Call the weather tool once per city. Then summarize all three in one sentence each."

const ChainedPrompt = "First find out the current time with the time tool, then get the weather in Bern with the weather tool, then answer with both in one sentence."

const TextThenToolPrompt = "First write one short sentence telling me what you are about to do, then call get_weather for London."

const UnicodePrompt = "Repeat exactly, with no other text: Grüße aus Zürich 🇨🇭 — ñandú, 日本語, Ελληνικά, ✓ done"

const EssayPrompt = "Write a detailed 800 word essay about the history of the ocean tides. Do not stop early."

const ColorPrompt = "What is the dominant color of this image? Reply with a single word."

const BookPrompt = "Recommend a classic science fiction book."

const ToolError = "Error: the weather service is unavailable (HTTP 503). Please tell the user."

var BookSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title":  map[string]any{"type": "string"},
		"author": map[string]any{"type": "string"},
		"year":   map[string]any{"type": "integer"},
	},
	"required":             []string{"title", "author", "year"},
	"additionalProperties": false,
}

// ToolResult answers a tool call: the weather for the requested location, or
// a fixed time for the clock tool.
func ToolResult(name, arguments string) string {
	if name == "get_time" {
		return "2026-09-05T14:30:00Z"
	}

	var args struct {
		Location string `json:"location"`
	}
	json.Unmarshal([]byte(arguments), &args)

	if args.Location == "" {
		args.Location = "the requested location"
	}

	return fmt.Sprintf("Sunny, 22°C in %s with light winds", args.Location)
}

func RequireMentionsCities(t *testing.T, label, answer string) {
	t.Helper()

	lower := strings.ToLower(answer)
	for _, city := range []string{"london", "paris", "tokyo"} {
		if !strings.Contains(lower, city) {
			t.Errorf("[%s] answer does not mention %s: %q", label, city, answer)
		}
	}
}

// RequireUnicode checks that the multi-byte and emoji fragments survived
// streaming intact.
func RequireUnicode(t *testing.T, label, text string) {
	t.Helper()

	for _, want := range []string{"Grüße", "Zürich", "🇨🇭", "ñandú", "日本語", "Ελληνικά", "✓"} {
		if !strings.Contains(text, want) {
			t.Errorf("[%s] streamed text lost %q: %q", label, want, text)
		}
	}
}

func RequireRed(t *testing.T, label, answer string) {
	t.Helper()

	if !strings.Contains(strings.ToLower(answer), "red") {
		t.Errorf("[%s] expected the image to be described as red, got %q", label, answer)
	}
}

func RequireBookJSON(t *testing.T, label, text string) {
	t.Helper()

	var book struct {
		Title  string `json:"title"`
		Author string `json:"author"`
		Year   int    `json:"year"`
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &book); err != nil {
		t.Fatalf("[%s] not valid JSON: %v\n%s", label, err, text)
	}
	if book.Title == "" || book.Author == "" || book.Year == 0 {
		t.Errorf("[%s] incomplete book: %+v", label, book)
	}
}

// RedSquareDataURL returns a 64x64 solid red PNG as a data URL.
func RedSquareDataURL() string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(RedSquarePNG())
}

func RedSquarePNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}

	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

const needle = "PELICAN-7"

// NeedlePrompt buries a code word in roughly 30 KB of filler and asks for it.
func NeedlePrompt() string {
	var b strings.Builder

	b.WriteString("Read the following notes carefully and then answer the question at the end.\n\n")

	for i := 0; i < 240; i++ {
		fmt.Fprintf(&b, "Note %d: The quarterly review covered logistics, staffing and the maintenance schedule for the northern depots; no decisions were taken and the item was carried over.\n", i)

		if i == 137 {
			fmt.Fprintf(&b, "Note %d-a: The project code name chosen for the new depot scheduling tool is %s.\n", i, needle)
		}
	}

	b.WriteString("\nQuestion: What is the project code name of the new depot scheduling tool? Reply with the code name only.")

	return b.String()
}

func RequireNeedle(t *testing.T, label, answer string) {
	t.Helper()

	if !strings.Contains(strings.ToUpper(answer), needle) {
		t.Errorf("[%s] answer does not contain the code name %s: %q", label, needle, answer)
	}
}

// CountingPrompt with a stop sequence at "6" must end before the sixth number.
const CountingPrompt = "Count from 1 to 10 as plain digits separated by commas and spaces, nothing else."

func RequireStoppedBeforeSix(t *testing.T, label, text string) {
	t.Helper()

	if !strings.Contains(text, "5") {
		t.Errorf("[%s] expected counting to reach 5: %q", label, text)
	}
	if strings.Contains(text, "6") {
		t.Errorf("[%s] stop sequence not applied: %q", label, text)
	}
}
