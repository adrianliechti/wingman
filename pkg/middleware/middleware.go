package middleware

import "slices"

type Middleware[T any] func(T) T

func Chain[T any](provider T, middlewares ...Middleware[T]) T {
	for _, wrap := range slices.Backward(middlewares) {
		provider = wrap(provider)
	}
	return provider
}
