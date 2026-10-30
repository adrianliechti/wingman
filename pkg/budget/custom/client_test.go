package custom

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/budget"
	"github.com/adrianliechti/wingman/pkg/provider"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testPlugin struct {
	UnimplementedBudgetServer
	mu      sync.Mutex
	users   []string
	reports []*ReportRequest
	tokens  []string
}

func (p *testPlugin) Check(ctx context.Context, r *CheckRequest) (*CheckResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.users = append(p.users, r.UserId)
	md, _ := metadata.FromIncomingContext(ctx)
	p.tokens = append(p.tokens, md.Get("authorization")...)
	return &CheckResponse{Allowed: true}, nil
}
func (p *testPlugin) Report(ctx context.Context, r *ReportRequest) (*emptypb.Empty, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reports = append(p.reports, r)
	md, _ := metadata.FromIncomingContext(ctx)
	p.tokens = append(p.tokens, md.Get("authorization")...)
	if len(p.reports) == 1 {
		return nil, status.Error(codes.Unavailable, "retry")
	}
	return &emptypb.Empty{}, nil
}

func TestLocalPluginTokenUsageAndRetry(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	plugin := &testPlugin{}
	RegisterBudgetServer(server, plugin)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	client, err := New("grpc://"+listener.Addr().String(), WithToken("local-plugin"), WithTimeout(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	decision, err := client.Check(t.Context(), "user-7")
	if err != nil || !decision.Allowed {
		t.Fatalf("check: %+v %v", decision, err)
	}
	err = client.Report(t.Context(), &budget.Report{OperationID: "stable-operation", UserID: "user-7", Provider: "anthropic", Model: "upstream", CompletedAt: time.Now(), Usage: &budget.Usage{InputTokens: 100, OutputTokens: 20, ReasoningTokens: new(5), CacheCreationInputTokens: 60, CacheWriteTokensDetails: &provider.CacheWriteTokensDetails{StandardTokens: 10, ExtendedTokens: 50}}})
	if err != nil {
		t.Fatal(err)
	}
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	if len(plugin.users) != 1 || plugin.users[0] != "user-7" || len(plugin.reports) != 2 || len(plugin.tokens) != 3 {
		t.Fatalf("wrong plugin exchange: checks=%v reports=%d tokens=%d", plugin.users, len(plugin.reports), len(plugin.tokens))
	}
	for _, token := range plugin.tokens {
		if token != "Bearer local-plugin" {
			t.Error("local plugin token not forwarded")
		}
	}
	for _, r := range plugin.reports {
		if r.OperationId != "stable-operation" || r.UserId != "user-7" || r.Model != "upstream" || r.Usage.CacheWriteTokensDetails.GetExtendedTokens() != 50 || r.Usage.CacheWriteTokensDetails.GetStandardTokens() != 10 || r.Usage.GetReasoningTokens() != 5 {
			t.Fatalf("accounting or retry identity changed: %v", r)
		}
	}
}

func TestTokenRequiresTLSOutsideLoopback(t *testing.T) {
	for target, ok := range map[string]bool{
		"grpc://budget.internal:9000":  false,
		"grpc://10.0.0.5:9000":         false,
		"grpc://localhost:9000":        true,
		"grpc://127.0.0.1:9000":        true,
		"grpc://[::1]:9000":            true,
		"grpcs://budget.internal:9000": true,
	} {
		client, err := New(target, WithToken("secret"))
		if (err == nil) != ok {
			t.Errorf("%s: err=%v, want ok=%t", target, err, ok)
		}
		if client != nil {
			client.Close()
		}
	}
	client, err := New("grpc://budget.internal:9000")
	if err != nil {
		t.Fatalf("tokenless plaintext rejected: %v", err)
	}
	client.Close()
}
