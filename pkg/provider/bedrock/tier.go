package bedrock

import (
	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func serviceTier(tier provider.Tier) *types.ServiceTier {
	switch tier {
	case provider.TierStandard:
		return &types.ServiceTier{Type: types.ServiceTierTypeDefault}
	case provider.TierFlex:
		return &types.ServiceTier{Type: types.ServiceTierTypeFlex}
	case provider.TierPriority:
		return &types.ServiceTier{Type: types.ServiceTierTypePriority}
	default:
		return nil
	}
}

func tierFromServiceTier(tier types.ServiceTierType) provider.Tier {
	switch tier {
	case types.ServiceTierTypeDefault:
		return provider.TierStandard
	case types.ServiceTierTypeFlex:
		return provider.TierFlex
	case types.ServiceTierTypePriority:
		return provider.TierPriority
	default:
		return ""
	}
}
