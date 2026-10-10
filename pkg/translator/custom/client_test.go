package custom

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/translator"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type recordingClient struct {
	calls, capabilityCalls int
	request                *TranslateRequest
	capabilities           *CapabilitiesResponse
	capabilityError        error
}

func (c *recordingClient) Capabilities(ctx context.Context, _ *CapabilitiesRequest, _ ...grpc.CallOption) (*CapabilitiesResponse, error) {
	c.capabilityCalls++
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
		return nil, errors.New("discovery has no bounded deadline")
	}
	return c.capabilities, c.capabilityError
}

func (c *recordingClient) Translate(_ context.Context, request *TranslateRequest, _ ...grpc.CallOption) (*File, error) {
	c.calls++
	c.request = request
	return &File{Name: "translated.pdf", Content: []byte("%PDF-output"), ContentType: "application/pdf"}, nil
}

func TestDiscoveredModesAndDocumentMetadata(t *testing.T) {
	rpc := &recordingClient{capabilities: &CapabilitiesResponse{
		TextToText: Support_SUPPORT_UNSUPPORTED, FileToText: Support_SUPPORT_UNSUPPORTED, FileToDocument: Support_SUPPORT_SUPPORTED,
	}}
	c := &Client{client: rpc}
	if err := c.loadCapabilities(); err != nil {
		t.Fatal(err)
	}
	if caps := c.Capabilities(); caps != (translator.Capabilities{FileToDocument: translator.Supported}) {
		t.Fatalf("discovered modes = %+v", caps)
	}
	if _, err := c.Translate(t.Context(), translator.Input{Text: "hello"}, nil); !errors.Is(err, translator.ErrUnsupported) || rpc.calls != 0 {
		t.Fatalf("unsupported input: error %v, calls %d", err, rpc.calls)
	}
	input := &translator.File{Name: "original.pdf", Content: []byte("%PDF-input"), ContentType: "application/pdf"}
	result, err := c.Translate(t.Context(), translator.Input{File: input}, &translator.TranslateOptions{Language: "de"})
	if err != nil || result == nil || result.Name != "translated.pdf" || result.ContentType != "application/pdf" || string(result.Content) != "%PDF-output" {
		t.Fatalf("document response %+v, error %v", result, err)
	}
	if rpc.calls != 1 || rpc.request.File.Name != input.Name || string(rpc.request.File.Content) != string(input.Content) || rpc.request.Language != "de" {
		t.Fatalf("RPC request %+v, calls %d", rpc.request, rpc.calls)
	}
	if rpc.capabilityCalls != 1 {
		t.Fatalf("capabilities were fetched %d times, want once", rpc.capabilityCalls)
	}
}

func TestUnknownModesRemainCandidates(t *testing.T) {
	rpc := &recordingClient{capabilityError: status.Error(codes.Unimplemented, "legacy service")}
	c := &Client{client: rpc}
	if err := c.loadCapabilities(); err != nil {
		t.Fatal(err)
	}
	caps := c.Capabilities()
	if caps.TextToText != translator.Unknown || caps.FileToText != translator.Unknown || caps.FileToDocument != translator.Unknown {
		t.Fatalf("unconfigured modes = %+v", caps)
	}
	if _, err := c.Translate(t.Context(), translator.Input{Text: "hello"}, nil); err != nil || rpc.calls != 1 {
		t.Fatalf("unknown mode: error %v, calls %d", err, rpc.calls)
	}
}

func TestDiscoveryDefaultsAndInvalidModes(t *testing.T) {
	c := &Client{client: &recordingClient{capabilities: &CapabilitiesResponse{FileToText: Support_SUPPORT_SUPPORTED}}}
	if err := c.loadCapabilities(); err != nil {
		t.Fatal(err)
	}
	if got := c.Capabilities(); got != (translator.Capabilities{TextToText: translator.Unknown, FileToText: translator.Supported, FileToDocument: translator.Unknown}) {
		t.Fatalf("omitted modes = %+v", got)
	}
	for _, response := range []*CapabilitiesResponse{
		{TextToText: Support(-1)}, {FileToText: Support(256)}, {FileToDocument: Support(2147483647)},
	} {
		c := &Client{client: &recordingClient{capabilities: response}}
		if err := c.loadCapabilities(); err == nil || c.capabilities != nil {
			t.Fatalf("invalid response %v: error %v, cached %+v", response, err, c.capabilities)
		}
	}
}

func TestDiscoveryFailuresAreNotCachedAsUnknown(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.PermissionDenied, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			c := &Client{client: &recordingClient{capabilityError: status.Error(code, "discovery failed")}}
			if err := c.loadCapabilities(); status.Code(err) != code || c.capabilities != nil {
				t.Fatalf("discovery error %v, cached %+v", err, c.capabilities)
			}
		})
	}
	c := &Client{client: &recordingClient{}}
	if err := c.loadCapabilities(); err == nil {
		t.Fatal("nil capabilities response was accepted")
	}
}
