package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"pdf-forge/internal/converters"
	"pdf-forge/internal/handlers"
	"pdf-forge/internal/middleware"
)

const (
	Version = "2.0.0"
)

func main() {
	// Configure structured logger
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))

	logger.Info("Starting PDF Forge", "version", Version)

	// Configuration from environment
	config := loadConfig()

	// Initialize Chrome converter
	converter, err := converters.NewChromeConverter(config.MaxWorkers)
	if err != nil {
		logger.Error("Failed to initialize Chrome converter", "error", err)
		os.Exit(1)
	}
	defer converter.Close()
	logger.Info("Chrome converter initialized", "workers", config.MaxWorkers)

	// Initialize PDF processor (for security, watermarks, etc.)
	processor, err := converters.NewPDFProcessor()
	if err != nil {
		logger.Warn("PDF processor initialization failed - some features may be unavailable", "error", err)
		processor = nil
	} else {
		defer processor.Close()
		logger.Info("PDF processor initialized")
	}

	// Create handlers
	h := handlers.NewHandler(converter, processor, logger, Version)

	// Create extended handler for advanced features
	extHandler, err := handlers.NewExtendedHandler(h)
	if err != nil {
		logger.Warn("Extended handler initialization failed - some features unavailable", "error", err)
	} else {
		defer extHandler.Close()
		logger.Info("Extended handler initialized (templates, manipulation, async)")
	}

	// Setup router
	mux := http.NewServeMux()

	// Health and metrics endpoints (no auth)
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("GET /healthz", h.Health)
	mux.HandleFunc("GET /metrics", h.Metrics)

	// Main conversion endpoint (unified)
	mux.HandleFunc("POST /convert", h.Convert)

	// Legacy/specific endpoints
	mux.HandleFunc("POST /render", h.ConvertHTML)
	mux.HandleFunc("POST /html", h.ConvertHTML)
	mux.HandleFunc("POST /url", h.ConvertURL)
	mux.HandleFunc("POST /image", h.ConvertImage)
	mux.HandleFunc("POST /images", h.ConvertImage)
	mux.HandleFunc("POST /markdown", h.ConvertMarkdown)
	mux.HandleFunc("POST /merge", h.MergePDFs)

	// Extended features (if available)
	if extHandler != nil {
		mux.HandleFunc("POST /template", extHandler.Template)
		mux.HandleFunc("POST /manipulate", extHandler.Manipulate)
		mux.HandleFunc("POST /async", extHandler.Async)
		mux.HandleFunc("POST /batch", extHandler.Batch)
		mux.HandleFunc("POST /table", extHandler.TableToPDF)
	}

	// Build middleware chain
	var chain http.Handler = mux

	// Apply middleware in reverse order (outermost first)
	chain = middleware.Recover(logger)(chain)
	chain = middleware.Logger(logger)(chain)
	chain = middleware.RequestID(chain)
	chain = middleware.MaxBodySize(config.MaxBodySize)(chain)

	// Rate limiting (if enabled)
	if config.RateLimit > 0 {
		limiter := middleware.NewRateLimiter(config.RateLimit, time.Minute)
		chain = limiter.Limit(chain)
		logger.Info("Rate limiting enabled", "limit", config.RateLimit, "window", "1m")
	}

	// CORS (if enabled)
	if len(config.CORSOrigins) > 0 {
		chain = middleware.CORS(config.CORSOrigins)(chain)
		logger.Info("CORS enabled", "origins", config.CORSOrigins)
	}

	// API key auth (if enabled)
	if config.APIKey != "" {
		chain = middleware.APIKeyAuth(config.APIKey)(chain)
		logger.Info("API key authentication enabled")
	}

	// Create server with generous timeouts for large files
	srv := &http.Server{
		Addr:         config.Address,
		Handler:      chain,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		logger.Info("Server starting", "address", config.Address)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
	}

	logger.Info("Server stopped")
}

type Config struct {
	Address      string
	APIKey       string
	MaxWorkers   int
	MaxBodySize  int64
	RateLimit    int
	CORSOrigins  []string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

func loadConfig() Config {
	return Config{
		Address:      getEnv("ADDRESS", ":8080"),
		APIKey:       os.Getenv("API_KEY"),
		MaxWorkers:   getEnvInt("MAX_WORKERS", 4),
		MaxBodySize:  getEnvInt64("MAX_BODY_SIZE", 500*1024*1024), // 500MB default
		RateLimit:    getEnvInt("RATE_LIMIT", 0),                  // 0 = disabled
		CORSOrigins:  getEnvSlice("CORS_ORIGINS", nil),
		ReadTimeout:  time.Duration(getEnvInt("READ_TIMEOUT", 300)) * time.Second,
		WriteTimeout: time.Duration(getEnvInt("WRITE_TIMEOUT", 300)) * time.Second,
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func getEnvInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return def
}

func getEnvSlice(key string, def []string) []string {
	if v := os.Getenv(key); v != "" {
		var result []string
		for _, s := range splitComma(v) {
			if s != "" {
				result = append(result, s)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return def
}

func splitComma(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func init() {
	// Print banner
	banner := `
    ____  ____  ______   ______                    
   / __ \/ __ \/ ____/  / ____/___  _________ ____ 
  / /_/ / / / / /_     / /_  / __ \/ ___/ __ '/ _ \
 / ____/ /_/ / __/    / __/ / /_/ / /  / /_/ /  __/
/_/   /_____/_/      /_/    \____/_/   \__, /\___/ 
                                      /____/       
`
	fmt.Println(banner)
	fmt.Printf("PDF Forge v%s - High-Performance PDF Conversion Microservice\n\n", Version)
}
