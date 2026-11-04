package server

import (
	"net/http"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/server/anthropic"
	"github.com/adrianliechti/wingman/server/api"
	"github.com/adrianliechti/wingman/server/exa"
	"github.com/adrianliechti/wingman/server/gemini"
	"github.com/adrianliechti/wingman/server/mcp"
	"github.com/adrianliechti/wingman/server/openai"
	"github.com/adrianliechti/wingman/server/tavily"
	"github.com/adrianliechti/wingman/server/web"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Server struct {
	*config.Config
	http.Handler

	api *api.Handler
	mcp *mcp.Handler

	openai    *openai.Handler
	anthropic *anthropic.Handler
	gemini    *gemini.Handler
	exa       *exa.Handler
	tavily    *tavily.Handler
}

func New(cfg *config.Config) (*Server, error) {
	api := api.New(cfg)
	mcp := mcp.New(cfg)
	openai := openai.New(cfg)
	anthropic := anthropic.New(cfg)
	gemini := gemini.New(cfg)
	exaHandler := exa.New(cfg)
	tavilyHandler := tavily.New(cfg)

	mux := chi.NewMux()

	s := &Server{
		Config:  cfg,
		Handler: mux,

		api: api,
		mcp: mcp,

		openai:    openai,
		anthropic: anthropic,
		gemini:    gemini,
		exa:       exaHandler,
		tavily:    tavilyHandler,
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
	mux.Use(handleRouteTag)
	mux.Use(handleRequestDecompression)
	mux.Use(web.Credentials)
	mux.Use(s.handleAuth)

	mux.Route("/v1", func(r chi.Router) {
		s.api.Attach(r)
		s.mcp.Attach(r)
		s.openai.Attach(r)
		s.anthropic.Attach(r)
		s.exa.Attach(r)
		s.tavily.Attach(r)
	})

	mux.Route("/v1beta", func(r chi.Router) {
		s.gemini.Attach(r)
	})

	return s, nil
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.Address, s)
}
