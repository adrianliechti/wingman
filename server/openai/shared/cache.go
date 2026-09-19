package shared

import "github.com/adrianliechti/wingman/pkg/provider"

// CacheOptions maps prompt_cache_key, prompt_cache_retention and the
// prompt_cache_options mode onto the provider's caching intent. Backends
// cache the stable prefix by default; "24h" asks for extended retention, and
// "explicit" caches only at the client's breakpoints. Other values keep the
// backend default.
func CacheOptions(key, retention *string, mode string) *provider.CacheOptions {
	options := provider.CacheOptions{}
	if key != nil {
		options.Key = *key
	}
	if retention != nil && *retention == "24h" {
		options.Retention = provider.CacheRetentionExtended
	}
	if mode == "explicit" {
		options.Mode = provider.CacheModeExplicit
	}
	if options == (provider.CacheOptions{}) {
		return nil
	}
	return &options
}
