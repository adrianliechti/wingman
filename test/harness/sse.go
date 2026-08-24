package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ParseSSE reads an SSE stream and returns all events.
func ParseSSE(r io.Reader) ([]*SSEEvent, error) {
	var events []*SSEEvent

	scanner := bufio.NewScanner(r)

	var currentEvent string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = end of event
			if len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")

				event := &SSEEvent{
					Event: currentEvent,
					Raw:   data,
				}

				if data != "[DONE]" {
					var parsed map[string]any
					if err := json.Unmarshal([]byte(data), &parsed); err == nil {
						event.Data = parsed
					}
				}

				events = append(events, event)
			}

			currentEvent = ""
			dataLines = nil
			continue
		}

		if after, ok := strings.CutPrefix(line, "event: "); ok {
			currentEvent = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			dataLines = append(dataLines, after)
		}
	}

	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("scan SSE stream: %w", err)
	}

	return events, nil
}
