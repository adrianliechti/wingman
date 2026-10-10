package custom

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/extractor"
	searchrpc "github.com/adrianliechti/wingman/pkg/searcher/custom"
	translaterpc "github.com/adrianliechti/wingman/pkg/translator/custom"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type protocolExtractor struct {
	UnimplementedExtractorServer
	legacy bool
}

func (s protocolExtractor) Capabilities(ctx context.Context, request *CapabilitiesRequest) (*CapabilitiesResponse, error) {
	if s.legacy {
		return s.UnimplementedExtractorServer.Capabilities(ctx, request)
	}
	return &CapabilitiesResponse{MediaTypes: []string{"application/pdf"}, Extensions: []string{".pdf"}}, nil
}

func (protocolExtractor) Extract(_ context.Context, request *ExtractRequest) (*Document, error) {
	return &Document{Text: string(request.File.Content)}, nil
}

type protocolTranslator struct {
	translaterpc.UnimplementedTranslatorServer
	legacy bool
}

func (s protocolTranslator) Capabilities(ctx context.Context, request *translaterpc.CapabilitiesRequest) (*translaterpc.CapabilitiesResponse, error) {
	if s.legacy {
		return s.UnimplementedTranslatorServer.Capabilities(ctx, request)
	}
	return &translaterpc.CapabilitiesResponse{
		TextToText:     translaterpc.Support_SUPPORT_UNSUPPORTED,
		FileToText:     translaterpc.Support_SUPPORT_SUPPORTED,
		FileToDocument: translaterpc.Support_SUPPORT_UNKNOWN,
	}, nil
}

func (protocolTranslator) Translate(_ context.Context, request *translaterpc.TranslateRequest) (*translaterpc.File, error) {
	return &translaterpc.File{Name: request.File.Name, Content: request.File.Content, ContentType: "text/plain"}, nil
}

type protocolSearcher struct {
	searchrpc.UnimplementedSearcherServer
	legacy bool
}

func (s protocolSearcher) Capabilities(ctx context.Context, request *searchrpc.CapabilitiesRequest) (*searchrpc.CapabilitiesResponse, error) {
	if s.legacy {
		return s.UnimplementedSearcherServer.Capabilities(ctx, request)
	}
	return &searchrpc.CapabilitiesResponse{DateFilters: true, Categories: []*searchrpc.Category{{Name: "publication", Description: "Research publications."}}}, nil
}

func (protocolSearcher) Search(_ context.Context, request *searchrpc.SearchRequest) (*searchrpc.SearchResponse, error) {
	// Echo the bounds in metadata so the test checks their serialized precision.
	return &searchrpc.SearchResponse{Results: []*searchrpc.Result{{
		Content: request.Query,
		Metadata: map[string]string{
			"since": request.Since.AsTime().Format(time.RFC3339Nano),
			"until": request.Until.AsTime().Format(time.RFC3339Nano),
		},
	}}}, nil
}

func TestCapabilityProtocolsOverGRPC(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "discovery", true: "legacy"}[legacy], func(t *testing.T) {
			listener := bufconn.Listen(1024 * 1024)
			server := grpc.NewServer()
			RegisterExtractorServer(server, protocolExtractor{legacy: legacy})
			translaterpc.RegisterTranslatorServer(server, protocolTranslator{legacy: legacy})
			searchrpc.RegisterSearcherServer(server, protocolSearcher{legacy: legacy})
			t.Cleanup(func() {
				server.Stop()
				listener.Close()
			})
			go server.Serve(listener)
			connection, err := grpc.NewClient("passthrough:///memory",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { connection.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			e := &Client{client: NewExtractorClient(connection)}
			if err := e.loadCapabilities(); err != nil {
				t.Fatal(err)
			}
			if e.Capabilities().UnknownFormats != legacy {
				t.Fatalf("extractor discovery = %+v", e.Capabilities())
			}
			if document, err := e.Extract(ctx, extractor.File{Name: "report.pdf", Content: []byte("PDF text")}, nil); err != nil || document.Text != "PDF text" {
				t.Fatalf("extractor operation = %+v, error %v", document, err)
			}

			translator := translaterpc.NewTranslatorClient(connection)
			modes, err := translator.Capabilities(ctx, &translaterpc.CapabilitiesRequest{})
			if legacy {
				if status.Code(err) != codes.Unimplemented {
					t.Fatalf("legacy translator discovery error %v", err)
				}
			} else if err != nil || !proto.Equal(modes, &translaterpc.CapabilitiesResponse{
				TextToText: translaterpc.Support_SUPPORT_UNSUPPORTED, FileToText: translaterpc.Support_SUPPORT_SUPPORTED, FileToDocument: translaterpc.Support_SUPPORT_UNKNOWN,
			}) {
				t.Fatalf("translator discovery = %v, error %v", modes, err)
			}
			file, err := translator.Translate(ctx, &translaterpc.TranslateRequest{Language: "de", File: &translaterpc.File{Name: "original.pdf", Content: []byte("Translated text")}})
			if err != nil || file.Name != "original.pdf" || string(file.Content) != "Translated text" || file.ContentType != "text/plain" {
				t.Fatalf("translator operation = %+v, error %v", file, err)
			}

			searcher := searchrpc.NewSearcherClient(connection)
			filters, err := searcher.Capabilities(ctx, &searchrpc.CapabilitiesRequest{})
			if legacy {
				if status.Code(err) != codes.Unimplemented {
					t.Fatalf("legacy search discovery error %v", err)
				}
			} else if err != nil || !proto.Equal(filters, &searchrpc.CapabilitiesResponse{DateFilters: true, Categories: []*searchrpc.Category{{Name: "publication", Description: "Research publications."}}}) {
				t.Fatalf("search discovery = %v, error %v", filters, err)
			}
			since := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
			until := since.Add(time.Hour)
			result, err := searcher.Search(ctx, &searchrpc.SearchRequest{Query: "Research", Since: timestamppb.New(since), Until: timestamppb.New(until)})
			if err != nil || len(result.Results) != 1 || result.Results[0].Metadata["since"] != since.Format(time.RFC3339Nano) || result.Results[0].Metadata["until"] != until.Format(time.RFC3339Nano) {
				t.Fatalf("search bounds = %+v, error %v", result, err)
			}
		})
	}
}
