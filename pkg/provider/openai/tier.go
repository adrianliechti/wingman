package openai

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
)

func serviceTier(tier provider.Tier) string {
	switch tier {
	case provider.TierStandard:
		return "default"
	case provider.TierFlex:
		return "flex"
	case provider.TierPriority:
		return "priority"
	default:
		return ""
	}
}

func tierFromServiceTier(tier string) provider.Tier {
	switch tier {
	case "default", "standard":
		return provider.TierStandard
	case "flex":
		return provider.TierFlex
	case "priority", "fast":
		return provider.TierPriority
	default:
		return ""
	}
}

// Some compatible endpoints reject the tier parameter altogether or only
// accept their own tier vocabulary. Retry without that preference only when
// the upstream explicitly rejects it, before any completion was delivered.
func unsupportedTier(err error) bool {
	apiError, ok := errors.AsType[*openai.Error](err)
	return ok && (apiError.StatusCode == 400 || apiError.StatusCode == 422) &&
		(apiError.Param == "service_tier" || strings.Contains(strings.ToLower(apiError.Message), "service_tier"))
}
