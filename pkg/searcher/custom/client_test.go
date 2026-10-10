package custom

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
	searchtool "github.com/adrianliechti/wingman/pkg/tool/search"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type searchClientFunc func(context.Context, *SearchRequest) (*SearchResponse, error)

func (f searchClientFunc) Capabilities(context.Context, *CapabilitiesRequest, ...grpc.CallOption) (*CapabilitiesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "legacy service")
}

func (f searchClientFunc) Search(ctx context.Context, req *SearchRequest, _ ...grpc.CallOption) (*SearchResponse, error) {
	return f(ctx, req)
}

func TestSearchProtocolFieldsAndCallerOptions(t *testing.T) {
	limit := 3
	for _, defaults := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "defaults"}[defaults], func(t *testing.T) {
			options := &searcher.SearchOptions{Limit: &limit, Include: []string{"example.com"}, Exclude: []string{"spam.example"}}
			if !defaults {
				options.Category, options.Location = "news", "CH"
			}
			want := *options
			var called bool
			c := &Client{category: "news", location: "CH", client: searchClientFunc(func(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
				called = true
				category, location, count := "news", "CH", int32(3)
				expected := &SearchRequest{Query: "release", Limit: &count, Category: &category, Location: &location, Include: []string{"example.com"}, Exclude: []string{"spam.example"}}
				if !proto.Equal(req, expected) {
					t.Fatalf("request = %v, want %v", req, expected)
				}
				return &SearchResponse{Results: []*Result{{Source: "https://example.com/a", Title: "Release", Content: "Evidence", Metadata: map[string]string{"published": "2026-10-10T12:00:00Z", "author": "Example"}}, {Source: "https://example.com/b"}}}, nil
			})}
			results, err := c.Search(context.Background(), " release ", options)
			if err != nil {
				t.Fatal(err)
			}
			if !called || !reflect.DeepEqual(*options, want) {
				t.Fatal("missing RPC or mutated caller options")
			}
			if len(results) != 2 || results[0].Source != "https://example.com/a" || results[0].Content != "Evidence" || results[0].Timestamp == nil || results[0].Metadata["author"] != "Example" || results[1].Timestamp != nil {
				t.Fatalf("wrong results: %#v", results)
			}
		})
	}
}

func TestSearchRejectsUnsupportedAndInvalidOptions(t *testing.T) {
	c := &Client{client: searchClientFunc(func(context.Context, *SearchRequest) (*SearchResponse, error) {
		t.Fatal("invalid options must not make an RPC")
		return nil, nil
	})}
	if _, err := c.Search(context.Background(), " ", nil); err == nil {
		t.Fatal("accepted empty query")
	}
	date := time.Now()
	zero, negative, excessive := 0, -1, int(math.MaxInt32)+1
	for _, options := range []searcher.SearchOptions{{Limit: &zero}, {Limit: &negative}, {Limit: &excessive}, {Since: &date}, {Until: &date}} {
		if _, err := c.Search(context.Background(), "query", &options); err == nil {
			t.Errorf("accepted %+v", options)
		}
	}
	for _, endpoint := range []string{"", "https://example.com", "grpc://", "grpc:// "} {
		if _, err := New(endpoint); err == nil {
			t.Errorf("accepted URL %q", endpoint)
		}
	}
}

func TestSearchOptionalFieldsAndRPCError(t *testing.T) {
	sentinel := errors.New("RPC unavailable")
	c := &Client{client: searchClientFunc(func(_ context.Context, req *SearchRequest) (*SearchResponse, error) {
		if req.Limit != nil || req.Category != nil || req.Location != nil || len(req.Include) != 0 || len(req.Exclude) != 0 {
			t.Fatalf("sent optional defaults: %v", req)
		}
		return nil, sentinel
	})}
	if _, err := c.Search(context.Background(), "query", nil); !errors.Is(err, sentinel) {
		t.Fatalf("got %v", err)
	}
	c.client = searchClientFunc(func(context.Context, *SearchRequest) (*SearchResponse, error) { return nil, nil })
	if _, err := c.Search(context.Background(), "query", nil); err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("got %v", err)
	}
}

type reportingClient struct {
	searchClientFunc
	response *CapabilitiesResponse
	err      error
	calls    int
}

func (c *reportingClient) Capabilities(ctx context.Context, _ *CapabilitiesRequest, _ ...grpc.CallOption) (*CapabilitiesResponse, error) {
	c.calls++
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
		return nil, errors.New("discovery has no bounded deadline")
	}
	return c.response, c.err
}

func TestDiscoveredCategoriesAndDateFilters(t *testing.T) {
	since := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.FixedZone("offset", 2*60*60))
	until := since.AddDate(0, 0, 1)
	options := &searcher.SearchOptions{Category: "publication", Since: &since, Until: &until}
	want := *options
	rpc := &reportingClient{
		response: &CapabilitiesResponse{DateFilters: true, Categories: []*Category{{Name: "publication", Description: "Research publications."}}},
		searchClientFunc: func(_ context.Context, req *SearchRequest) (*SearchResponse, error) {
			category := "publication"
			expected := &SearchRequest{Query: "research", Category: &category, Since: timestamppb.New(since), Until: timestamppb.New(until)}
			if !proto.Equal(req, expected) {
				t.Fatalf("request = %v, want %v", req, expected)
			}
			return &SearchResponse{}, nil
		},
	}
	c := &Client{client: rpc}
	if err := c.loadCapabilities(); err != nil {
		t.Fatal(err)
	}
	rpc.response.Categories[0].Name = "mutated"
	caps := c.Capabilities()
	if !caps.DateFilters || !reflect.DeepEqual(caps.Categories, []searcher.Category{{Name: "publication", Description: "Research publications."}}) {
		t.Fatalf("reported capabilities = %+v", caps)
	}
	caps.Categories[0].Name = "mutated again"
	if c.Capabilities().Categories[0].Name != "publication" {
		t.Fatal("caller mutated cached categories")
	}
	tool, err := searchtool.New(c)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := tool.Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	props := tools[0].Parameters["properties"].(map[string]any)
	if !reflect.DeepEqual(props["category"].(map[string]any)["enum"], []string{"publication"}) || props["since"] == nil || props["until"] == nil {
		t.Fatalf("tool ignored discovered filters: %+v", props)
	}
	if _, err := c.Search(t.Context(), "research", options); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*options, want) || rpc.calls != 1 {
		t.Fatalf("options mutated or capabilities fetched again: %+v, calls %d", options, rpc.calls)
	}
	invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, options := range []*searcher.SearchOptions{{Since: &until, Until: &since}, {Since: &invalid}, {Until: &invalid}} {
		if _, err := c.Search(t.Context(), "research", options); err == nil {
			t.Fatalf("accepted invalid bounds: %+v", options)
		}
	}
}

func TestLegacyDiscoveryDoesNotInventFilters(t *testing.T) {
	c := &Client{client: searchClientFunc(nil)}
	if err := c.loadCapabilities(); err != nil {
		t.Fatal(err)
	}
	if got := c.Capabilities(); got.DateFilters || len(got.Categories) != 0 {
		t.Fatalf("legacy capabilities = %+v", got)
	}
}

func TestDiscoveryRejectsMalformedCategoriesAndFailures(t *testing.T) {
	for _, response := range []*CapabilitiesResponse{
		{Categories: []*Category{nil}},
		{Categories: []*Category{{Name: " "}}},
		{Categories: []*Category{{Name: "news"}, {Name: "news"}}},
		nil,
	} {
		c := &Client{client: &reportingClient{response: response}}
		if err := c.loadCapabilities(); err == nil {
			t.Fatalf("accepted malformed capabilities: %v", response)
		}
	}
	for _, code := range []codes.Code{codes.Unavailable, codes.PermissionDenied, codes.DeadlineExceeded} {
		c := &Client{client: &reportingClient{err: status.Error(code, "discovery failed")}}
		if err := c.loadCapabilities(); status.Code(err) != code {
			t.Fatalf("discovery error %v, want %v", err, code)
		}
	}
}
