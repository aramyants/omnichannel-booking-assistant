// Command gateway is the HTTP entry point for the booking assistant. It
// receives channel webhooks, normalises them and hands them to the application.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	// The runtime image is distroless and carries no timezone database, so
	// without this every location lookup would fail and appointment times would
	// silently fall back to UTC. Embedding it costs a few hundred kilobytes and
	// removes a class of bug that only appears in production.
	_ "time/tzdata"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/ai/openai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/altegio"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/meta"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/telegram"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/firestore"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	cloudtasksadapter "github.com/aramyants/omnichannel-booking-assistant/internal/adapters/tasks/cloudtasks"
	localtasks "github.com/aramyants/omnichannel-booking-assistant/internal/adapters/tasks/local"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/tasks/taskhttp"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/assistant"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/reminders"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/config"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/httpserver"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/logging"
)

// version identifies the running build. It is overwritten at link time with
// -ldflags "-X main.version=<revision>" and stays "dev" for local builds.
var version = "dev"

// buildVersion is the revision to log and report on the health endpoint.
//
// A source deploy builds the container through Cloud Build, which offers no way
// to pass a Docker build argument, so the link-time stamp above is always "dev"
// there no matter what the deploy script worked out. BUILD_VERSION carries the
// revision as an ordinary environment variable instead and wins when it is set.
// Builds that can stamp the binary, such as the Makefile's, still do and set
// nothing, so neither path has to know about the other.
func buildVersion() string {
	if stamped := strings.TrimSpace(os.Getenv("BUILD_VERSION")); stamped != "" {
		return stamped
	}
	return version
}

// webhookRegistrationTimeout bounds the calls that tell providers where to
// deliver updates. They run during startup, so they must not be able to hang it.
const webhookRegistrationTimeout = 15 * time.Second

// schedulingCheckTimeout bounds the startup check against the scheduling
// system, for the same reason.
const schedulingCheckTimeout = 15 * time.Second

func main() {
	// Install a structured logger before anything else can fail. Configuration
	// loading is itself a source of fatal errors, and those entries need the
	// same JSON shape as the rest of the process or they cannot be alerted on.
	slog.SetDefault(logging.New(os.Stdout, slog.LevelInfo))

	if err := run(); err != nil {
		slog.Error("gateway exited with an error", "error", err)
		os.Exit(1)
	}
}

// run holds the real body of main. main itself only translates an error into an
// exit code, because os.Exit skips deferred functions and would otherwise
// silently drop cleanup.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.LogLevel).With("version", buildVersion())

	// Replace the bootstrap logger with the configured one. Everything that
	// reaches for slog directly, including the standard library's log package,
	// then writes in the same format at the same level.
	slog.SetDefault(logger)

	// NotifyContext cancels ctx when the process is asked to stop. Cloud Run
	// sends SIGTERM before reclaiming an instance; Ctrl-C sends SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	gw := &gateway{logger: logger, version: buildVersion()}
	senders := make(map[messaging.Provider]assistant.Sender)

	var telegramClient *telegram.Client
	if cfg.Telegram.Enabled() {
		var opts []telegram.ClientOption
		if cfg.Telegram.APIBaseURL != "" {
			opts = append(opts, telegram.WithBaseURL(cfg.Telegram.APIBaseURL))
			logger.Warn("using a non-default telegram api host", "host", cfg.Telegram.APIBaseURL)
		}
		telegramClient = telegram.NewClient(cfg.Telegram.BotToken, opts...)
		senders[messaging.ProviderTelegram] = telegramClient
	}

	var whatsappClient *meta.Client
	if cfg.WhatsApp.Enabled() {
		opts := []meta.Option{meta.WithGraphVersion(cfg.WhatsApp.GraphVersion)}

		client, err := meta.NewClient(cfg.WhatsApp.AccessToken, cfg.WhatsApp.PhoneNumberID, opts...)
		if err != nil {
			return err
		}
		whatsappClient = client
		senders[messaging.ProviderWhatsApp] = client
	}

	// Without a staff channel a customer asking for a person changes a stored
	// state and nobody is told, so the absence is worth saying out loud.

	var scheduling assistant.Scheduling
	if cfg.Altegio.Enabled() {
		opts := []altegio.Option{
			altegio.WithLocation(cfg.Altegio.Location),
			altegio.WithCurrency(cfg.Altegio.Currency),
		}
		if cfg.Altegio.BaseURL != "" {
			opts = append(opts, altegio.WithBaseURL(cfg.Altegio.BaseURL))
			logger.Warn("using a non-default altegio host", "host", cfg.Altegio.BaseURL)
		}

		client, err := altegio.NewClient(
			cfg.Altegio.PartnerToken,
			cfg.Altegio.UserToken,
			cfg.Altegio.CompanyID,
			logger,
			opts...,
		)
		if err != nil {
			return err
		}
		verifyScheduling(ctx, client, cfg, logger)
		scheduling = client
	}

	var model ai.Provider
	if cfg.AI.Enabled() {
		opts := []openai.Option{openai.WithModel(cfg.AI.Model)}
		if cfg.AI.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(cfg.AI.BaseURL))
			logger.Warn("using a non-default openai host", "host", cfg.AI.BaseURL)
		}

		client, err := openai.NewClient(cfg.AI.APIKey, opts...)
		if err != nil {
			return err
		}
		model = client
		logger.Info("language model configured", "model", client.Model())
	} else {
		logger.Warn("no language model configured: the assistant will acknowledge " +
			"messages and hand them to a colleague")
	}

	store, closeStore, err := openStore(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer closeStore()
	var staff assistant.StaffNotifier
	if telegramClient != nil && cfg.Telegram.StaffChatID != "" {
		notifier, err := telegram.NewStaffNotifier(telegramClient, cfg.Telegram.StaffChatID, store)
		if err != nil {
			return err
		}
		staff = notifier
		logger.Info("handovers will be announced to the staff chat",
			"chat_id", cfg.Telegram.StaffChatID)
	} else {
		logger.Warn("no staff chat configured: customers asking for a person will " +
			"wait without anyone being told. Set TELEGRAM_STAFF_CHAT_ID")
	}

	reminderService, reminderHandler, closeReminders, err := openReminders(
		ctx, cfg, store, senders, logger,
	)
	if err != nil {
		return err
	}
	defer closeReminders()
	gw.reminder = reminderHandler

	assistantService, err := assistant.NewService(assistant.Deps{
		Senders:       senders,
		Customers:     store,
		Conversations: store,
		Messages:      store,
		Processed:     store,
		Logger:        logger,
		AI:            model,
		Scheduling:    scheduling,
		Bookings:      store,
		Staff:         staff,
		Reminders:     reminderService,
		Business: assistant.Business{
			Name:        cfg.BusinessName,
			Description: cfg.BusinessDescription,
			Location:    cfg.Altegio.Location,
		},
	})
	if err != nil {
		return err
	}

	if cfg.Telegram.Enabled() {
		gw.telegram = telegram.NewHandler(
			telegram.NewWebhook(cfg.Telegram.WebhookSecret),
			assistantService,
			logger,
			telegram.WithStaffChat(cfg.Telegram.StaffChatID),
			// Turns the staff chat from a noticeboard into a desk: replying to
			// a notification reaches the customer it names.
			telegram.WithStaffDesk(assistantService, store, telegramClient, cfg.Telegram.StaffChatID),
			// Lets a pressed button be acknowledged and an answered question
			// stop being answerable.
			telegram.WithButtons(telegramClient),
		)
	}

	if whatsappClient != nil {
		webhook, err := meta.NewWebhook(cfg.WhatsApp.AppSecret, cfg.WhatsApp.VerifyToken)
		if err != nil {
			return err
		}
		gw.whatsapp = meta.NewWhatsAppHandler(webhook, assistantService, logger)
	}

	srv, err := httpserver.New(ctx, cfg.Addr(), gw.routes(), logger, cfg.ShutdownTimeout)
	if err != nil {
		return err
	}

	// Registration happens after the listener is bound but before requests are
	// served. The socket already accepts connections at this point, so a
	// delivery arriving in the gap waits in the backlog rather than failing.
	if telegramClient != nil {
		registerTelegramWebhook(ctx, telegramClient, cfg, logger)
		publishTelegramMenu(ctx, telegramClient, cfg, logger)
	}

	logger.Info("gateway starting",
		"env", string(cfg.Env),
		"addr", srv.Addr(),
		"channels", enabledChannels(cfg),
	)

	return srv.Run(ctx)
}

// registerTelegramWebhook points Telegram at this deployment.
//
// A failure here is logged rather than fatal. The webhook is very likely still
// registered from a previous deployment, so refusing to start over a transient
// Telegram outage would take a working service down.
func registerTelegramWebhook(ctx context.Context, client *telegram.Client, cfg config.Config, logger *slog.Logger) {
	if cfg.PublicBaseURL == "" {
		logger.Warn("not registering the telegram webhook because PUBLIC_BASE_URL is unset")
		return
	}

	callbackURL := cfg.PublicBaseURL + TelegramWebhookPath

	ctx, cancel := context.WithTimeout(ctx, webhookRegistrationTimeout)
	defer cancel()

	if err := client.SetWebhook(ctx, callbackURL, cfg.Telegram.WebhookSecret); err != nil {
		logger.Error("could not register the telegram webhook",
			"error", err, "callback_url", callbackURL)
		return
	}

	logger.Info("registered the telegram webhook", "callback_url", callbackURL)
}

// publishTelegramMenu tells Telegram what to show beside the text box.
//
// It is published on every start for the same reason the webhook is: the menu
// belongs to the deployment, so it is stated rather than assumed, and a menu
// left behind by an older version cannot outlive it.
//
// A failure is logged rather than fatal. A menu is a convenience, and refusing
// to serve customers because a hint could not be published would be an odd
// trade.
func publishTelegramMenu(ctx context.Context, client *telegram.Client, cfg config.Config, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, webhookRegistrationTimeout)
	defer cancel()

	// One call per language. Telegram stores a menu per language and chooses
	// between them by the customer's app setting, so publishing the Armenian
	// menu is a separate call from publishing the default one.
	published := 0
	for _, menu := range assistant.Menus() {
		commands := make([]telegram.Command, 0, len(menu.Commands))
		for _, entry := range menu.Commands {
			commands = append(commands, telegram.Command{
				Name:        entry.Name,
				Description: entry.Description,
			})
		}

		if err := client.SetCommands(ctx, "", menu.LanguageTag, commands); err != nil {
			// One language failing is not a reason to abandon the rest: a
			// customer reading any of the others still gets a menu.
			logger.Error("could not publish the telegram command menu",
				"error", err, "language", menu.LanguageTag)
			continue
		}
		published++
	}

	// The staff group gets no menu at all. Everything a colleague does there is
	// a button on the notification it belongs to, and a customer menu offering
	// to book them an appointment would only be in the way.
	if cfg.Telegram.StaffChatID != "" {
		if err := client.SetCommands(ctx, cfg.Telegram.StaffChatID, "", nil); err != nil {
			logger.Warn("could not clear the command menu in the staff chat", "error", err)
		}
	}

	logger.Info("published the telegram command menu", "menus", published)
}

// appStore is everything the assistant needs to persist. Both the in-process
// and the Firestore implementations satisfy it, and nothing above this line
// knows which one is in use.
type appStore interface {
	assistant.CustomerRepository
	assistant.ConversationRepository
	assistant.MessageRepository
	assistant.ProcessedEvents
	assistant.BookingRepository

	// Staff notifications are linked to the conversation they announce, so a
	// colleague replying to one is understood.
	telegram.StaffThreads
	reminders.Repository
}

func openReminders(
	ctx context.Context,
	cfg config.Config,
	store appStore,
	assistantSenders map[messaging.Provider]assistant.Sender,
	logger *slog.Logger,
) (*reminders.Service, http.Handler, func(), error) {
	if !cfg.Reminders.Enabled() {
		logger.Info("appointment reminders are disabled")
		return nil, nil, func() {}, nil
	}

	senders := make(map[messaging.Provider]reminders.Sender, len(assistantSenders))
	for provider, sender := range assistantSenders {
		senders[provider] = sender
	}
	deps := reminders.Deps{
		Repository: store,
		Bookings:   store,
		Messages:   store,
		Senders:    senders,
		LeadTime:   cfg.Reminders.LeadTime,
		Location:   cfg.Altegio.Location,
		Logger:     logger,
	}

	switch cfg.Reminders.Backend {
	case config.RemindersMemory:
		var service *reminders.Service
		scheduler, err := localtasks.New(func(ctx context.Context, reminderID string) error {
			return service.Deliver(ctx, reminderID)
		}, logger)
		if err != nil {
			return nil, nil, nil, err
		}
		deps.Scheduler = scheduler
		service, err = reminders.NewService(deps)
		if err != nil {
			scheduler.Close()
			return nil, nil, nil, err
		}
		logger.Info("using in-process reminder timers: pending reminders are lost on restart",
			"lead_time", cfg.Reminders.LeadTime.String())
		return service, nil, scheduler.Close, nil

	case config.RemindersCloudTasks:
		scheduler, err := cloudtasksadapter.New(ctx, cloudtasksadapter.Config{
			ProjectID:           cfg.Reminders.ProjectID,
			Location:            cfg.Reminders.Location,
			Queue:               cfg.Reminders.Queue,
			TargetURL:           cfg.Reminders.TargetURL,
			Audience:            cfg.Reminders.Audience,
			ServiceAccountEmail: cfg.Reminders.ServiceAccountEmail,
		})
		if err != nil {
			return nil, nil, nil, err
		}
		deps.Scheduler = scheduler
		service, err := reminders.NewService(deps)
		if err != nil {
			_ = scheduler.Close()
			return nil, nil, nil, err
		}
		authorizer, err := cloudtasksadapter.NewAuthorizer(
			cfg.Reminders.Audience, cfg.Reminders.ServiceAccountEmail,
		)
		if err != nil {
			_ = scheduler.Close()
			return nil, nil, nil, err
		}
		handler, err := taskhttp.New(authorizer, service, logger)
		if err != nil {
			_ = scheduler.Close()
			return nil, nil, nil, err
		}
		logger.Info("using Cloud Tasks for appointment reminders",
			"queue", cfg.Reminders.Queue,
			"location", cfg.Reminders.Location,
			"lead_time", cfg.Reminders.LeadTime.String())
		return service, handler, func() {
			if err := scheduler.Close(); err != nil {
				logger.Error("could not close the Cloud Tasks client", "error", err)
			}
		}, nil

	default:
		return nil, nil, nil, fmt.Errorf("unsupported reminder backend %q", cfg.Reminders.Backend)
	}
}

// openStore builds the configured store and returns a function that releases
// it.
func openStore(ctx context.Context, cfg config.Config, logger *slog.Logger) (appStore, func(), error) {
	switch cfg.Storage.Backend {
	case config.StorageFirestore:
		store, err := firestore.New(ctx, cfg.Storage.ProjectID)
		if err != nil {
			return nil, nil, err
		}

		logger.Info("using firestore for storage", "project", cfg.Storage.ProjectID)

		return store, func() {
			if err := store.Close(); err != nil {
				logger.Error("could not close the firestore client", "error", err)
			}
		}, nil

	default:
		// Configuration refuses this in production, so reaching it means local
		// development, where losing state on restart is what you want.
		logger.Info("using in-process storage: state is lost on restart")
		return memory.New(), func() {}, nil
	}
}

// verifyScheduling reads the service catalogue once at startup.
//
// It turns a wrong token or a wrong location id into one clear line in the
// deployment log, rather than into a customer being told the assistant cannot
// help them. A failure is reported but does not stop the process: the messaging
// channels still work, and refusing to start over a scheduling outage would
// take the whole service down with it.
func verifyScheduling(ctx context.Context, client *altegio.Client, cfg config.Config, logger *slog.Logger) {
	if cfg.Altegio.UserToken == "" {
		logger.Warn("no altegio user token: the public catalogue is readable but " +
			"appointments and customer records are not")
	}

	ctx, cancel := context.WithTimeout(ctx, schedulingCheckTimeout)
	defer cancel()

	services, err := client.ListServices(ctx)
	if err != nil {
		logger.Error("could not read the altegio service catalogue",
			"error", err, "company_id", cfg.Altegio.CompanyID)
		return
	}

	logger.Info("connected to altegio",
		"company_id", cfg.Altegio.CompanyID,
		"bookable_services", len(services),
		"timezone", cfg.Altegio.Location.String(),
	)
}

func enabledChannels(cfg config.Config) []string {
	channels := []string{}
	if cfg.Telegram.Enabled() {
		channels = append(channels, string(messaging.ProviderTelegram))
	}
	if cfg.WhatsApp.Enabled() {
		channels = append(channels, string(messaging.ProviderWhatsApp))
	}
	return channels
}
