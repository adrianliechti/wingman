package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/internal/claude"

	"github.com/anthropics/anthropic-sdk-go/v2/bedrock"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

type Config struct {
	url string

	token string
	model string

	client     *http.Client
	maxRetries *int
}

type Option func(*Config)

func WithClient(client *http.Client) Option {
	return func(c *Config) {
		c.client = client
	}
}

func WithToken(token string) Option {
	return func(c *Config) {
		c.token = token
	}
}

func WithMaxRetries(retries int) Option {
	return func(c *Config) {
		c.maxRetries = &retries
	}
}

func (cfg *Config) Options() ([]option.RequestOption, error) {
	baseURL := cfg.url

	if baseURL == "" {
		baseURL = "https://api.anthropic.com/"
	}

	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")

	if err != nil {
		return nil, fmt.Errorf("anthropic: invalid URL: %w", err)
	}

	client := cfg.client

	if client == nil {
		client = provider.DefaultClient
	}

	options := []option.RequestOption{
		option.WithHTTPClient(client),
	}

	if cfg.maxRetries != nil {
		options = append(options, option.WithMaxRetries(*cfg.maxRetries))
	}

	service, region := bedrockEndpoint(endpoint.Hostname())

	switch service {
	case "bedrock-mantle":
		if endpoint.Path == "/" {
			endpoint.Path = "/anthropic/"
		}

		client, err := bedrock.NewMantleClient(context.Background(), bedrock.MantleClientConfig{
			APIKey: cfg.token, AWSRegion: region, BaseURL: endpoint.String(),
		}, options...)

		if err != nil {
			return nil, fmt.Errorf("anthropic: configure Bedrock Mantle: %w", err)
		}

		return client.Options, nil

	case "bedrock-runtime":
		var awsConfig aws.Config

		if cfg.token != "" {
			awsConfig = aws.Config{Region: region, BearerAuthTokenProvider: bedrock.NewStaticBearerTokenProvider(cfg.token)}
		} else {
			awsConfig, err = awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region), awsconfig.WithHTTPClient(client))

			if err != nil {
				return nil, fmt.Errorf("anthropic: configure Bedrock Runtime: %w", err)
			}
		}

		// The SDK's Runtime adapter uses InvokeModel at the endpoint root.
		endpoint.Path, endpoint.RawPath = "/", ""
		options = append(options, bedrock.WithConfig(awsConfig), option.WithBaseURL(endpoint.String()))

		if cfg.token != "" {
			// WithConfig prefers AWS_BEARER_TOKEN_BEDROCK over its token provider.
			// Apply our explicit token after its bearer-auth middleware instead.
			options = append(options, option.WithMiddleware(func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
				r.Header.Set("Authorization", "Bearer "+cfg.token)
				return next(r)
			}))
		}

		// Older Bedrock models do not accept the SDK v2 API version.
		if claude.MatchesModel(cfg.model, LegacyModels) || claude.MatchesModel(cfg.model, []string{"sonnet-4-6", "opus-4-6", "sonnet-4-2025", "opus-4-2025"}) {
			options = append(options, option.WithHeader("anthropic-version", "bedrock-2023-05-31"))
		}

		return options, nil

	default:
		options = append(options, option.WithBaseURL(endpoint.String()))

		if cfg.token != "" {
			options = append(options, option.WithAPIKey(cfg.token))
		}

		return options, nil
	}
}

// Match AWS endpoint hostnames, including FIPS, dual-stack, and China endpoints.
func bedrockEndpoint(host string) (service, region string) {
	host = strings.ToLower(host)
	for _, suffix := range []string{".amazonaws.com.cn", ".amazonaws.com", ".api.aws"} {
		prefix, ok := strings.CutSuffix(host, suffix)
		if !ok {
			continue
		}
		service, region, ok = strings.Cut(prefix, ".")
		service = strings.TrimSuffix(service, "-fips")
		if ok && region != "" && !strings.Contains(region, ".") && (service == "bedrock-runtime" || service == "bedrock-mantle") {
			return service, region
		}
	}
	return "", ""
}
