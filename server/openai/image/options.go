package image

import (
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func parseAspect(size, aspect string) provider.AspectRatio {
	if a := provider.ParseAspect(aspect); a != "" {
		return a
	}

	return provider.ParseSizeAspect(size)
}

func parseQuality(value string) provider.Quality {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "hd":
		return provider.QualityHigh
	case "standard":
		return provider.QualityMedium
	}

	return provider.ParseQuality(value)
}
