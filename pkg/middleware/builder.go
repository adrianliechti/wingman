package middleware

type Builder[T any] struct {
	provider    T
	middlewares []Middleware[T]
}

func NewBuilder[T any](provider T) *Builder[T] {
	return &Builder[T]{provider: provider}
}

func (b *Builder[T]) Use(m ...Middleware[T]) {
	b.middlewares = append(b.middlewares, m...)
}

func (b *Builder[T]) Build() T {
	return Chain(b.provider, b.middlewares...)
}
