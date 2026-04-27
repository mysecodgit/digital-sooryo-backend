package main

import (
	"github.com/mysecodgit/go_accounting/internal/db"
	"github.com/mysecodgit/go_accounting/internal/env"
	"github.com/mysecodgit/go_accounting/internal/store"
	"github.com/joho/godotenv"

	"go.uber.org/zap"
)

const version = "0.0.1"

func main() {
	// Load environment variables from .env if present.
	// This keeps local development consistent across OSes without requiring direnv.
	_ = godotenv.Load()

	cfg := config{
		addr: env.GetString("ADDR", ":8080"),
		db: dbConfig{
			addr:         env.GetString("DB_ADDR", "root:@tcp(localhost:3306)/digital_sooryo?parseTime=true&charset=utf8mb4"),
			maxOpenConns: env.GetInt("DB_MAX_OPEN_CONNS", 30),
			maxIdleConns: env.GetInt("DB_MAX_IDLE_CONNS", 30),
			maxIdleTime:  env.GetString("DB_MAX_IDLE_TIME", "15m"),
		},
		env:         env.GetString("ENV", "development"),
		// Public base URL of this API, used in QR payloads (should include scheme).
		apiURL:      env.GetString("EXTERNAL_URL", "http://localhost:5075"),
		frontendURL: env.GetString("FRONTEND_URL", "http://localhost:5174"),
		jwtSecret:   env.GetString("JWT_SECRET", "dev-insecure-change-me"),
		auth: authConfig{
			basic: basicConfig{
				user: env.GetString("AUTH_BASIC_USER", "admin"),
				pass: env.GetString("AUTH_BASIC_PASS", "admin"),
			},
		},
	}

	// Logger
	logger := zap.Must(zap.NewProduction()).Sugar()
	defer logger.Sync()

	// Database

	db, err := db.New(
		cfg.db.addr,
		cfg.db.maxOpenConns,
		cfg.db.maxIdleConns,
		cfg.db.maxIdleTime,
	)

	if err != nil {
		logger.Fatal(err)
	}

	defer db.Close()

	logger.Info("database connection pool established.")

	store := store.NewStorage(db)

	app := &application{
		config: cfg,
		store:  store,
		logger: logger,
	}

	mux := app.mount()
	logger.Fatal(app.run(mux))
}
