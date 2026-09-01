# Omnichannel Booking Assistant

A Go backend for an AI booking assistant that talks to customers on their own
messaging channel and creates real appointments in [Altegio](https://alteg.io).

Customers ask for an appointment in natural language. The assistant works out
what they want, checks live availability, collects what it needs and books it.
The language model interprets the conversation; it never decides the outcome.
Booking, cancellation and rescheduling all run through deterministic code that
validates every request before it reaches Altegio.

## Status

Early. The foundation runs; no channels are connected yet.

| Area | State |
| --- | --- |
| HTTP gateway, config, logging, graceful shutdown | done |
| Telegram | not started |
| WhatsApp, Instagram, Messenger, Viber | not started |
| Altegio integration | not started |
| AI orchestration | not started |

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
# {"status":"ok"}
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

Secrets are never read from files in the repository. In production they come
from Secret Manager and arrive as environment variables.

## Tests

```sh
make test
make test-race   # needs cgo and a C compiler
make check       # vet, test and build
```

CI runs formatting, `go vet`, the race-enabled test suite and a build on every
pull request.

## Layout

```
cmd/gateway/          HTTP entry point and route registration
internal/platform/    Cross-cutting concerns: config, logging, HTTP server
```

Packages are added as they are needed rather than scaffolded up front.

## Licence

Not yet chosen.
