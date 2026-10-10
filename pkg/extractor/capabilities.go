package extractor

import (
	"mime"
	"path"
	"slices"
	"strings"
)

type Capabilities struct {
	MediaTypes []string
	Extensions []string
	// UnknownFormats keeps files not matched by the lists eligible for provider
	// validation. This covers content inspection and unknown service formats.
	UnknownFormats bool
}

// MaySupport checks whether a file is a candidate, not whether its contents are
// valid. Providers still validate files and can return ErrUnsupported.
func (c Capabilities) MaySupport(file File) bool {
	if c.UnknownFormats {
		return true
	}
	if slices.ContainsFunc(c.Extensions, func(extension string) bool {
		return strings.EqualFold(extension, path.Ext(file.Name)) && extension != ""
	}) {
		return true
	}
	mediaType, _, _ := mime.ParseMediaType(strings.TrimSpace(file.ContentType))
	for _, supported := range c.MediaTypes {
		supported = strings.ToLower(strings.TrimSpace(supported))
		if supported == "" {
			continue
		}
		if mediaType == supported || (strings.HasSuffix(supported, "/*") && strings.HasPrefix(mediaType, strings.TrimSuffix(supported, "*"))) {
			return true
		}
	}
	return false
}
