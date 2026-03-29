package server

import (
	"net/http"

	"github.com/adrianliechti/wingman/config"

	"github.com/adrianliechti/wingman/server/anthropic"
	"github.com/adrianliechti/wingman/server/api"
	"github.com/adrianliechti/wingman/server/gemini"
	"github.com/adrianliechti/wingman/server/mcp"
	"github.com/adrianliechti/wingman/server/openai"
	"github.com/adrianliechti/wingman/server/otel"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Server struct {
	*config.Config
	http.Handler

	api  *api.Handler
	mcp  *mcp.Handler
	otel *otel.Handler

	openai    *openai.Handler
	anthropic *anthropic.Handler
	gemini    *gemini.Handler
}

func New(cfg *config.Config) (*Server, error) {
	api := api.New(cfg)
	mcp := mcp.New(cfg)
	otel := otel.New()
	openai := openai.New(cfg)
	anthropic := anthropic.New(cfg)
	gemini := gemini.New(cfg)

	mux := chi.NewMux()

	s := &Server{
		Config:  cfg,
		Handler: mux,

		api:  api,
		mcp:  mcp,
		otel: otel,

		openai:    openai,
		anthropic: anthropic,
		gemini:    gemini,
	}

	mux.Use(middleware.Logger)
	mux.Use(middleware.Recoverer)

	mux.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},

		AllowedMethods: []string{
			http.MethodHead,
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions,
		},

		AllowedHeaders: []string{"*"},

		MaxAge: 300,
	}))

	mux.Use(otelhttp.NewMiddleware("http"))
	mux.Use(s.handleAuth)

	mux.Route("/v1", func(r chi.Router) {
		s.api.Attach(r)
		s.mcp.Attach(r)
		s.otel.Attach(r)
		s.openai.Attach(r)
		s.anthropic.Attach(r)
	})

	mux.Route("/v1beta", func(r chi.Router) {
		s.gemini.Attach(r)
	})

	return s, nil
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.Address, s)
}
