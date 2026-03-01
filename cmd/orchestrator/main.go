package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/zyrakk/hivemind/internal/dashboard"
	"github.com/zyrakk/hivemind/internal/evaluator"
	"github.com/zyrakk/hivemind/internal/launcher"
	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/notify"
	"github.com/zyrakk/hivemind/internal/planner"
	"github.com/zyrakk/hivemind/internal/state"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databasePath := envOrDefault("DATABASE_PATH", "./hivemind.db")
	configPath := envOrDefault("CONFIG_PATH", "./config.yaml")
	telegramChatID := envInt64OrDefault("TELEGRAM_CHAT_ID", 0)

	zaiAPIKey := os.Getenv("ZAI_API_KEY")
	anthropicAPIKey := os.Getenv("ANTHROPIC_API_KEY")
	googleAIAPIKey := os.Getenv("GOOGLE_AI_API_KEY")
	telegramBotToken := os.Getenv("TELEGRAM_BOT_TOKEN")

	store, err := state.New(databasePath)
	if err != nil {
		logger.Error("failed to initialize state store", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			logger.Error("failed to close state store", slog.Any("error", closeErr))
		}
	}()

	glmClient := llm.NewGLMClient(zaiAPIKey, "glm-4.7", "https://api.z.ai/v1")
	plannerService := planner.New(glmClient, "")
	evaluatorService := evaluator.New(glmClient, "")
	launcherService := launcher.New("codex", 30*time.Minute)
	telegramBot := notify.NewBot(telegramBotToken, telegramChatID)

	_ = plannerService
	_ = evaluatorService
	_ = launcherService
	_ = telegramBot
	_ = anthropicAPIKey
	_ = googleAIAPIKey
	_ = configPath

	cfg := dashboard.DefaultConfig()
	cfg.Host = "0.0.0.0"
	cfg.Port = 8080
	cfg.Logger = logger

	srv := dashboard.NewServer(store, cfg)

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
			logger.Error("dashboard shutdown failed", slog.Any("error", shutdownErr))
		}
	}()

	logger.Info(
		"dashboard server listening",
		slog.String("addr", srv.Addr),
		slog.String("database_path", databasePath),
		slog.String("config_path", configPath),
		slog.Bool("zai_api_key_set", zaiAPIKey != ""),
		slog.Bool("anthropic_api_key_set", anthropicAPIKey != ""),
		slog.Bool("google_ai_api_key_set", googleAIAPIKey != ""),
		slog.Bool("telegram_bot_token_set", telegramBotToken != ""),
	)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("dashboard server stopped with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}

func envInt64OrDefault(key string, fallback int64) int64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}

	return parsed
}
