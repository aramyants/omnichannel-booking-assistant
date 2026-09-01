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
| Meta app with WhatsApp, Instagram or Messenger access | developers.facebook.com, with business verification | The Meta channels |

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
export TELEGRAM_BOT_TOKEN=...
export TELEGRAM_WEBHOOK_SECRET=...

./deployments/gcp/deploy.sh
```

On Windows, run it from Git Bash.

The first run performs two revisions. A service cannot know its own URL until it
exists, and it needs that URL to register its webhooks, so the script deploys,
reads the URL back and applies it. Later runs deploy once.

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

Not yet implemented. The credentials to have ready:

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

Not yet implemented. WhatsApp, Instagram and Messenger all run through one Meta
app at [developers.facebook.com](https://developers.facebook.com), and all three
require business verification, which takes days and needs company documents.
Start that process early if those channels matter.

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
