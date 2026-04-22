package main

import (
	"net/http"
	"time"

	"github.com/mysecodgit/go_accounting/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"go.uber.org/zap"
)

type application struct {
	config config
	store  store.Storage
	logger *zap.SugaredLogger
}

type config struct {
	addr        string
	db          dbConfig
	env         string
	apiURL      string
	frontendURL string
	jwtSecret   string
	auth        authConfig
}

type authConfig struct {
	basic basicConfig
}

type basicConfig struct {
	user string
	pass string
}

type dbConfig struct {
	addr         string
	maxOpenConns int
	maxIdleConns int
	maxIdleTime  string
}

func (app *application) mount() http.Handler {

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(middleware.Timeout(60 * time.Second))

	//cors
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{app.config.frontendURL}, // frontend URL
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300, // Max age in seconds
	}))

	r.Route("/v1", func(r chi.Router) {
		r.Get("/health", app.checkHealthHandler)

		r.Post("/auth/login", app.loginHandler)
		r.Get("/public/wedding/{weddingID}", app.getPublicWeddingHandler)
		r.Get("/public/qr/{token}", app.getPublicQrHandler)
		r.Get("/public/qr/{token}/image.png", app.getPublicQrImageHandler)
		r.Post("/claim", app.claimQrCodeHandler)

		r.Group(func(r chi.Router) {
			r.Use(app.authenticate)

			r.Route("/user", func(r chi.Router) {
				r.Get("/", app.getUsersHandler)

				r.Route("/{userID}", func(r chi.Router) {
					r.Use(app.requireURLUserIDMatchesJWT)
					r.Get("/weddings", app.getWeddingsHandler)
				})
			})

			r.Post("/wedding", app.createWeddingHandler)
			r.Put("/wedding", app.updateWeddingHandler)

			r.Get("/qr-codes", app.listQrCodesHandler)
			r.Get("/claims", app.listClaimsHandler)
			r.Get("/qr-codes/range", app.listQrCodesRangeHandler)
			r.Post("/qr-codes/generate", app.generateQrCodesHandler)
			r.Post("/qr-codes/activate", app.activateQrCodesHandler)
			r.Post("/qr-codes/assign", app.assignQrCodesHandler)
			r.Post("/qr-codes/unassign", app.unassignQrCodesHandler)
			r.Post("/qr-codes/export/pdf", app.exportQrCodesPDFHandler)
		})

		// r.Route("/posts", func(r chi.Router) {
		// 	r.Post("/", app.createPostHandler)

		// 	r.Route("/{postID}", func(r chi.Router) {
		// 		r.Use(app.postsContextMiddleware)

		// 		r.Get("/", app.getPostHandler)
		// 		r.Delete("/", app.deletePostHandler)
		// 		r.Patch("/", app.updatePostHandler)

		// 	})
		// })

		// r.Route("/users", func(r chi.Router) {
		// 	r.Put("/activate/{token}", app.activateUserHandler)

		// 	r.Route("/{userID}", func(r chi.Router) {
		// 		r.Use(app.userContextMiddleware)

		// 		r.Get("/", app.getUserHandler)
		// 		r.Put("/follow", app.followUserHandler)
		// 		r.Put("/unfollow", app.unfollowUserHandler)
		// 	})

		// 	r.Group(func(r chi.Router) {
		// 		r.Get("/feed", app.getUserFeedHandler)
		// 	})
		// })

		// r.Route("/authentication", func(r chi.Router) {
		// 	r.Post("/user", app.registerUserHandler)
		// })
	})

	return r
}

func (app *application) run(mux http.Handler) error {
	srv := &http.Server{
		Addr:         app.config.addr,
		Handler:      mux,
		WriteTimeout: time.Second * 30,
		ReadTimeout:  time.Second * 10,
		IdleTimeout:  time.Minute,
	}

	app.logger.Infow("server has started", "addr", app.config.addr, "env", app.config.env)

	return srv.ListenAndServe()
}
