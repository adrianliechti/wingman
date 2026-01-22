package header

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
