package anthropic

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

func speed(tier provider.Tier) anthropic.BetaMessageNewParamsSpeed {
	switch tier {
	case provider.TierStandard:
		return anthropic.BetaMessageNewParamsSpeedStandard
	case provider.TierPriority:
		return anthropic.BetaMessageNewParamsSpeedFast
	default:
		return ""
	}
}

func effectiveTier(usage anthropic.BetaUsage) provider.Tier {
	switch usage.Speed {
	case anthropic.BetaUsageSpeedStandard:
		return provider.TierStandard
	case anthropic.BetaUsageSpeedFast:
		return provider.TierPriority
	default:
		return ""
	}
}

func fastUnavailable(err error) bool {
	apiError, ok := errors.AsType[*anthropic.Error](err)
	if !ok {
		return false
	}
	switch apiError.StatusCode {
	case 429, 529:
		return true
	case 400, 422:
		message, _ := extractAnthropicErrorInfo(apiError)
		message = strings.ToLower(message)
		return strings.Contains(message, "speed") || strings.Contains(message, "fast")
	default:
		return false
	}
}
