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

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}

	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("scan SSE stream: %w", err)
	}

	return events, nil
}
