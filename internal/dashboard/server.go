package dashboard

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/zyrak/hivemind/internal/state"
)

type Config struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	CORSAllowOrigin string
	Logger          *slog.Logger
}

func DefaultConfig() Config {
	return Config{
		Host:            "0.0.0.0",
		Port:            8080,
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     60 * time.Second,
		CORSAllowOrigin: "*",
		Logger:          slog.Default(),
	}
}

func NewServer(store *state.Store, cfg Config) *http.Server {
	cfg = normalizeConfig(cfg)

	handlers := NewHandlers(store, cfg.Logger, time.Now().UTC())
	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux)
	registerFrontendRoutes(mux, cfg.Logger)

	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	return &http.Server{
		Addr:         address,
		Handler:      withCORS(mux, cfg.CORSAllowOrigin),
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
}

func normalizeConfig(cfg Config) Config {
	defaults := DefaultConfig()

	if cfg.Host == "" {
		cfg.Host = defaults.Host
	}
	if cfg.Port <= 0 {
		cfg.Port = defaults.Port
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = defaults.ReadTimeout
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = defaults.WriteTimeout
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaults.IdleTimeout
	}
	if cfg.CORSAllowOrigin == "" {
		cfg.CORSAllowOrigin = defaults.CORSAllowOrigin
	}
	if cfg.Logger == nil {
		cfg.Logger = defaults.Logger
	}

	return cfg
}

func withCORS(next http.Handler, allowOrigin string) http.Handler {
	if allowOrigin == "" {
		allowOrigin = "*"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
