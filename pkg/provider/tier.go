package provider

// Tier selects a backend's processing tier. Availability depends on
// the provider, model, region, and account. An empty value keeps its default.
type Tier string

const (
	TierStandard Tier = "standard"
	TierFlex     Tier = "flex"
	TierPriority Tier = "priority"
	TierUltra    Tier = "ultra"
)
