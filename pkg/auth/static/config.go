package static

import (
	"regexp"
)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func WithUserHeader(val string) Option {
	return func(p *Provider) {
		p.userHeader = val
	}
}

func WithEmailHeader(val string) Option {
	return func(p *Provider) {
		p.emailHeader = val
	}
}
