package custom

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type recordingClient struct {
	calls, capabilityCalls int
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

func (c *recordingClient) Extract(context.Context, *ExtractRequest, ...grpc.CallOption) (*Document, error) {
	c.calls++
	return &Document{Text: "extracted"}, nil
}

func TestDiscoveredFormatsSkipUnsupportedRequests(t *testing.T) {
	rpc := &recordingClient{capabilities: &CapabilitiesResponse{MediaTypes: []string{"application/pdf"}, Extensions: []string{".pdf"}}}
	c := &Client{client: rpc}
	if err := c.loadCapabilities(); err != nil {
		t.Fatal(err)
	}
	rpc.capabilities.Extensions[0] = ".csv"
	if _, err := c.Extract(t.Context(), extractor.File{Name: "data.csv"}, nil); !errors.Is(err, extractor.ErrUnsupported) || rpc.calls != 0 {
		t.Fatalf("unsupported file: error %v, calls %d", err, rpc.calls)
	}
	got := c.Capabilities()
	got.Extensions[0] = ".csv"
	result, err := c.Extract(t.Context(), extractor.File{Name: "report.pdf"}, nil)
	if err != nil || result == nil || result.Text != "extracted" || rpc.calls != 1 {
		t.Fatalf("PDF result %+v, error %v, calls %d", result, err, rpc.calls)
	}
	if rpc.capabilityCalls != 1 {
		t.Fatalf("capabilities were fetched %d times, want once", rpc.capabilityCalls)
	}
}

func TestUnknownFormatsRemainCandidates(t *testing.T) {
	rpc := &recordingClient{capabilityError: status.Error(codes.Unimplemented, "legacy service")}
	c := &Client{client: rpc}
	if err := c.loadCapabilities(); err != nil {
		t.Fatal(err)
	}
	if caps := c.Capabilities(); !caps.UnknownFormats {
		t.Fatalf("unconfigured capabilities = %+v", caps)
	}
	if _, err := c.Extract(t.Context(), extractor.File{Name: "unknown.bin"}, nil); err != nil || rpc.calls != 1 {
		t.Fatalf("unknown format: error %v, calls %d", err, rpc.calls)
	}
}

func TestDiscoveredEmptyFormatsAreUnsupported(t *testing.T) {
	rpc := &recordingClient{capabilities: &CapabilitiesResponse{}}
	c := &Client{client: rpc}
	if err := c.loadCapabilities(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Extract(t.Context(), extractor.File{Name: "report.pdf"}, nil); !errors.Is(err, extractor.ErrUnsupported) || rpc.calls != 0 {
		t.Fatalf("empty formats: error %v, calls %d", err, rpc.calls)
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
