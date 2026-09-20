# Deployment

How to run this service on Google Cloud and connect it to the outside world.

## What has to be arranged by hand

Most of the deployment is scripted. Four things cannot be, because they need an
account that belongs to a person, a payment method, or a verified business
identity. Each one produces a credential that the deployment script then takes
care of.

| Credential | Where it comes from | Needed for |
| --- | --- | --- |
| Google Cloud project with billing enabled | console.cloud.google.com | Everything |
| Telegram bot token | BotFather, in Telegram | The Telegram channel |
| Altegio partner and user tokens, and the company id | Altegio marketplace and account settings | Reading availability and creating bookings |
| Meta app and channel-specific account tokens/access | developers.facebook.com; see [Meta setup](meta-setup.md) | WhatsApp, Instagram and Messenger |

Nothing else needs a console. Once those values exist, deployment is one
command.

## Google Cloud

Install the [gcloud CLI](https://cloud.google.com/sdk/docs/install) and sign in:

```sh
gcloud auth login
```

Create a project and attach billing. Billing must be enabled even though the
expected cost is close to zero: Cloud Run refuses to deploy without it.

```sh
gcloud projects create my-booking-assistant
gcloud billing accounts list
gcloud billing projects link my-booking-assistant --billing-account=<ACCOUNT_ID>
```

Then deploy. The script enables the APIs it needs, stores credentials in Secret
Manager, builds the container and deploys it:

```sh
export GCP_PROJECT_ID=my-booking-assistant
export GCP_REGION=europe-west1
export FIRESTORE_LOCATION=europe-west1
export TELEGRAM_BOT_TOKEN=...
export TELEGRAM_WEBHOOK_SECRET=...

./deployments/gcp/deploy.sh
```

On Windows, run it from Git Bash.

The first run creates the Firestore database with deletion protection, enables
TTL cleanup for processed webhook ids, creates a least-privilege runtime
service account, and creates the Cloud Tasks reminder queue. It then performs
two revisions: a service cannot know its own URL until it exists, and it needs
that URL for webhooks and authenticated reminder tasks, so the script deploys,
reads the URL back and applies it. Later runs deploy once.

### Automatic, atomic releases from GitHub

Run `deployments/gcp/deploy.sh` for the first deployment and whenever the
service's infrastructure, secrets or runtime configuration must change. Normal
application releases do not need that script or copied environment variables.

The checked-in `cloudbuild.yaml` is the release pipeline for the existing
service. It:

1. runs formatting checks, `go vet`, and the race-enabled test suite;
2. builds an image named by the exact Git commit and pushes it to Artifact
   Registry;
3. deploys that image as a tagged revision with zero production traffic;
4. calls the candidate revision's `/health` endpoint and verifies its reported
   commit; and
5. moves 100% of traffic in one Cloud Run traffic update only after the probe
   succeeds.

Connect the GitHub repository once in **Cloud Run > the service > Source >
Connect repository**, choose Cloud Build, select the `main` branch, and choose
`cloudbuild.yaml` as the configuration file. The build identity needs Artifact
Registry Writer, Cloud Run Admin, Logs Writer, and permission to act as
`booking-assistant-runtime@PROJECT_ID.iam.gserviceaccount.com`. After that, a
merge to `main` is the deploy action. A failed test, build, startup, or health
probe leaves the currently serving revision untouched.

The defaults in `cloudbuild.yaml` match this service (`europe-west1`, repository
`cloud-run-source-deploy`, and service `omnichannel-booking-assistant`). Change
the trigger's `_REGION`, `_REPOSITORY`, or `_SERVICE` substitutions if the
Google Cloud resources use different names.

SSH for Cloud Run services is currently a limited preview, not a dependable
deployment channel. This production image is also deliberately distroless and
contains no shell. Use Cloud Logging for diagnosis, Cloud Shell or the local
`gcloud` CLI for administration, and deploy a new immutable revision for code
changes. If the project is later admitted to the SSH preview, use it only for
temporary inspection rather than changing a live container: instances remain
disposable and those changes disappear when an instance stops.

Cloud Tasks signs reminder requests with an OIDC token for the runtime service
account. The public Cloud Run service validates that identity inside the
reminder route; provider webhook routes continue to use their own signatures or
shared secrets.

`FIRESTORE_LOCATION` defaults to `GCP_REGION`. Choose it carefully on the first
run because a Firestore database's location cannot be changed later.

### What it costs

Cloud Run scales to zero, so an idle service is free. For a single salon the
request volume sits inside the free tier. The recurring cost is Secret Manager
and Artifact Registry storage, which is cents per month. The AI provider, once
connected, will be the largest line item.

### Choosing a region

Pick the one closest to your customers, because it sets the latency of every
reply. `europe-west1` is Belgium, `europe-west3` is Frankfurt,
`me-central1` is Doha. Changing region later means a new service and a new URL,
so the webhooks have to be re-registered.

## Telegram

Open Telegram, message [@BotFather](https://t.me/BotFather) and send
`/newbot`. It asks for a display name and a username ending in `bot`, then gives
you a token that looks like `123456789:AAH...`. That is `TELEGRAM_BOT_TOKEN`.
Anyone holding it controls the bot.

Invent `TELEGRAM_WEBHOOK_SECRET` yourself. It is the only thing proving a
webhook delivery came from Telegram rather than from anyone on the internet who
found the URL, so generate it randomly and never reuse it:

```sh
openssl rand -hex 32
```

Only `A-Z`, `a-z`, `0-9`, `_` and `-` are allowed, up to 256 characters. The
service refuses to start with a token but no secret, so the endpoint can never
be exposed unauthenticated.

You do not need to call `setWebhook`. The service registers itself at startup
using `PUBLIC_BASE_URL`, which the deployment script sets. To confirm afterwards:

```sh
curl "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getWebhookInfo"
```

`pending_update_count` climbing and a non-empty `last_error_message` mean the
service is rejecting or failing deliveries.

## Altegio

The service reads the catalogue and availability and creates bookings through
Altegio. The credentials to provide are:

Register at the [Altegio marketplace](https://alteg.io) and the partner token
appears in your account settings. Business data additionally needs a user token,
obtained through the user authorization method with the business account
credentials. Both travel in one header:

```
Authorization: Bearer <partner_token>, User <user_token>
```

The API is at `https://api.alteg.io/api/v1`, documented at
[developer.alteg.io](https://developer.alteg.io/api). It allows 200 requests per
minute and 5 per second per IP, which the adapter will have to respect.

You will also need the company id of the salon, which appears in the Altegio
URL when managing the business.

## Meta channels

WhatsApp Cloud API, Messenger and Instagram Login have incoming text and outgoing
reply adapters. Each channel needs its own account ID/token and webhook
subscription; business verification alone does not connect the channel.
Follow the [Meta setup checklist](meta-setup.md) for credentials, callback URLs,
account subscriptions and the separate access/review requirements.

The deployment stores `WHATSAPP_ACCESS_TOKEN`, `MESSENGER_ACCESS_TOKEN`,
`INSTAGRAM_ACCESS_TOKEN`, `META_APP_SECRET` and `META_VERIFY_TOKEN` in Secret
Manager. Their account IDs and `META_GRAPH_VERSION` are ordinary environment
settings. Leave a channel's access token and account ID empty to disable it.

Meta channels receive text/audio and reply with text; Telegram-specific inline
buttons are not sent on Meta. Incoming audio uses `OPENAI_API_KEY` with
`OPENAI_TRANSCRIPTION_MODEL` (default `gpt-4o-mini-transcribe`). Automated reminders
are currently Telegram only; Meta reminders are skipped until channel-specific
templates and messaging-window handling are implemented. Staff follow-ups remain
subject to each channel's messaging window.

## Operating the service

Logs, filtered to errors:

```sh
gcloud run services logs read omnichannel-booking-assistant \
  --region europe-west1 --limit 100
```

Every entry carries `version` and, for anything handling a request,
`request_id`. To follow one customer message end to end, filter on its
`request_id` in the Cloud Logging console.

Rolling back is immediate, because every deployment keeps its predecessor:

```sh
gcloud run revisions list --service omnichannel-booking-assistant --region europe-west1
gcloud run services update-traffic omnichannel-booking-assistant \
  --region europe-west1 --to-revisions <REVISION>=100
```

## Rotating a credential

Secrets are read at startup, so a new version needs a new revision:

```sh
printf '%s' "<new value>" | gcloud secrets versions add telegram-bot-token --data-file=-
gcloud run services update omnichannel-booking-assistant --region europe-west1
```

Rotating `TELEGRAM_WEBHOOK_SECRET` is safe at any time: the service re-registers
the new secret with Telegram as it starts.
