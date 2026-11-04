package shared

import (
	"fmt"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type PromptCacheOptions struct {
	Mode string `json:"mode,omitzero"`
	TTL  string `json:"ttl,omitzero"`
}

func (o *PromptCacheOptions) Validate() error {
	if o == nil {
		return nil
	}
	if o.Mode != "" && o.Mode != "implicit" && o.Mode != "explicit" {
		return fmt.Errorf("prompt_cache_options.mode: must be implicit or explicit")
	}
	_, err := CacheRetention(o.TTL)
	return err
}

func CacheRetention(ttl string) (provider.CacheRetention, error) {
	switch ttl {
	case "":
		return provider.CacheRetentionDefault, nil
	case "30m":
		return provider.CacheRetentionStandard, nil
	case "12h":
		return provider.CacheRetentionExtended, nil
	default:
		return "", fmt.Errorf("cache ttl: must be 30m or 12h")
	}
}

func CacheOptions(key, retention *string, cache *PromptCacheOptions) *provider.CacheOptions {
	options := provider.CacheOptions{}
	if key != nil {
		options.Key = *key
	}
	if retention != nil && *retention == "24h" {
		options.Retention = provider.CacheRetentionExtended
	}
	if cache != nil {
		if cache.Mode == "explicit" {
			options.Mode = provider.CacheModeExplicit
		}
		if cache.TTL != "" {
			options.Retention, _ = CacheRetention(cache.TTL)
		}
	}
	if options == (provider.CacheOptions{}) {
		return nil
	}
	return &options
}
