package anthropic

import (
	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go"
)

// cacheControl builds a breakpoint with the retention the client asked for:
// the breakpoint's own, or the request's.
func cacheControl(options *provider.CacheOptions, control *provider.CacheControl) anthropic.BetaCacheControlEphemeralParam {
	result := anthropic.NewBetaCacheControlEphemeralParam()
	retention := provider.CacheRetentionDefault
	if options != nil {
		retention = options.Retention
	}
	if control != nil && control.Retention != "" {
		retention = control.Retention
	}
	if retention == provider.CacheRetentionExtended {
		result.TTL = anthropic.BetaCacheControlEphemeralTTLTTL1h
	}
	return result
}

// cacheBreakpoints returns a predicate that honors the client's breakpoints
// in explicit cache mode, limited to the last four the API accepts. Call it
// in the order the parts are written to the request.
func cacheBreakpoints(messages []provider.Message, explicit bool) func(provider.Content) bool {
	if !explicit {
		return func(provider.Content) bool { return false }
	}
	total := 0
	for _, m := range messages {
		for _, c := range m.Content {
			if c.CacheControl != nil {
				total++
			}
		}
	}
	skip := max(total-4, 0)
	seen := 0
	return func(c provider.Content) bool {
		if c.CacheControl == nil {
			return false
		}
		seen++
		return seen > skip
	}
}
