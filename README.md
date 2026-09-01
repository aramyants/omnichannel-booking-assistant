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
| Conversation persistence and deduplication | next |
| Asynchronous processing | not started |
| Altegio integration | not started |
| AI orchestration | not started |
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
internal/adapters/    Provider integrations behind those ports
internal/platform/    Cross-cutting concerns: config, logging, HTTP server
deployments/          Deployment scripts
```

Packages are added as they are needed rather than scaffolded up front.

Provider payloads never leave their adapter. Each one is normalised into a
single envelope type at the boundary, so adding a channel does not change the
conversation logic.

## Licence

Not yet chosen.
