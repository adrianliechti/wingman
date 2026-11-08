package google

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
)

func (r *Renderer) renderVideo(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	if options.Format != "" && options.Format != provider.RenderFormatMP4 {
		return nil, errors.New("gemini omni requires MP4 output")
	}
	if options.Mask != nil || options.Markup != nil || options.FrameCount != nil || options.FrameDuration != nil {
		return nil, errors.New("image edit guides and GIF frame controls are not supported for video output")
	}
	format := interactions.VideoResponseFormat{Delivery: new(interactions.VideoResponseFormatDeliveryURI)}
	if options.Duration != nil {
		d := *options.Duration
		if d < 3*time.Second || d > 15*time.Second || d%time.Second != 0 {
			return nil, errors.New("gemini omni duration must be whole seconds from 3 to 15")
		}
		format.Duration = new(fmt.Sprintf("%ds", d/time.Second))
	}
	if options.Aspect != "" {
		format.AspectRatio = new(interactions.VideoResponseFormatAspectRatio(options.Aspect.Nearest([]provider.AspectRatio{provider.AspectRatio16x9, provider.AspectRatio9x16})))
	}
	if options.Resolution != "" {
		switch options.Resolution {
		case provider.Resolution360p, provider.Resolution720p, provider.Resolution1080p, provider.Resolution4K:
			format.Resolution = new(interactions.Resolution(strings.ToLower(string(options.Resolution))))
		default:
			return nil, errors.New("gemini omni resolution must be 360p, 720p, 1080p or 4K")
		}
	}
	if len(options.Images) > 10 {
		return nil, errors.New("gemini omni supports at most 10 reference images")
	}
	parts := []interactions.Content{}
	for _, image := range options.Images {
		if !strings.HasPrefix(image.ContentType, "image/") {
			return nil, errors.New("video generation references must be images")
		}
		part, err := fileContent(&image)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	if len(parts) > 0 {
		input += "\nUse the given images as references for video generation, not literal initial frames."
	}
	parts = append(parts, interactions.NewContent(interactions.TextContent{Text: input}))
	contents := interactions.NewInteractionsInput(parts)
	responseFormat := interactions.NewCreateModelInteractionResponseFormat(interactions.NewResponseFormat(format))
	client, err := r.newClient(ctx)
	if err != nil {
		return nil, err
	}
	response, err := client.Interactions.Create(ctx, operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(interactions.CreateModelInteraction{
			Model: interactions.Model(r.model), Input: &contents, ResponseFormat: &responseFormat,
			// Google requires storage for URI delivery, including synchronous requests.
			Background: new(false), Store: new(true), Stream: new(false),
		}),
	})
	if err != nil {
		return nil, convertError(err)
	}
	result := response.Interaction
	if result == nil {
		return nil, errors.New("gemini omni: empty interaction response")
	}
	// The public renderer returns bytes rather than a resumable interaction.
	// Remove only the resources created by this generation, including on failure
	// or client cancellation. Cleanup errors must not discard a downloaded video.
	var fileName string
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if fileName != "" {
			if _, err := client.Files.Delete(cleanupCtx, fileName, nil); err != nil {
				slog.WarnContext(cleanupCtx, "gemini omni: generated file cleanup failed", "error", convertError(err))
			}
		}
		if id := value(result.ID); id != "" {
			if _, err := client.Interactions.Delete(cleanupCtx, operations.DeleteInteractionRequest{ID: id}); err != nil {
				slog.WarnContext(cleanupCtx, "gemini omni: interaction cleanup failed", "error", convertError(err))
			}
		}
	}()
	if result.Status != interactions.InteractionStatusCompleted {
		reason := string(result.Status)
		for _, fault := range result.Errors {
			if fault.Message != nil {
				reason += ": " + *fault.Message
			}
		}
		return nil, fmt.Errorf("gemini omni: video generation %s", reason)
	}
	video := result.OutputVideo
	if video == nil {
		return nil, errors.New("gemini omni: no video in response")
	}
	var data []byte
	switch {
	case video.Data != nil && *video.Data != "":
		data, err = base64.StdEncoding.DecodeString(*video.Data)
	case video.URI != nil && *video.URI != "":
		fileName, err = videoFileName(*video.URI)
		if err == nil {
			err = waitVideoFile(ctx, client, fileName, 5*time.Second)
		}
		if err == nil {
			// Passing the canonical files/ name avoids the SDK URL parser truncating
			// valid file IDs that contain a dash.
			data, err = client.Files.Download(ctx, genai.NewDownloadURIFromFile(&genai.File{DownloadURI: fileName}), nil)
		}
	default:
		return nil, errors.New("gemini omni: video response has no data or download URI")
	}
	if err != nil {
		return nil, convertError(err)
	}
	if len(data) == 0 {
		return nil, errors.New("gemini omni: empty video data")
	}
	contentType := string(value(video.MimeType))
	if contentType == "" {
		contentType = "video/mp4"
	}
	return &provider.Rendering{ID: value(result.ID), Model: r.model, Content: data, ContentType: contentType}, nil
}

// videoFileName accepts Files API references and download URLs. Media requests
// still go through the authenticated SDK endpoint, never to an arbitrary host.
func videoFileName(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("gemini omni: invalid video URI: %w", err)
	}
	path := strings.TrimPrefix(parsed.Path, "/")
	if !strings.HasPrefix(path, "files/") {
		_, path, _ = strings.Cut(path, "/files/")
	} else {
		path = strings.TrimPrefix(path, "files/")
	}
	id := strings.TrimSuffix(path, ":download")
	if id == "" {
		return "", errors.New("gemini omni: video URI has no file ID")
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return "", errors.New("gemini omni: video URI has an invalid file ID")
		}
	}
	return "files/" + id, nil
}

func waitVideoFile(ctx context.Context, client *genai.Client, name string, interval time.Duration) error {
	for {
		file, err := client.Files.Get(ctx, name, nil)
		if err != nil {
			return err
		}
		switch file.State {
		case genai.FileStateActive:
			return nil
		case genai.FileStateProcessing:
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		case genai.FileStateFailed:
			reason := "processing failed"
			if file.Error != nil && file.Error.Message != "" {
				reason = file.Error.Message
			}
			return fmt.Errorf("gemini omni: video file %s", reason)
		default:
			return fmt.Errorf("gemini omni: unexpected video file state %q", file.State)
		}
	}
}
