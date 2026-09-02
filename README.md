# Omnichannel Booking Assistant

A Go backend for an AI booking assistant that talks to customers on their own
messaging channel and creates real appointments in [Altegio](https://alteg.io).

Customers ask for an appointment in natural language. The assistant works out
what they want, checks live availability, collects what it needs and books it.
The language model interprets the conversation; it never decides the outcome.
Booking, cancellation and rescheduling all run through deterministic code that
validates every request before it reaches Altegio.

## Status

Early. Telegram messages arrive, are normalised and are answered. Nothing is
booked yet.

| Area | State |
| --- | --- |
| HTTP gateway, config, logging, graceful shutdown | done |
| Telegram: webhook, normalisation, replies | done |
| Customer identity, conversations, transcript, deduplication | done, in-process store |
| Altegio: catalogue, availability, booking | done |
| AI orchestration and tool calling | done |
| Booking from a conversation | done |
| Durable storage | next |
| Cancel and reschedule, reminders | not started |
| WhatsApp, Instagram, Messenger, Viber | not started |

## Design

A modular monolith in one Go module, deployed as small services on Cloud Run.
Every external system sits behind an interface, so the business logic depends on
nothing it does not own.

```
Telegram  WhatsApp  Instagram  Messenger  Viber
    \        |          |          |       /
     \       |          |          |      /
      +--------------------------------------+
      |          Webhook gateway             |
      |  verify, normalise, deduplicate, ack |
      +--------------------------------------+
                        |
                    Pub/Sub
                        |
      +--------------------------------------+
      |             Worker                   |
      |  conversation state, AI tool calls   |
      +--------------------------------------+
                        |
              Application services
                        |
              Altegio adapter -> Altegio
```

Dependencies point inward: adapters depend on the application layer, the
application layer depends on the domain, and the domain depends on neither.

Webhooks are acknowledged before any slow work starts. Everything expensive
happens asynchronously, so a provider never waits on an AI call or an Altegio
request and never times out into a retry storm.

## Operating behaviour

Logs are JSON on stdout, keyed for Cloud Logging so entries arrive as
structured fields rather than text. Every log line carries the running build.

Every request is given an identifier, returned in `X-Request-Id` and attached to
every entry written while handling it. An identifier supplied by the caller, or
the trace Cloud Run attaches, is reused rather than replaced, so one customer
message can be followed across services. Query strings and bodies are never
logged: webhook URLs carry provider verification tokens and bodies carry
customer messages.

A panic in a handler becomes a 500 and one log entry with a stack, rather than a
dropped connection the caller sees as a network failure. Request bodies are
capped at 1 MiB.

Every provider retries deliveries, so each one is recorded once handled and a
repeat is dropped. A delivery is marked handled only after its reply is out:
marking it earlier would mean a failure part-way through loses the message,
because the retry would be discarded as a duplicate.

A customer is not the same thing as a messaging account. Accounts are stored as
separate identities pointing at one customer, so the same person writing from
two channels keeps one history. Identities are never merged automatically on a
resemblance such as a matching name, because wrongly merging two people would
expose one customer's appointments to another.

Whether the assistant answers is a stored conversation state, not a prompt
instruction. Once a colleague takes a conversation over, the assistant stops
replying and no wording in a customer's message can change that.

The transcript holds what was said and nothing else: customer messages and the
replies they were sent. No hidden model reasoning is stored.

**Storage is currently in-process**, so state is lost on restart and not shared
between instances. The durable store lands behind the same interfaces.

On `SIGTERM` the server stops accepting connections and waits for in-flight
requests to finish, so a deploy cannot turn a half-processed webhook into a
duplicate booking.

## Requirements

- Go 1.27 or newer
- Docker, to build or run the container
- GNU Make, optional

## Running locally

```sh
cp .env.example .env
export APP_ENV=development
make run
```

```sh
curl localhost:8080/health
# {"status":"ok","version":"dev"}
```

Or in a container:

```sh
make docker-build
make docker-run
```

## Configuration

All configuration comes from the environment and is validated at startup. A
missing or invalid value stops the process with an error naming every problem it
found, rather than failing later inside a request.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `APP_ENV` | yes | | `development`, `staging` or `production` |
| `PORT` | no | `8080` | TCP port to listen on |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn` or `error` |
| `SHUTDOWN_TIMEOUT` | no | `15s` | How long in-flight requests may take to drain |
| `PUBLIC_BASE_URL` | no | | This service's public https origin, used to register webhooks |
| `TELEGRAM_BOT_TOKEN` | no | | Enables the Telegram channel when set |
| `TELEGRAM_WEBHOOK_SECRET` | with token | | Shared secret Telegram echoes on every delivery |
| `TELEGRAM_API_BASE_URL` | no | | Overrides the Telegram host, for local stubs or egress proxies |
| `ALTEGIO_PARTNER_TOKEN` | no | | Enables the scheduling system when set |
| `ALTEGIO_USER_TOKEN` | no | | Authorises the business's own data |
| `ALTEGIO_COMPANY_ID` | with token | | The Altegio location this deployment serves |
| `ALTEGIO_TIMEZONE` | no | `UTC` | The business's timezone, as an IANA name |
| `ALTEGIO_CURRENCY` | no | | Labels the prices Altegio returns |
| `ALTEGIO_BASE_URL` | no | | Overrides the Altegio host, for local stubs |
| `BUSINESS_NAME` | no | | What the assistant calls the business |
| `OPENAI_API_KEY` | no | | Enables the language model when set |
| `OPENAI_MODEL` | no | adapter default | Model identifier |
| `OPENAI_BASE_URL` | no | | Overrides the OpenAI host, for local stubs or proxies |

A channel is enabled by supplying its credentials and disabled by leaving them
out, in which case its endpoint is not served at all. Enabling Telegram without
a webhook secret is refused at startup rather than defaulted, because the
alternative is an unauthenticated public endpoint.

Secrets are never read from files in the repository. In production they come
from Secret Manager and arrive as environment variables.

## Channels

### Telegram

The service registers its own webhook at startup from `PUBLIC_BASE_URL`, so
deploying it is the only step needed to make it reachable, and a changed URL
cannot be left pointing at a previous deployment.

Every delivery must carry the configured secret in
`X-Telegram-Bot-Api-Secret-Token`, compared in constant time. Telegram publishes
no source IP range and signs nothing, so that secret is the endpoint's only
authentication. Deliveries without it are refused before anything is parsed.

The status returned to Telegram decides whether it redelivers, so the two
failure modes are answered differently. A body that cannot be parsed will never
parse, so it is logged and acknowledged. A message that parsed but could not be
handled may succeed on a second attempt, so it is answered with an error and
Telegram sends it again.

Messages the assistant cannot read, such as voice notes, are answered by saying
so rather than ignored. A caption on a photo is treated as the request, because
that is usually where it is.

## Scheduling

Altegio holds the calendar and stays the authority on it. This service reads the
catalogue and availability from it and writes appointments to it; it never keeps
a competing copy of who is booked when.

Availability is a snapshot, never a reservation. Between showing a customer a
free slot and booking it, somebody else can take it, so every booking is checked
immediately before it is created and a refusal is reported as the slot having
gone rather than as an error.

A booking request carries an idempotency key, sent to Altegio as `api_id`, so
the same request submitted twice is recognisable rather than a second
appointment. Booking requests are never retried automatically: nothing in the
published API guarantees a repeat is recognised, and the cost of being wrong is
a customer with two appointments. A request whose outcome is never learned is
reported as such, so it can be reconciled instead of guessed at.

Times are stored in UTC and sent to Altegio in the business's own timezone. A
customer asking for "Friday at three" means three o'clock where the salon is,
and some Altegio times arrive without an offset, so `ALTEGIO_TIMEZONE` has to be
set correctly or every appointment lands at the wrong hour.

Requests are limited to four per second, under Altegio's ceiling of five per
second and two hundred per minute per IP address. Reads are retried with
exponential backoff and jitter; rejected credentials are not, because they fail
identically every time.

At startup the service reads the catalogue once, so a wrong token or location id
becomes one line in the deployment log rather than a customer being told the
assistant cannot help.

The response shapes follow Altegio's published documentation, which does not
fully specify them. The mapping is tolerant of missing fields and covered by
fixtures in `internal/adapters/altegio/testdata`, which is where to correct any
difference found against a live account.

## The assistant

The model interprets; it never decides. It cannot reach Altegio, never sees a
credential, and has no way to state a fact the business has not confirmed. What
it can do is ask for named tools, which ordinary Go code then validates and
runs:

```
customer message
      |
      v
model  ->  "run find_available_slots{staff_id, date}"
      |
      v
application  validates the arguments, refuses a date in the past,
             parses it in the business timezone, calls Altegio
      |
      v
model  ->  phrases the real answer
      |
      v
customer
```

Every fact in a reply comes from a tool. The catalogue is filtered before the
model sees it, so a service the business has withdrawn cannot be offered no
matter what the model does with it.

The loop is bounded. A model that keeps asking for the same lookup is stopped
after a few rounds and the conversation goes to a colleague, rather than
spending money and the customer's patience.

A tool that fails hands the model an explanation rather than an error, because a
customer asking about a day the salon is closed should be told so, not met with
silence. If the model itself cannot be reached, the customer still gets an
answer and a person picks the conversation up.

Only what a person could have read is stored or replayed: customer messages and
the replies they were sent. The model's private reasoning is discarded, and
OpenAI is asked not to retain the conversation, because this system keeps the
only copy that matters.

### Taking a booking

Agreeing to an appointment and creating one are two separate steps, and the
separation is what makes "never tell a customer they have an appointment before
one exists" something the code enforces rather than something the prompt asks
for.

`prepare_booking` resolves the service and specialist from the catalogue, reads
the time in the business's timezone, normalises the phone number, asks the
scheduling system whether the booking would be accepted, and stores a draft. It
creates nothing, and it tells the model so.

`confirm_booking` takes **no arguments**. The details are whatever was prepared,
so nothing can change between the customer agreeing and the appointment being
made. Only when the scheduling system has confirmed does anything report a
booking.

The idempotency key is generated once, when the draft is prepared, and reused
unchanged on every attempt. Regenerating it would turn a retry into a second
appointment.

Three outcomes, three different behaviours:

- **Booked.** The customer is told, with the reference.
- **The time was taken** between preparing and confirming. The draft is
  discarded and the model is told to offer what is left, and explicitly told not
  to say the appointment was made.
- **The outcome is unknown**, because the request left and no answer came. The
  appointment may or may not exist, so the conversation goes to a colleague to
  reconcile, and the model is told to say neither that it worked nor that it
  failed.

A draft is not a hold. After an hour it is refused, because the availability it
was built from is stale enough that confirming is more likely to fail than
succeed.

### Why the prompt is not the security boundary

A determined customer can talk any model out of any instruction. So nothing
important depends on one. Whether the assistant replies at all is stored
conversation state, not a sentence in a prompt: once a colleague takes a
conversation over, no wording in a customer's message can start it answering
again, and a test asserts exactly that against an injection attempt. Tool
arguments are decoded with unknown fields refused, so a model inventing a
plausible extra parameter is rejected rather than quietly acted on.

## Deployment

See [docs/deployment.md](docs/deployment.md) for going live on Cloud Run and for
the credentials each channel needs.

```sh
export GCP_PROJECT_ID=my-project
export TELEGRAM_BOT_TOKEN=... TELEGRAM_WEBHOOK_SECRET=...
./deployments/gcp/deploy.sh
```

## Tests and checks

```sh
make test        # unit tests
make test-race   # with the race detector, needs cgo and a C compiler
make lint        # golangci-lint
make vulncheck   # known vulnerabilities in dependencies and the standard library
make check       # vet, lint, test and build
```

CI runs formatting, `go vet`, `golangci-lint`, `govulncheck`, the race-enabled
test suite and a build on every pull request.

## Layout

```
cmd/gateway/          HTTP entry point, routes and middleware wiring
internal/domain/      Channel-independent types and rules
internal/application/ Use cases, and the ports they need
internal/adapters/    Provider integrations and storage behind those ports
internal/platform/    Cross-cutting concerns: config, logging, HTTP server, ids
deployments/          Deployment scripts
```

Packages are added as they are needed rather than scaffolded up front.

Provider payloads never leave their adapter. Each one is normalised into a
single envelope type at the boundary, so adding a channel does not change the
conversation logic.

## Licence

Not yet chosen.
