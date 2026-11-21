package google

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
	"google.golang.org/genai/interactions/retry"
)

func (s *Synthesizer) synthesizeMusic(ctx context.Context, prompt, format string) (*provider.Synthesis, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, provider.InvalidRequest(errors.New("lyria: input must contain a music prompt"))
	}

	request := interactions.CreateModelInteraction{
		Model: interactions.Model(strings.TrimPrefix(s.model, "models/")),
		Input: new(interactions.NewInteractionsInput(prompt)),
		Store: new(false),
	}
	contentType := "audio/mpeg"
	switch strings.ToLower(format) {
	case "", "mp3":
		// Lyria defaults to MP3.
	case "wav":
		contentType = "audio/wav"
		audio := interactions.NewResponseFormat(interactions.AudioResponseFormat{
			MimeType: new(interactions.AudioResponseFormatMimeTypeAudioWav),
		})
		request.ResponseFormat = new(interactions.NewCreateModelInteractionResponseFormat(audio))
	default:
		return nil, provider.InvalidRequest(fmt.Errorf("lyria: unsupported response_format %q, use mp3 or wav", format))
	}

	client, err := s.newClient(ctx)
	if err != nil {
		return nil, err
	}
	response, err := client.Interactions.Create(ctx, operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(request),
	}, operations.WithRetries(retry.Config{Strategy: "none"}))
	if err != nil {
		return nil, convertError(err)
	}
	result := response.GetInteraction()
	if result == nil {
		return nil, errors.New("lyria: empty interaction response")
	}
	if result.Status != "" && result.Status != interactions.InteractionStatusCompleted {
		for _, fault := range result.Errors {
			if message := value(fault.Message); message != "" {
				return nil, fmt.Errorf("lyria: music generation %s: %s", result.Status, message)
			}
		}
		return nil, fmt.Errorf("lyria: music generation %s", result.Status)
	}

	audio := result.OutputAudio
	if audio == nil || value(audio.Data) == "" {
		return nil, errors.New("lyria: no audio data in response")
	}
	switch mimeType := string(value(audio.MimeType)); mimeType {
	case "":
	case "audio/mp3":
		contentType = "audio/mpeg"
	default:
		contentType = mimeType
	}
	file, err := interactionFile(audio.Data, nil, contentType)
	if err != nil {
		return nil, err
	}
	if len(file.Content) == 0 {
		return nil, errors.New("lyria: no audio data in response")
	}

	id := value(result.ID)
	if id == "" {
		id = uuid.NewString()
	}
	return &provider.Synthesis{
		ID:    id,
		Model: s.model,

		Content:     file.Content,
		ContentType: file.ContentType,
	}, nil
}
