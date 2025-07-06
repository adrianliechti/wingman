package server

import (
	"net/http"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/server/api"
	"github.com/adrianliechti/wingman/server/index"
	"github.com/adrianliechti/wingman/server/mcp"
	"github.com/adrianliechti/wingman/server/openai"
	"github.com/adrianliechti/wingman/server/unstructured"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Server struct {
	*config.Config
	http.Handler

	api    *api.Handler
	mcp    *mcp.Handler
	index  *index.Handler
	openai *openai.Handler

	unstructured *unstructured.Handler
}

func New(cfg *config.Config) (*Server, error) {
	api, err := api.New(cfg)

	if err != nil {
		return nil, err
	}

	mcp, err := mcp.New(cfg)

	if err != nil {
		return nil, err
	}

	openai, err := openai.New(cfg)

	if err != nil {
		return nil, err
	}

	index, err := index.New(cfg)

	if err != nil {
		return nil, err
	}

	unstructured, err := unstructured.New(cfg)

	if err != nil {
		return nil, err
	}

	mux := chi.NewMux()

	s := &Server{
		Config:  cfg,
		Handler: mux,

		api: api,
		mcp: mcp,

		index:  index,
		openai: openai,

		unstructured: unstructured,
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

		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,

		MaxAge: 300,
	}))

	mux.Use(otelhttp.NewMiddleware("http"))

	mux.Use(s.handleAuth)

	mux.Handle("/files/*", http.FileServer(http.Dir("public")))

	mux.Route("/v1", func(r chi.Router) {
		s.api.Attach(r)
		s.mcp.Attach(r)
		s.openai.Attach(r)

		s.unstructured.Attach(r)
	})

	mux.Route("/v1/index", func(r chi.Router) {
		s.index.Attach(r)
	})

	for name, handler := range cfg.APIs {
		mux.Mount("/api/"+name, handler)
	}

	return s, nil
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.Address, s)
}

func (s *Server) handleAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var authorized = len(s.Authorizers) == 0

		for _, a := range s.Authorizers {
			if err := a.Verify(ctx, r); err == nil {
				authorized = true
				break
			}
		}

		if !authorized {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
