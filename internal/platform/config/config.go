// Package config loads and validates process configuration from the
// environment. Configuration is read once at startup and treated as immutable
// for the lifetime of the process.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment names a deployment target. Behaviour that differs between
// environments should branch on this rather than on ad-hoc environment
// variable checks scattered through the code.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvStaging     Environment = "staging"
	EnvProduction  Environment = "production"
)

// Config holds everything the process needs to start. Every field is populated
// by Load; there are no optional-at-runtime lookups elsewhere in the codebase.
type Config struct {
	// Env is the deployment environment this process believes it is running in.
	Env Environment

	// Port is the TCP port the HTTP server listens on. Cloud Run injects PORT
	// into the container and expects the server to honour it.
	Port int

	// LogLevel is the minimum severity written to the structured logger.
	LogLevel slog.Level

	// ShutdownTimeout bounds how long in-flight requests may take to drain
	// after a termination signal before the process exits regardless.
	ShutdownTimeout time.Duration
}

// Load reads configuration from the process environment.
//
// It reports every problem it finds rather than stopping at the first, so a
// misconfigured deployment surfaces all of its errors in a single startup
// failure instead of one per restart.
func Load() (Config, error) {
	var (
		cfg  Config
		errs []error
	)

	env := Environment(getenv("APP_ENV", ""))
	switch env {
	case EnvDevelopment, EnvStaging, EnvProduction:
		cfg.Env = env
	case "":
		errs = append(errs, errors.New("APP_ENV is required"))
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be one of development, staging, production: got %q", env))
	}

	port, err := strconv.Atoi(getenv("PORT", "8080"))
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("PORT must be an integer: %w", err))
	case port < 1 || port > 65535:
		errs = append(errs, fmt.Errorf("PORT must be between 1 and 65535: got %d", port))
	default:
		cfg.Port = port
	}

	level, err := parseLevel(getenv("LOG_LEVEL", "info"))
	if err != nil {
		errs = append(errs, err)
	} else {
		cfg.LogLevel = level
	}

	timeout, err := time.ParseDuration(getenv("SHUTDOWN_TIMEOUT", "15s"))
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT must be a duration such as 15s: %w", err))
	case timeout <= 0:
		errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT must be positive: got %s", timeout))
	default:
		cfg.ShutdownTimeout = timeout
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("load config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// Addr returns the listen address for the HTTP server. The host is left empty
// so the server binds every interface, which is what a container needs.
func (c Config) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
}

// getenv returns the value of key, or fallback when the variable is unset or
// empty. Treating empty as unset avoids a class of deployment bug where a
// templated manifest sets a variable to the empty string.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error: got %q", s)
	}
}
