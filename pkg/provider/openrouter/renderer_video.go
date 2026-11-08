package openrouter

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type videoJob struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

func (r *Renderer) renderVideo(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	if options.Format != "" && options.Format != provider.RenderFormatMP4 {
		return nil, errors.New("video output requires MP4 format")
	}
	if options.Mask != nil || options.Markup != nil || options.FrameCount != nil || options.FrameDuration != nil {
		return nil, errors.New("image edit guides and GIF frame controls are not supported for video output")
	}
	body := map[string]any{"model": r.model, "prompt": input}
	if options.Duration != nil {
		d := *options.Duration
		if d <= 0 || d%time.Second != 0 {
			return nil, errors.New("video duration must be a positive whole number of seconds")
		}
		body["duration"] = int64(d / time.Second)
	}
	if options.Aspect != "" {
		body["aspect_ratio"] = string(options.Aspect)
	}
	if options.Resolution != "" {
		body["resolution"] = string(options.Resolution)
	}
	if len(options.Images) > 0 {
		refs := make([]map[string]any, 0, len(options.Images))
		for _, image := range options.Images {
			if !strings.HasPrefix(image.ContentType, "image/") {
				return nil, errors.New("video generation references must be images")
			}
			refs = append(refs, map[string]any{"type": "image_url", "image_url": map[string]string{
				"url": "data:" + image.ContentType + ";base64," + base64.StdEncoding.EncodeToString(image.Content),
			}})
		}
		body["input_references"] = refs
	}
	var job videoJob
	// Video submission returns 202 Accepted; image requests continue to require 200.
	if err := doRequest(ctx, r.client, r.url+"/videos", r.token, body, &job); err != nil {
		return nil, err
	}
	if job.ID == "" {
		return nil, errors.New("openrouter: video response has no job ID")
	}
	jobURL := r.url + "/videos/" + url.PathEscape(job.ID)
	for {
		switch job.Status {
		case "completed":
			// Construct the authenticated content URL locally instead of forwarding
			// credentials to URLs supplied by a provider response.
			response, err := r.videoGet(ctx, jobURL+"/content?index=0")
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			if err != nil {
				return nil, err
			}
			if len(data) == 0 {
				return nil, errors.New("openrouter: empty video content")
			}
			return &provider.Rendering{ID: job.ID, Model: r.model, Content: data, ContentType: "video/mp4"}, nil
		case "failed", "cancelled", "expired":
			return nil, fmt.Errorf("openrouter: video generation %s: %s", job.Status, job.Error)
		case "pending", "in_progress":
		default:
			return nil, fmt.Errorf("openrouter: unexpected video status %q", job.Status)
		}
		timer := time.NewTimer(r.videoPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		response, err := r.videoGet(ctx, jobURL)
		if err != nil {
			return nil, err
		}
		var next videoJob
		err = json.UnmarshalRead(response.Body, &next)
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		if next.ID != "" && next.ID != job.ID {
			return nil, errors.New("openrouter: video job ID changed while polling")
		}
		next.ID = job.ID
		job = next
	}
}

func (r *Renderer) videoGet(ctx context.Context, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	response, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, &provider.ProviderError{Code: response.StatusCode, Message: string(message)}
	}
	return response, nil
}
