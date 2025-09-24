package responses

import (
	"encoding/json"
	"testing"
)

func TestResponsesRequest_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ResponsesRequest
	}{
		{
			name:  "string input",
			input: `{"model": "gpt-5", "input": "Are semicolons optional in JavaScript?"}`,
			expected: ResponsesRequest{
				Model: "gpt-5",
				Input: ResponsesInput{
					Messages: []InputMessage{
						{
							Role: MessageRoleUser,
							Content: []InputContent{
								{
									Type: InputContentText,
									Text: "Are semicolons optional in JavaScript?",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "array of messages with string content",
			input: `{
				"model": "gpt-5",
				"input": [
					{
						"role": "developer",
						"content": "Talk like a pirate."
					},
					{
						"role": "user",
						"content": "Are semicolons optional in JavaScript?"
					}
				]
			}`,
			expected: ResponsesRequest{
				Model: "gpt-5",
				Input: ResponsesInput{
					Messages: []InputMessage{
						{
							Role: MessageRoleDeveloper,
							Content: []InputContent{
								{
									Type: InputContentText,
									Text: "Talk like a pirate.",
								},
							},
						},
						{
							Role: MessageRoleUser,
							Content: []InputContent{
								{
									Type: InputContentText,
									Text: "Are semicolons optional in JavaScript?",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "message with mixed content types",
			input: `{
				"model": "gpt-4.1-mini",
				"input": [
					{
						"role": "user",
						"content": [
							{
								"type": "input_text",
								"text": "what is in this image?"
							},
							{
								"type": "input_image",
								"image_url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg"
							}
						]
					}
				]
			}`,
			expected: ResponsesRequest{
				Model: "gpt-4.1-mini",
				Input: ResponsesInput{
					Messages: []InputMessage{
						{
							Role: MessageRoleUser,
							Content: []InputContent{
								{
									Type: InputContentText,
									Text: "what is in this image?",
								},
								{
									Type:     InputContentImage,
									ImageURL: "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "conversation with mixed content",
			input: `{
				"model": "gpt-4.1-mini",
				"input": [
					{
						"role": "user",
						"content": "knock knock."
					},
					{
						"role": "assistant",
						"content": "Who's there?"
					},
					{
						"role": "user",
						"content": [
							{
								"type": "input_text",
								"text": "what is in this image?"
							},
							{
								"type": "input_image",
								"image_url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg"
							}
						]
					}
				]
			}`,
			expected: ResponsesRequest{
				Model: "gpt-4.1-mini",
				Input: ResponsesInput{
					Messages: []InputMessage{
						{
							Role: MessageRoleUser,
							Content: []InputContent{
								{
									Type: InputContentText,
									Text: "knock knock.",
								},
							},
						},
						{
							Role: MessageRoleAssistant,
							Content: []InputContent{
								{
									Type: InputContentText,
									Text: "Who's there?",
								},
							},
						},
						{
							Role: MessageRoleUser,
							Content: []InputContent{
								{
									Type: InputContentText,
									Text: "what is in this image?",
								},
								{
									Type:     InputContentImage,
									ImageURL: "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg",
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result ResponsesRequest
			err := json.Unmarshal([]byte(tt.input), &result)
			if err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			if result.Model != tt.expected.Model {
				t.Errorf("Expected model %q, got %q", tt.expected.Model, result.Model)
			}

			if len(result.Input.Messages) != len(tt.expected.Input.Messages) {
				t.Errorf("Expected %d messages, got %d", len(tt.expected.Input.Messages), len(result.Input.Messages))
				return
			}

			for i, msg := range result.Input.Messages {
				expected := tt.expected.Input.Messages[i]
				if msg.Role != expected.Role {
					t.Errorf("Message[%d]: Expected role %q, got %q", i, expected.Role, msg.Role)
				}

				if len(msg.Content) != len(expected.Content) {
					t.Errorf("Message[%d]: Expected %d content items, got %d", i, len(expected.Content), len(msg.Content))
					continue
				}

				for j, content := range msg.Content {
					expectedContent := expected.Content[j]
					if content.Type != expectedContent.Type {
						t.Errorf("Message[%d].Content[%d]: Expected type %q, got %q", i, j, expectedContent.Type, content.Type)
					}
					if content.Text != expectedContent.Text {
						t.Errorf("Message[%d].Content[%d]: Expected text %q, got %q", i, j, expectedContent.Text, content.Text)
					}
					if content.ImageURL != expectedContent.ImageURL {
						t.Errorf("Message[%d].Content[%d]: Expected image_url %q, got %q", i, j, expectedContent.ImageURL, content.ImageURL)
					}
				}
			}
		})
	}
}
