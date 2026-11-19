package shared

import "github.com/adrianliechti/wingman/pkg/provider"

func ParseTier(tier string) provider.Tier {
	switch tier {
	case "default", "standard":
		return provider.TierStandard
	case "flex":
		return provider.TierFlex
	case "priority", "fast":
		return provider.TierPriority
	case "ultrafast":
		return provider.TierUltra
	default:
		return ""
	}
}

func ServiceTier(tier provider.Tier) string {
	switch tier {
	case provider.TierStandard:
		return "default"
	case provider.TierFlex:
		return "flex"
	case provider.TierPriority:
		return "priority"
	case provider.TierUltra:
		return "ultrafast"
	default:
		return ""
	}
}
