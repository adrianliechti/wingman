package google

import (
	"github.com/adrianliechti/wingman/pkg/provider"

	"google.golang.org/genai/interactions/models/interactions"
)

func (s *interactionState) setTier(tier *interactions.ServiceTier) {
	if tier != nil {
		if value := tierFromServiceTier(*tier); value != "" {
			s.tier = value
		}
	}
}

func serviceTier(tier provider.Tier) *interactions.ServiceTier {
	switch tier {
	case provider.TierStandard:
		return new(interactions.ServiceTierStandard)
	case provider.TierFlex:
		return new(interactions.ServiceTierFlex)
	case provider.TierPriority:
		return new(interactions.ServiceTierPriority)
	default:
		return nil
	}
}

func tierFromServiceTier(tier interactions.ServiceTier) provider.Tier {
	switch tier {
	case interactions.ServiceTierStandard:
		return provider.TierStandard
	case interactions.ServiceTierFlex:
		return provider.TierFlex
	case interactions.ServiceTierPriority:
		return provider.TierPriority
	default:
		return ""
	}
}
