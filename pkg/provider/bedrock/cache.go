package bedrock

import (
	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// cachePolicy decides where Converse cache points go: the automatic points
// after system, tools and the last user message, or, in explicit mode, only
// after the client's breakpoints.
type cachePolicy struct {
	explicit bool
	mark     func(provider.Content) bool
}

func (p cachePolicy) marks(c provider.Content) bool {
	return p.mark != nil && p.mark(c)
}

// newCachePolicy honors breakpoints in explicit mode, limited to the last
// four Converse accepts. Call marks in the order the blocks are written.
func newCachePolicy(messages []provider.Message, options *provider.CompleteOptions) cachePolicy {
	explicit := options != nil && options.CacheOptions != nil && options.CacheOptions.Mode == provider.CacheModeExplicit
	if !explicit {
		return cachePolicy{}
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
	return cachePolicy{explicit: true, mark: func(c provider.Content) bool {
		if c.CacheControl == nil {
			return false
		}
		seen++
		return seen > skip
	}}
}

func cachePoint() types.CachePointBlock {
	return types.CachePointBlock{Type: types.CachePointTypeDefault}
}
