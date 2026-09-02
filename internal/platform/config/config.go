// Package config loads and validates process configuration from the
// environment. Configuration is read once at startup and treated as immutable
// for the lifetime of the process.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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

	// PublicBaseURL is the externally reachable origin of this service, used
	// to tell messaging providers where to deliver webhooks. When empty, the
	// service serves its webhook endpoints but does not register them.
	PublicBaseURL string

	// BusinessName is what the assistant calls the business when it introduces
	// itself. Empty makes it speak generically rather than fail.
	BusinessName string

	Telegram Telegram
	Altegio  Altegio
	AI       AI
	Storage  Storage
}

// StorageBackend names where application state is kept.
type StorageBackend string

const (
	// StorageMemory keeps state in the process. It is lost on restart and not
	// shared between instances, so it is for local development and tests.
	StorageMemory StorageBackend = "memory"

	// StorageFirestore keeps state in Google Cloud Firestore.
	StorageFirestore StorageBackend = "firestore"
)

// Storage selects where conversations, customers and deduplication live.
type Storage struct {
	Backend StorageBackend

	// ProjectID is the Google Cloud project holding the Firestore database. The
	// deployment supplies it to Cloud Run; GOOGLE_CLOUD_PROJECT is accepted as
	// a conventional fallback in other Google-hosted environments.
	ProjectID string
}

// AI holds the language model settings. The assistant falls back to a fixed
// reply, rather than failing, when no key is set.
type AI struct {
	APIKey string

	// Model is the model identifier. It is configurable because model names
	// change faster than deployments do, so correcting one is a configuration
	// change rather than a release. Empty uses the adapter's default.
	Model string

	// BaseURL overrides the API host, for exercising the assistant locally
	// against a stub and for routing through a proxy. Empty means the real API.
	BaseURL string
}

// Enabled reports whether a language model is configured.
func (a AI) Enabled() bool { return a.APIKey != "" }

// Altegio holds the credentials and settings for the scheduling system. It is
// disabled when the partner token is absent.
type Altegio struct {
	// PartnerToken identifies the integration and reaches the public booking
	// endpoints on its own.
	PartnerToken string

	// UserToken authorises access to the business's own data. Reading the
	// public catalogue works without it; anything about real appointments does
	// not.
	UserToken string

	// CompanyID is the Altegio location this deployment serves.
	CompanyID string

	// Location is the business's timezone. A customer saying "Friday at three"
	// means three o'clock where the salon is, and Altegio reports some times
	// without an offset, so this cannot be guessed.
	Location *time.Location

	// Currency labels the prices Altegio returns, which carry no currency of
	// their own.
	Currency string

	// BaseURL overrides the API host, for exercising the assistant locally
	// against a stub. Empty means the real API.
	BaseURL string
}

// Enabled reports whether the scheduling system is configured.
func (a Altegio) Enabled() bool { return a.PartnerToken != "" }

// Telegram holds the credentials for the Telegram channel. The channel is
// disabled, and its endpoint not served, when the bot token is absent.
type Telegram struct {
	BotToken string

	// WebhookSecret is echoed by Telegram in a header on every delivery. It is
	// the only thing distinguishing a genuine delivery from anyone on the
	// internet posting to the endpoint, so it is required whenever the channel
	// is enabled.
	WebhookSecret string

	// APIBaseURL overrides the Telegram API host. It exists so the channel can
	// be exercised locally against a stub, and so traffic can be routed through
	// an egress proxy. Empty means the real API.
	APIBaseURL string
}

// Enabled reports whether the Telegram channel is configured.
func (t Telegram) Enabled() bool { return t.BotToken != "" }

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

	cfg.PublicBaseURL = strings.TrimSuffix(getenv("PUBLIC_BASE_URL", ""), "/")
	if err := validatePublicBaseURL(cfg.PublicBaseURL); err != nil {
		errs = append(errs, err)
	}

	cfg.Telegram = Telegram{
		BotToken:      getenv("TELEGRAM_BOT_TOKEN", ""),
		WebhookSecret: getenv("TELEGRAM_WEBHOOK_SECRET", ""),
		APIBaseURL:    strings.TrimSuffix(getenv("TELEGRAM_API_BASE_URL", ""), "/"),
	}
	errs = append(errs, cfg.Telegram.validate()...)

	altegio, altegioErrs := loadAltegio()
	cfg.Altegio = altegio
	errs = append(errs, altegioErrs...)

	cfg.BusinessName = getenv("BUSINESS_NAME", "")
	cfg.AI = AI{
		APIKey:  getenv("OPENAI_API_KEY", ""),
		Model:   getenv("OPENAI_MODEL", ""),
		BaseURL: strings.TrimSuffix(getenv("OPENAI_BASE_URL", ""), "/"),
	}
	if !cfg.AI.Enabled() && cfg.AI.Model != "" {
		errs = append(errs, errors.New("OPENAI_MODEL is set but OPENAI_API_KEY is not"))
	}

	storage, storageErrs := loadStorage(cfg.Env)
	cfg.Storage = storage
	errs = append(errs, storageErrs...)

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("load config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// validate reports every problem with the Telegram settings.
//
// An enabled channel without a webhook secret is refused rather than defaulted:
// the alternative is an unauthenticated public endpoint that anyone can post
// customer messages to.
func (t Telegram) validate() []error {
	if !t.Enabled() {
		// A configured secret with no token is a half-finished deployment, and
		// silently ignoring it would hide the mistake.
		if t.WebhookSecret != "" {
			return []error{errors.New("TELEGRAM_WEBHOOK_SECRET is set but TELEGRAM_BOT_TOKEN is not")}
		}
		return nil
	}

	var errs []error
	switch {
	case t.WebhookSecret == "":
		errs = append(errs, errors.New("TELEGRAM_WEBHOOK_SECRET is required when TELEGRAM_BOT_TOKEN is set"))
	case len(t.WebhookSecret) > 256:
		errs = append(errs, fmt.Errorf("TELEGRAM_WEBHOOK_SECRET must be at most 256 characters: got %d", len(t.WebhookSecret)))
	case !isTelegramSecret(t.WebhookSecret):
		// Telegram rejects setWebhook outright for a secret outside this set,
		// so catching it here turns a confusing API error into a clear one.
		errs = append(errs, errors.New("TELEGRAM_WEBHOOK_SECRET may only contain A-Z, a-z, 0-9, underscore and hyphen"))
	}
	return errs
}

func isTelegramSecret(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// loadStorage decides where state is kept.
//
// Production defaults to Firestore and refuses to fall back to memory: an
// in-process store there would lose every conversation on each deploy and
// deduplicate nothing across instances, which is exactly the failure that
// produces duplicate appointments.
func loadStorage(env Environment) (Storage, []error) {
	defaultBackend := StorageMemory
	if env == EnvProduction {
		defaultBackend = StorageFirestore
	}

	cfg := Storage{
		Backend: StorageBackend(getenv("STORAGE_BACKEND", string(defaultBackend))),
		// GOOGLE_CLOUD_PROJECT is a conventional fallback used by several
		// Google-hosted environments and local tools.
		ProjectID: getenv("GCP_PROJECT_ID", getenv("GOOGLE_CLOUD_PROJECT", "")),
	}

	var errs []error
	switch cfg.Backend {
	case StorageMemory:
		if env == EnvProduction {
			errs = append(errs, errors.New(
				"STORAGE_BACKEND=memory is refused in production: state would be lost on "+
					"every deploy and not shared between instances"))
		}
	case StorageFirestore:
		if cfg.ProjectID == "" {
			errs = append(errs, errors.New("GCP_PROJECT_ID is required when STORAGE_BACKEND=firestore"))
		}
	default:
		errs = append(errs, fmt.Errorf("STORAGE_BACKEND must be memory or firestore: got %q", cfg.Backend))
	}

	return cfg, errs
}

// loadAltegio reads the scheduling system settings and reports every problem.
func loadAltegio() (Altegio, []error) {
	cfg := Altegio{
		PartnerToken: getenv("ALTEGIO_PARTNER_TOKEN", ""),
		UserToken:    getenv("ALTEGIO_USER_TOKEN", ""),
		CompanyID:    getenv("ALTEGIO_COMPANY_ID", ""),
		Currency:     getenv("ALTEGIO_CURRENCY", ""),
		BaseURL:      strings.TrimSuffix(getenv("ALTEGIO_BASE_URL", ""), "/"),
		Location:     time.UTC,
	}

	var errs []error

	// The timezone is validated even when the channel is off, because a name
	// that does not load is a deployment mistake worth reporting either way.
	if name := getenv("ALTEGIO_TIMEZONE", ""); name != "" {
		loc, err := time.LoadLocation(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("ALTEGIO_TIMEZONE is not a known timezone: %w", err))
		} else {
			cfg.Location = loc
		}
	}

	if !cfg.Enabled() {
		if cfg.CompanyID != "" || cfg.UserToken != "" {
			errs = append(errs, errors.New("ALTEGIO_COMPANY_ID or ALTEGIO_USER_TOKEN is set but ALTEGIO_PARTNER_TOKEN is not"))
		}
		return cfg, errs
	}

	if cfg.CompanyID == "" {
		errs = append(errs, errors.New("ALTEGIO_COMPANY_ID is required when ALTEGIO_PARTNER_TOKEN is set"))
	}

	// Reading the catalogue works with the partner token alone, so a missing
	// user token is a warning rather than a failure. It is reported at startup
	// instead, where it can name what will not work.
	return cfg, errs
}

// validatePublicBaseURL requires an absolute HTTPS origin. Every provider this
// system targets refuses to deliver webhooks over plain HTTP.
func validatePublicBaseURL(raw string) error {
	if raw == "" {
		return nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("PUBLIC_BASE_URL is not a valid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("PUBLIC_BASE_URL must use https: got %q", raw)
	}
	if parsed.Host == "" {
		return fmt.Errorf("PUBLIC_BASE_URL has no host: got %q", raw)
	}
	return nil
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
