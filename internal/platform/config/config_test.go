package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "development")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Env != EnvDevelopment {
		t.Errorf("Env = %q, want %q", cfg.Env, EnvDevelopment)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelInfo)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 15s", cfg.ShutdownTimeout)
	}
	if got, want := cfg.Addr(), ":8080"; got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("PORT", "9090")
	t.Setenv("LOG_LEVEL", "WARN")
	t.Setenv("SHUTDOWN_TIMEOUT", "30s")
	// Production stores state in Firestore, which needs a project.
	t.Setenv("GCP_PROJECT_ID", "my-project")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Env != EnvProduction {
		t.Errorf("Env = %q, want %q", cfg.Env, EnvProduction)
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.LogLevel != slog.LevelWarn {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelWarn)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 30s", cfg.ShutdownTimeout)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		want string
	}{
		"missing APP_ENV": {
			env:  map[string]string{},
			want: "APP_ENV is required",
		},
		"unknown APP_ENV": {
			env:  map[string]string{"APP_ENV": "prod"},
			want: "APP_ENV must be one of",
		},
		"non-numeric PORT": {
			env:  map[string]string{"APP_ENV": "development", "PORT": "http"},
			want: "PORT must be an integer",
		},
		"out of range PORT": {
			env:  map[string]string{"APP_ENV": "development", "PORT": "70000"},
			want: "PORT must be between 1 and 65535",
		},
		"unknown LOG_LEVEL": {
			env:  map[string]string{"APP_ENV": "development", "LOG_LEVEL": "verbose"},
			want: "LOG_LEVEL must be one of",
		},
		"negative SHUTDOWN_TIMEOUT": {
			env:  map[string]string{"APP_ENV": "development", "SHUTDOWN_TIMEOUT": "-1s"},
			want: "SHUTDOWN_TIMEOUT must be positive",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			// t.Setenv cannot unset, so clear the variables this case omits.
			for _, k := range []string{"APP_ENV", "PORT", "LOG_LEVEL", "SHUTDOWN_TIMEOUT"} {
				if _, ok := tt.env[k]; !ok {
					t.Setenv(k, "")
				}
			}

			_, err := Load()
			if err == nil {
				t.Fatal("Load() succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestTelegramIsOptional(t *testing.T) {
	t.Setenv("APP_ENV", "development")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Telegram.Enabled() {
		t.Error("Telegram reports enabled with no bot token")
	}
}

func TestTelegramConfiguration(t *testing.T) {
	tests := map[string]struct {
		token   string
		secret  string
		wantErr string
	}{
		"a token and a secret": {
			token:  "123456:AAHtestTokenValue",
			secret: "a-valid_SECRET-123",
		},
		// An enabled channel with no secret would expose an unauthenticated
		// public endpoint, so it is refused rather than defaulted.
		"a token with no secret": {
			token:   "123456:AAHtestTokenValue",
			wantErr: "TELEGRAM_WEBHOOK_SECRET is required",
		},
		"a secret with no token": {
			secret:  "a-valid_SECRET-123",
			wantErr: "TELEGRAM_BOT_TOKEN is not",
		},
		// Telegram rejects setWebhook outright for these, so catching it at
		// startup turns a confusing API error into a clear one.
		"a secret with illegal characters": {
			token:   "123456:AAHtestTokenValue",
			secret:  "has spaces and: colons",
			wantErr: "may only contain",
		},
		"a secret that is too long": {
			token:   "123456:AAHtestTokenValue",
			secret:  strings.Repeat("a", 257),
			wantErr: "at most 256 characters",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_ENV", "development")
			t.Setenv("TELEGRAM_BOT_TOKEN", tt.token)
			t.Setenv("TELEGRAM_WEBHOOK_SECRET", tt.secret)

			cfg, err := Load()

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load() returned error: %v", err)
				}
				if !cfg.Telegram.Enabled() {
					t.Error("Telegram reports disabled despite a bot token")
				}
				return
			}

			if err == nil {
				t.Fatal("Load() accepted an invalid Telegram configuration")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestPublicBaseURL(t *testing.T) {
	tests := map[string]struct {
		raw     string
		want    string
		wantErr string
	}{
		"unset":            {raw: "", want: ""},
		"https origin":     {raw: "https://booking.example.run.app", want: "https://booking.example.run.app"},
		"trailing slash":   {raw: "https://booking.example.run.app/", want: "https://booking.example.run.app"},
		"plain http":       {raw: "http://booking.example.com", wantErr: "must use https"},
		"no host":          {raw: "https://", wantErr: "has no host"},
		"not a url at all": {raw: "://nope", wantErr: "not a valid URL"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_ENV", "development")
			t.Setenv("PUBLIC_BASE_URL", tt.raw)

			cfg, err := Load()

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load() returned error: %v", err)
				}
				if cfg.PublicBaseURL != tt.want {
					t.Errorf("PublicBaseURL = %q, want %q", cfg.PublicBaseURL, tt.want)
				}
				return
			}

			if err == nil {
				t.Fatal("Load() accepted an invalid PUBLIC_BASE_URL")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestStorageDefaults(t *testing.T) {
	tests := map[string]struct {
		env     string
		want    StorageBackend
		project string
	}{
		// Local development keeps state in the process, so a restart is a clean
		// slate and nothing external is needed to run the service.
		"development defaults to memory": {env: "development", want: StorageMemory},

		// Production keeps it in Firestore, because an in-process store would
		// lose every conversation on each deploy and deduplicate nothing across
		// instances.
		"production defaults to firestore": {env: "production", want: StorageFirestore, project: "my-project"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_ENV", tt.env)
			t.Setenv("GCP_PROJECT_ID", tt.project)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}
			if cfg.Storage.Backend != tt.want {
				t.Errorf("backend = %q, want %q", cfg.Storage.Backend, tt.want)
			}
		})
	}
}

// TestProductionRefusesInProcessStorage guards the failure that produces
// duplicate appointments: deduplication that is not shared between instances.
func TestProductionRefusesInProcessStorage(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("STORAGE_BACKEND", "memory")
	t.Setenv("GCP_PROJECT_ID", "my-project")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() accepted in-process storage in production")
	}
	if !strings.Contains(err.Error(), "refused in production") {
		t.Errorf("error = %q, want it to explain the refusal", err)
	}
}

func TestFirestoreNeedsAProject(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("STORAGE_BACKEND", "firestore")
	t.Setenv("GCP_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() accepted firestore with no project")
	}
	if !strings.Contains(err.Error(), "GCP_PROJECT_ID is required") {
		t.Errorf("error = %q", err)
	}
}

func TestGoogleCloudProjectFallback(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("STORAGE_BACKEND", "firestore")
	t.Setenv("GCP_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "injected-by-cloud-run")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Storage.ProjectID != "injected-by-cloud-run" {
		t.Errorf("project = %q", cfg.Storage.ProjectID)
	}
}

func TestUnknownStorageBackendIsRefused(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("STORAGE_BACKEND", "postgres")

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted an unknown storage backend")
	}
}

func TestReminderDefaults(t *testing.T) {
	tests := map[string]struct {
		env  string
		want ReminderBackend
	}{
		"development uses local timers":          {env: "development", want: RemindersMemory},
		"production waits for deployment wiring": {env: "production", want: RemindersDisabled},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_ENV", tt.env)
			t.Setenv("GCP_PROJECT_ID", "my-project")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}
			if cfg.Reminders.Backend != tt.want || cfg.Reminders.LeadTime != 24*time.Hour {
				t.Errorf("reminders = %+v, want backend %q and 24h lead", cfg.Reminders, tt.want)
			}
		})
	}
}

func TestCloudTasksReminderConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("GCP_PROJECT_ID", "my-project")
	t.Setenv("REMINDER_BACKEND", "cloudtasks")
	t.Setenv("REMINDER_LEAD_TIME", "2h")
	t.Setenv("CLOUD_TASKS_LOCATION", "europe-west1")
	t.Setenv("CLOUD_TASKS_QUEUE", "appointment-reminders")
	t.Setenv("CLOUD_TASKS_TARGET_URL", "https://booking.example.run.app/tasks/reminders")
	t.Setenv("CLOUD_TASKS_AUDIENCE", "https://booking.example.run.app")
	t.Setenv("CLOUD_TASKS_SERVICE_ACCOUNT", "runtime@my-project.iam.gserviceaccount.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if !cfg.Reminders.Enabled() || cfg.Reminders.LeadTime != 2*time.Hour {
		t.Errorf("reminders = %+v", cfg.Reminders)
	}
}

func TestInvalidReminderConfiguration(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		want string
	}{
		"memory in production": {
			env:  map[string]string{"APP_ENV": "production", "GCP_PROJECT_ID": "p", "REMINDER_BACKEND": "memory"},
			want: "timers would be lost",
		},
		"unknown backend": {
			env:  map[string]string{"APP_ENV": "development", "REMINDER_BACKEND": "cron"},
			want: "must be disabled, memory or cloudtasks",
		},
		"invalid lead": {
			env:  map[string]string{"APP_ENV": "development", "REMINDER_LEAD_TIME": "tomorrow"},
			want: "REMINDER_LEAD_TIME",
		},
		"incomplete cloud tasks": {
			env:  map[string]string{"APP_ENV": "development", "REMINDER_BACKEND": "cloudtasks"},
			want: "CLOUD_TASKS_QUEUE is required",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() error = %v, want %q", err, tt.want)
			}
		})
	}
}

// TestLoadReportsEveryError guards the decision to accumulate failures rather
// than return on the first one.
func TestLoadReportsEveryError(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("PORT", "http")
	t.Setenv("LOG_LEVEL", "verbose")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded, want error")
	}

	for _, want := range []string{"APP_ENV", "PORT", "LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}
