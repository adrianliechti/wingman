package custom

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/budget"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ budget.Provider = (*Client)(nil)

type Client struct {
	conn    *grpc.ClientConn
	client  BudgetClient
	token   string
	timeout time.Duration
}

func New(target string, options ...Option) (*Client, error) {
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" || (u.Scheme != "grpc" && u.Scheme != "grpcs") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("budget URL must be grpc://host:port or grpcs://host:port")
	}
	c := &Client{timeout: 5 * time.Second}
	for _, option := range options {
		option(c)
	}
	if c.timeout <= 0 {
		return nil, errors.New("budget timeout must be positive")
	}
	// The token travels as plain metadata, so it needs TLS unless the budget
	// service is a local sidecar.
	if c.token != "" && u.Scheme == "grpc" && !isLoopback(u.Hostname()) {
		return nil, errors.New("budget token requires grpcs:// for non-local hosts")
	}
	var transport credentials.TransportCredentials = insecure.NewCredentials()
	if u.Scheme == "grpcs" {
		transport = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	c.conn, err = grpc.NewClient(u.Host, grpc.WithTransportCredentials(transport))
	if err != nil {
		return nil, err
	}
	c.client = NewBudgetClient(c.conn)
	return c, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) context(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token)
	}
	return context.WithTimeout(ctx, c.timeout)
}

func (c *Client) Check(ctx context.Context, userID string) (*budget.Result, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	response, err := c.client.Check(ctx, &CheckRequest{UserId: userID})
	if err != nil {
		return nil, err
	}
	result := &budget.Result{Allowed: response.Allowed, Message: response.Message}
	if response.ResetsAt != nil {
		if err := response.ResetsAt.CheckValid(); err != nil {
			return nil, err
		}
		result.ResetsAt = response.ResetsAt.AsTime()
	}
	return result, nil
}

func (c *Client) Report(ctx context.Context, report *budget.Report) error {
	ctx, cancel := c.context(ctx)
	defer cancel()
	req := &ReportRequest{
		OperationId: report.OperationID,
		UserId:      report.UserID,
		Provider:    report.Provider,
		Model:       report.Model,
		ServiceTier: report.ServiceTier,
		CompletedAt: timestamppb.New(report.CompletedAt),
	}
	if u := report.Usage; u != nil {
		req.Usage = &Usage{
			InputTokens: int64(u.InputTokens), OutputTokens: int64(u.OutputTokens),
			CacheReadInputTokens: int64(u.CacheReadInputTokens), CacheCreationInputTokens: int64(u.CacheCreationInputTokens),
		}
		if u.ReasoningTokens != nil {
			req.Usage.ReasoningTokens = new(int64(*u.ReasoningTokens))
		}
		if d := u.CacheWriteTokensDetails; d != nil {
			req.Usage.CacheWriteTokensDetails = &CacheWriteTokensDetails{StandardTokens: int64(d.StandardTokens), ExtendedTokens: int64(d.ExtendedTokens)}
		}
	}
	for attempt := 0; ; attempt++ {
		_, err := c.client.Report(ctx, req)
		if err == nil || attempt == 2 || (status.Code(err) != codes.Unavailable && status.Code(err) != codes.DeadlineExceeded) {
			return err
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
