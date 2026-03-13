package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/zyrakk/hivemind/internal/dashboard"
	directivepkg "github.com/zyrakk/hivemind/internal/directive"
	"github.com/zyrakk/hivemind/internal/engine"
	"github.com/zyrakk/hivemind/internal/evaluator"
	"github.com/zyrakk/hivemind/internal/launcher"
	"github.com/zyrakk/hivemind/internal/llm"
	"github.com/zyrakk/hivemind/internal/notify"
	"github.com/zyrakk/hivemind/internal/planner"
	"github.com/zyrakk/hivemind/internal/recon"
	"github.com/zyrakk/hivemind/internal/state"
	"gopkg.in/yaml.v3"
)

type runtimeConfig struct {
	GLM struct {
		APIKeyEnv string `yaml:"api_key_env"`
		Model     string `yaml:"model"`
		BaseURL   string `yaml:"base_url"`
		Timeout   string `yaml:"timeout"`
	} `yaml:"glm"`
	Engine struct {
		Primary    string `yaml:"primary"`
		Fallback   string `yaml:"fallback"`
		ClaudeCode struct {
			Binary         string                    `yaml:"binary"`
			Model          string                    `yaml:"model"`
			TimeoutMinutes int                       `yaml:"timeout_minutes"`
			PromptDir      string                    `yaml:"prompt_dir"`
			Usage          engine.UsageTrackerConfig `yaml:"usage"`
		} `yaml:"claude_code"`
	} `yaml:"engine"`
	Consultants struct {
		Claude struct {
			Enabled             bool    `yaml:"enabled"`
			APIKeyEnv           string  `yaml:"api_key_env"`
			Model               string  `yaml:"model"`
			MaxMonthlyBudgetUSD float64 `yaml:"max_monthly_budget_usd"`
			MaxCallsPerDay      int     `yaml:"max_calls_per_day"`
		} `yaml:"claude"`
		Gemini struct {
			Enabled        bool   `yaml:"enabled"`
			APIKeyEnv      string `yaml:"api_key_env"`
			Model          string `yaml:"model"`
			MaxCallsPerDay int    `yaml:"max_calls_per_day"`
		} `yaml:"gemini"`
	} `yaml:"consultants"`
	Codex struct {
		ApprovalMode    string `yaml:"approval_mode"`
		TimeoutMins     int    `yaml:"timeout_minutes"`
		ReposDir        string `yaml:"repos_dir"`
		Model           string `yaml:"model"`
		ReasoningEffort string `yaml:"reasoning_effort"`
	} `yaml:"codex"`
	Dashboard struct {
		Port int    `yaml:"port"`
		Host string `yaml:"host"`
	} `yaml:"dashboard"`
	Telegram struct {
		BotTokenEnv   string `yaml:"bot_token_env"`
		AllowedChatID int64  `yaml:"allowed_chat_id"`
	} `yaml:"telegram"`
	Database struct {
		Path string `yaml:"path"`
	} `yaml:"database"`
	Git struct {
		DefaultRemote string `yaml:"default_remote"`
		BranchPrefix  string `yaml:"branch_prefix"`
	} `yaml:"git"`
}

type plannerEvaluatorBridge struct {
	evaluator *evaluator.Evaluator
	logger    *slog.Logger
}

func (b plannerEvaluatorBridge) EvaluateWorkerOutput(ctx context.Context, session launcher.Session) (*evaluator.EvalResult, error) {
	completionResult, err := b.HandleWorkerCompletionDetailed(ctx, session.SessionID)
	if err != nil {
		return nil, err
	}
	if completionResult == nil {
		reason := "completion handler returned nil result"
		if b.logger != nil {
			b.logger.Warn("evaluation skipped: "+reason, slog.String("session_id", session.SessionID))
		}
		return nil, fmt.Errorf(reason)
	}

	return &evaluator.EvalResult{
		Action:        completionResult.Action,
		RetryCount:    completionResult.RetryCount,
		NextSessionID: completionResult.NextSessionID,
	}, nil
}

func (b plannerEvaluatorBridge) HandleWorkerCompletionDetailed(ctx context.Context, sessionID string) (*evaluator.CompletionResult, error) {
	if b.evaluator == nil {
		reason := "evaluator is not initialized"
		if b.logger != nil {
			b.logger.Warn("evaluation skipped: "+reason, slog.String("session_id", sessionID))
		}
		return nil, fmt.Errorf(reason)
	}

	return b.evaluator.HandleWorkerCompletionDetailed(ctx, sessionID)
}

func (b plannerEvaluatorBridge) SetTaskChecklists(taskID int64, checklists evaluator.TaskChecklists) {
	if b.evaluator != nil {
		b.evaluator.SetTaskChecklists(taskID, checklists)
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	configPath := envOrDefault("CONFIG_PATH", "./config.yaml")
	cfg, err := loadConfig(configPath)
	if err != nil {
		logger.Error("failed to load config", slog.Any("error", err), slog.String("config_path", configPath))
		os.Exit(1)
	}

	databasePath := envOrDefault("DATABASE_PATH", cfg.Database.Path)
	if strings.TrimSpace(databasePath) == "" {
		databasePath = "./hivemind.db"
	}

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

	recovered, recoverErr := store.RecoverFromRestart(ctx)
	if recoverErr != nil {
		logger.Error("startup recovery failed", slog.Any("error", recoverErr))
	} else if recovered > 0 {
		logger.Warn("startup recovery completed", slog.Int("recovered", recovered))
	}

	glmAPIKey := os.Getenv(defaultEnvName(cfg.GLM.APIKeyEnv, "ZAI_API_KEY"))
	glmTimeout := parseDurationOrDefault(cfg.GLM.Timeout, 60*time.Second)
	glmClient := llm.NewGLMClient(llm.GLMConfig{
		APIKey:  glmAPIKey,
		Model:   cfg.GLM.Model,
		BaseURL: cfg.GLM.BaseURL,
		Timeout: glmTimeout,
		Logger:  logger,
	})

	consultants := make([]llm.ConsultantClient, 0, 2)

	if cfg.Consultants.Claude.Enabled {
		key := os.Getenv(defaultEnvName(cfg.Consultants.Claude.APIKeyEnv, "ANTHROPIC_API_KEY"))
		if key != "" {
			budget, budgetErr := llm.NewBudgetTracker(llm.BudgetConfig{
				ConsultantName: "claude",
				MaxMonthlyUSD:  cfg.Consultants.Claude.MaxMonthlyBudgetUSD,
				MaxDailyCalls:  cfg.Consultants.Claude.MaxCallsPerDay,
				DBPath:         databasePath,
				Logger:         logger,
			})
			if budgetErr != nil {
				logger.Error("failed to initialize claude budget tracker", slog.Any("error", budgetErr))
			} else {
				consultants = append(consultants, llm.NewClaudeClient(llm.ClaudeConfig{
					APIKey:    key,
					Model:     cfg.Consultants.Claude.Model,
					PromptDir: "prompts",
					Budget:    budget,
					Logger:    logger,
				}))
			}
		}
	}

	if cfg.Consultants.Gemini.Enabled {
		key := os.Getenv(defaultEnvName(cfg.Consultants.Gemini.APIKeyEnv, "GOOGLE_AI_API_KEY"))
		if key == "" {
			key = os.Getenv("GEMINI_API_KEY")
		}
		if key != "" {
			budget, budgetErr := llm.NewBudgetTracker(llm.BudgetConfig{
				ConsultantName: "gemini",
				MaxDailyCalls:  cfg.Consultants.Gemini.MaxCallsPerDay,
				DBPath:         databasePath,
				Logger:         logger,
			})
			if budgetErr != nil {
				logger.Error("failed to initialize gemini budget tracker", slog.Any("error", budgetErr))
			} else {
				consultants = append(consultants, llm.NewGeminiClient(llm.GeminiConfig{
					APIKey:    key,
					Model:     cfg.Consultants.Gemini.Model,
					PromptDir: "prompts",
					Budget:    budget,
					Logger:    logger,
				}))
			}
		}
	}

	launcherService := launcher.NewWithStore(store, launcher.LauncherConfig{
		CodexBinary:          envOrDefault("CODEX_BINARY", "codex"),
		ApprovalMode:         cfg.Codex.ApprovalMode,
		TimeoutMinutes:       cfg.Codex.TimeoutMins,
		MaxConcurrentWorkers: 5,
		ReposDir:             cfg.Codex.ReposDir,
		Model:                cfg.Codex.Model,
		ReasoningEffort:      cfg.Codex.ReasoningEffort,
		WorkDir:              ".",
		GitRemote:            cfg.Git.DefaultRemote,
		BranchPrefix:         cfg.Git.BranchPrefix,
		UseTmux:              true,
		Logger:               logger,
	})

	reconRunner := recon.NewRunner(logger)

	var ccEngine *engine.ClaudeCodeEngine
	engines := map[string]engine.Engine{}
	if glmClient != nil {
		engines["glm"] = engine.NewGLMEngine(glmClient, logger)
	}
	if cfg.Engine.Primary == "claude-code" || cfg.Engine.Fallback == "claude-code" {
		ccEngine = engine.NewClaudeCodeEngine(engine.ClaudeCodeConfig{
			Binary:         defaultString(cfg.Engine.ClaudeCode.Binary, "claude"),
			Model:          cfg.Engine.ClaudeCode.Model,
			TimeoutMinutes: cfg.Engine.ClaudeCode.TimeoutMinutes,
			PromptDir:      defaultString(cfg.Engine.ClaudeCode.PromptDir, "prompts"),
			Usage:          cfg.Engine.ClaudeCode.Usage,
		}, logger)
		engines["claude-code"] = ccEngine
	}

	engineMgr := engine.NewManager(engine.ManagerConfig{
		PrimaryEngine:  defaultString(cfg.Engine.Primary, "glm"),
		FallbackEngine: defaultString(cfg.Engine.Fallback, "none"),
	}, engines, logger)

	plannerService := planner.New(glmClient, consultants, launcherService, store, "prompts", logger)
	evaluatorService := evaluator.New(glmClient, consultants, launcherService, store, logger)
	plannerService.SetEngine(engineMgr)
	plannerService.SetRecon(reconRunner)
	evaluatorService.SetEngine(engineMgr)
	evaluatorService.SetRecon(reconRunner)
	plannerService.SetEvaluator(plannerEvaluatorBridge{
		evaluator: evaluatorService,
		logger:    logger,
	})

	logger.Info("engine configured",
		slog.String("primary", defaultString(cfg.Engine.Primary, "glm")),
		slog.String("fallback", defaultString(cfg.Engine.Fallback, "none")),
		slog.String("active", engineMgr.ActiveEngine(ctx)),
	)

	telegramTokenEnv := defaultEnvName(cfg.Telegram.BotTokenEnv, "TELEGRAM_BOT_TOKEN")
	telegramToken := strings.TrimSpace(os.Getenv(telegramTokenEnv))
	telegramChatID := cfg.Telegram.AllowedChatID
	if rawChatID := strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")); rawChatID != "" {
		parsedChatID, parseErr := strconv.ParseInt(rawChatID, 10, 64)
		if parseErr != nil {
			logger.Warn("invalid TELEGRAM_CHAT_ID, using config value", slog.String("raw_chat_id", rawChatID), slog.Any("error", parseErr))
		} else {
			telegramChatID = parsedChatID
		}
	}

	var telegramNotifier *notify.TelegramBot
	if telegramToken != "" && telegramChatID != 0 {
		telegramNotifier = notify.NewTelegramBot(telegramToken, telegramChatID, plannerService, store, logger)
		telegramNotifier.SetWorkerController(launcherService)
		telegramNotifier.SetConsultants(consultants)
		if startErr := telegramNotifier.Start(ctx); startErr != nil {
			logger.Error("failed to start telegram notifier", slog.Any("error", startErr))
			telegramNotifier = nil
		} else {
			engineMgr.SetSwitchCallback(func(from, to, reason string) {
				telegramNotifier.NotifyEngineSwitch(from, to, reason)
			})
			if ccEngine != nil && ccEngine.UsageTracker() != nil {
				ccEngine.UsageTracker().SetAlertCallback(func(msg string) {
					telegramNotifier.QueueMessage(msg)
				})
			}
			if ccEngine != nil && ccEngine.UsageTracker() != nil {
				telegramNotifier.SetUsageTracker(ccEngine.UsageTracker())
			}
			plannerService.SetNotifier(telegramNotifier)
			evaluatorService.SetNotifier(telegramNotifier)
			launcherService.SetNotifier(telegramNotifier)

			defer func() {
				if stopErr := telegramNotifier.Stop(); stopErr != nil && !errors.Is(stopErr, notify.ErrBotNotStarted) {
					logger.Error("failed to stop telegram notifier", slog.Any("error", stopErr))
				}
			}()
		}
	} else {
		logger.Info(
			"telegram notifier disabled",
			slog.Bool("token_configured", telegramToken != ""),
			slog.Int64("allowed_chat_id", telegramChatID),
		)
	}

	dcfg := dashboard.DefaultConfig()
	dcfg.Host = defaultString(cfg.Dashboard.Host, "0.0.0.0")
	if cfg.Dashboard.Port > 0 {
		dcfg.Port = cfg.Dashboard.Port
	}
	dcfg.Logger = logger
	dashboardServer := dashboard.NewServer(store, dcfg)

	serverErrCh := make(chan error, 1)
	go func() {
		if serveErr := dashboardServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErrCh <- serveErr
		}
	}()

	logger.Info("hivemind orchestrator ready",
		slog.String("dashboard_addr", dashboardServer.Addr),
		slog.String("config_path", configPath),
		slog.Bool("glm_configured", glmAPIKey != ""),
		slog.Int("consultants_enabled", len(consultants)),
	)

	defaultProjectRef := envOrDefault("PROJECT_ID", "flux")
	promptProjectRef := ""
	stdinIsTTY, statErr := isStdinTTY()
	if statErr != nil {
		logger.Warn("failed to inspect stdin; disabling interactive cli", slog.Any("error", statErr))
	}
	if !stdinIsTTY {
		logger.Info("stdin is not a tty; interactive cli disabled")
		select {
		case <-ctx.Done():
		case serveErr := <-serverErrCh:
			if serveErr != nil {
				logger.Error("dashboard server failed", slog.Any("error", serveErr))
			}
		}
		shutdownDashboard(logger, dashboardServer)
		return
	}

	scanner := bufio.NewScanner(os.Stdin)

	for {
		select {
		case <-ctx.Done():
			shutdownDashboard(logger, dashboardServer)
			return
		case serveErr := <-serverErrCh:
			if serveErr != nil {
				logger.Error("dashboard server failed", slog.Any("error", serveErr))
			}
			return
		default:
		}

		promptLabel := fmt.Sprintf("default: %s", defaultProjectRef)
		if promptProjectRef != "" {
			promptLabel = promptProjectRef
		}
		fmt.Printf("\nDirective (%s) > ", promptLabel)
		if !scanner.Scan() {
			shutdownDashboard(logger, dashboardServer)
			return
		}

		rawDirective := strings.TrimSpace(scanner.Text())
		directive := rawDirective
		projectRef := defaultProjectRef
		promptProjectRef = ""

		if parsedDirective, parsedProjectRef, hasProjectRouting := directivepkg.ParseRouting(rawDirective); hasProjectRouting {
			parsedProjectRef = strings.TrimSpace(parsedProjectRef)
			if parsedProjectRef == "" {
				fmt.Printf("proyecto '%s' no encontrado\n", parsedProjectRef)
				continue
			}

			project, resolveErr := store.GetProjectByReference(ctx, parsedProjectRef)
			if resolveErr != nil {
				if errors.Is(resolveErr, state.ErrNotFound) {
					fmt.Printf("proyecto '%s' no encontrado\n", parsedProjectRef)
				} else {
					logger.Error("resolve project failed", slog.Any("error", resolveErr), slog.String("project_ref", parsedProjectRef))
				}
				continue
			}

			projectRef = strings.TrimSpace(project.Name)
			if projectRef == "" {
				projectRef = parsedProjectRef
			}
			promptProjectRef = projectRef
			directive = parsedDirective
		}

		directive = strings.TrimSpace(directive)
		if directive == "" {
			continue
		}
		if strings.EqualFold(directive, "exit") || strings.EqualFold(directive, "quit") {
			shutdownDashboard(logger, dashboardServer)
			return
		}

		planResult, planErr := plannerService.CreatePlan(ctx, directive, projectRef)
		if planErr != nil {
			logger.Error("create plan failed", slog.Any("error", planErr))
			continue
		}

		printJSON("Plan", planResult.Plan)
		if planResult.NeedsInput {
			fmt.Println("Plan requires user input before execution:")
			for _, q := range planResult.Plan.Questions {
				fmt.Printf("- %s\n", q)
			}
			continue
		}

		fmt.Print("Execute plan? (y/n/edit): ")
		if !scanner.Scan() {
			shutdownDashboard(logger, dashboardServer)
			return
		}

		choice := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if choice == "edit" {
			fmt.Print("Provide additional guidance: ")
			if !scanner.Scan() {
				shutdownDashboard(logger, dashboardServer)
				return
			}
			additional := strings.TrimSpace(scanner.Text())
			if additional != "" {
				directive = directive + "\n\nAdditional guidance:\n" + additional
			}
			planResult, planErr = plannerService.CreatePlan(ctx, directive, projectRef)
			if planErr != nil {
				logger.Error("create edited plan failed", slog.Any("error", planErr))
				continue
			}
			printJSON("Edited Plan", planResult.Plan)
			fmt.Print("Execute edited plan? (y/n): ")
			if !scanner.Scan() {
				shutdownDashboard(logger, dashboardServer)
				return
			}
			choice = strings.ToLower(strings.TrimSpace(scanner.Text()))
		}

		if choice != "y" && choice != "yes" {
			fmt.Println("Plan skipped.")
			continue
		}

		if execErr := plannerService.ExecutePlan(ctx, planResult.PlanID); execErr != nil {
			logger.Error("execute plan failed", slog.Any("error", execErr), slog.String("plan_id", planResult.PlanID))
			continue
		}

		fmt.Println("Plan execution finished.")
	}
}

func loadConfig(path string) (runtimeConfig, error) {
	cfg := defaultRuntimeConfig()
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return runtimeConfig{}, err
	}

	if err := yaml.Unmarshal(payload, &cfg); err != nil {
		return runtimeConfig{}, fmt.Errorf("parse yaml config: %w", err)
	}

	return cfg, nil
}

func defaultRuntimeConfig() runtimeConfig {
	cfg := runtimeConfig{}
	cfg.GLM.APIKeyEnv = "ZAI_API_KEY"
	cfg.GLM.Model = "glm-4.7"
	cfg.GLM.BaseURL = "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions"
	cfg.GLM.Timeout = "60s"

	cfg.Engine.Primary = "glm"
	cfg.Engine.Fallback = "none"
	cfg.Engine.ClaudeCode.Binary = "claude"
	cfg.Engine.ClaudeCode.TimeoutMinutes = 10
	cfg.Engine.ClaudeCode.PromptDir = "prompts"
	cfg.Engine.ClaudeCode.Usage.SoftLimitDaily = 12
	cfg.Engine.ClaudeCode.Usage.HardLimitDaily = 18
	cfg.Engine.ClaudeCode.Usage.SoftLimitWeekly = 70
	cfg.Engine.ClaudeCode.Usage.HardLimitWeekly = 100

	cfg.Codex.ApprovalMode = "full-auto"
	cfg.Codex.TimeoutMins = 30
	cfg.Codex.ReposDir = "/home/stefan/Github_Repos"
	cfg.Codex.Model = ""
	cfg.Codex.ReasoningEffort = "medium"

	cfg.Dashboard.Host = "0.0.0.0"
	cfg.Dashboard.Port = 8080
	cfg.Telegram.BotTokenEnv = "TELEGRAM_BOT_TOKEN"
	cfg.Telegram.AllowedChatID = 0

	cfg.Database.Path = "./hivemind.db"
	cfg.Git.DefaultRemote = "origin"
	cfg.Git.BranchPrefix = "feature/"

	cfg.Consultants.Claude.APIKeyEnv = "ANTHROPIC_API_KEY"
	cfg.Consultants.Claude.Model = "claude-sonnet-4-5-20250929"
	cfg.Consultants.Gemini.APIKeyEnv = "GOOGLE_AI_API_KEY"
	cfg.Consultants.Gemini.Model = "gemini-2.5-flash"

	return cfg
}

func parseDurationOrDefault(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func defaultEnvName(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func isStdinTTY() (bool, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false, err
	}

	if (info.Mode() & os.ModeCharDevice) == 0 {
		return false, nil
	}

	// ModeCharDevice is also true for /dev/null. Confirm terminal capability with ioctl.
	var termios syscall.Termios
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		os.Stdin.Fd(),
		uintptr(syscall.TCGETS),
		uintptr(unsafe.Pointer(&termios)),
		0,
		0,
		0,
	)
	if errno == 0 {
		return true, nil
	}
	if errno == syscall.ENOTTY {
		return false, nil
	}
	return false, fmt.Errorf("stdin ioctl tcgets: %w", errno)
}

func shutdownDashboard(logger *slog.Logger, srv *http.Server) {
	if srv == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("dashboard shutdown failed", slog.Any("error", err))
	}
}

func printJSON(label string, payload any) {
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Printf("%s: <failed to encode json: %v>\n", label, err)
		return
	}
	fmt.Printf("%s:\n%s\n", label, string(encoded))
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
