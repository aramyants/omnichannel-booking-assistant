#!/usr/bin/env bash
#
# Deploy the gateway to Cloud Run.
#
# The script is idempotent: run it for the first deployment and for every one
# after. It enables the APIs it needs, stores credentials in Secret Manager,
# builds the container and deploys it.
#
# The service URL is not known until the first deployment exists, and the
# service needs that URL to register its own webhooks. The script therefore
# deploys, reads the URL back and applies it, which is why a first run performs
# two revisions and later runs only one.
#
# Usage:
#   export GCP_PROJECT_ID=your-project
#   export TELEGRAM_BOT_TOKEN=... TELEGRAM_WEBHOOK_SECRET=...
#   ./deployments/gcp/deploy.sh

set -euo pipefail

PROJECT_ID="${GCP_PROJECT_ID:?set GCP_PROJECT_ID to your Google Cloud project id}"
REGION="${GCP_REGION:-europe-west1}"
FIRESTORE_LOCATION="${FIRESTORE_LOCATION:-${REGION}}"
SERVICE="${SERVICE_NAME:-omnichannel-booking-assistant}"
LOG_LEVEL="${LOG_LEVEL:-info}"
RUNTIME_SA_NAME="${RUNTIME_SERVICE_ACCOUNT:-booking-assistant-runtime}"
RUNTIME_SA="${RUNTIME_SA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"
REMINDER_QUEUE="${CLOUD_TASKS_QUEUE:-appointment-reminders}"
REMINDER_LEAD_TIME="${REMINDER_LEAD_TIME:-24h}"

# Cloud Run scales to zero, so an idle service costs nothing. One instance is
# kept warm at most; concurrency is well above what a single business produces.
MAX_INSTANCES="${MAX_INSTANCES:-4}"
CONCURRENCY="${CONCURRENCY:-80}"
MEMORY="${MEMORY:-256Mi}"

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }

say "Project ${PROJECT_ID}, region ${REGION}, service ${SERVICE}"
gcloud config set project "${PROJECT_ID}" >/dev/null

say "Enabling the APIs this deployment uses"
gcloud services enable \
  run.googleapis.com \
  cloudbuild.googleapis.com \
  artifactregistry.googleapis.com \
  secretmanager.googleapis.com \
  firestore.googleapis.com \
  cloudtasks.googleapis.com \
  iam.googleapis.com \
  --quiet

say "Preparing Firestore in ${FIRESTORE_LOCATION}"
if ! gcloud firestore databases describe --database='(default)' >/dev/null 2>&1; then
  # A Firestore database's location cannot be changed later. Keeping it beside
  # Cloud Run minimises latency and cross-region traffic.
  gcloud firestore databases create \
    --database='(default)' \
    --location="${FIRESTORE_LOCATION}" \
    --type=firestore-native \
    --delete-protection \
    --quiet
fi

# Processed webhook ids expire after seven days. The application already
# ignores expired entries; TTL performs the eventual physical deletion.
if ! gcloud firestore fields ttls list \
  --database='(default)' \
  --collection-group=processed_events \
  --format='value(name)' | grep -q '/fields/expires_at$'; then
  gcloud firestore fields ttls update expires_at \
    --database='(default)' \
    --collection-group=processed_events \
    --enable-ttl \
    --async \
    --quiet
fi

say "Preparing the least-privilege runtime identity"
if ! gcloud iam service-accounts describe "${RUNTIME_SA}" >/dev/null 2>&1; then
  gcloud iam service-accounts create "${RUNTIME_SA_NAME}" \
    --display-name="Omnichannel booking assistant runtime" \
    --quiet
fi
gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
  --member="serviceAccount:${RUNTIME_SA}" \
  --role=roles/datastore.user \
  --quiet >/dev/null
gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
  --member="serviceAccount:${RUNTIME_SA}" \
  --role=roles/cloudtasks.enqueuer \
  --quiet >/dev/null

# Creating a task with an OIDC token requires iam.serviceAccounts.actAs for the
# identity named on that task. The runtime uses its own identity, so the grant
# is narrow rather than project-wide.
gcloud iam service-accounts add-iam-policy-binding "${RUNTIME_SA}" \
  --member="serviceAccount:${RUNTIME_SA}" \
  --role=roles/iam.serviceAccountUser \
  --quiet >/dev/null

say "Preparing the reminder queue"
if ! gcloud tasks queues describe "${REMINDER_QUEUE}" --location "${REGION}" >/dev/null 2>&1; then
  # Every duration is given in seconds. Cloud Tasks rejects the compound forms
  # gcloud accepts elsewhere, such as 24h or 10m, with a message about the
  # format rather than a hint about which unit it wants.
  gcloud tasks queues create "${REMINDER_QUEUE}" \
    --location "${REGION}" \
    --max-attempts 20 \
    --max-retry-duration 86400s \
    --min-backoff 10s \
    --max-backoff 600s \
    --max-doublings 5 \
    --quiet
fi

# put_secret writes a value to Secret Manager, creating the secret on first use
# and adding a version on every run. Values arrive from the environment and are
# never echoed.
put_secret() {
  local name="$1" value="$2"

  if ! gcloud secrets describe "${name}" >/dev/null 2>&1; then
    gcloud secrets create "${name}" --replication-policy=automatic --quiet
  fi
  printf '%s' "${value}" | gcloud secrets versions add "${name}" --data-file=- --quiet >/dev/null
  echo "  stored ${name}"
}

SECRET_NAMES=()
SECRET_MAPPINGS=()

# store records one credential and maps it onto an environment variable in the
# running container. Values that are not set are skipped, so a partial
# configuration deploys and reports what is missing at startup rather than
# failing here.
store() {
  local env_var="$1" secret_name="$2" value="$3"

  [[ -z "${value}" ]] && return 0

  put_secret "${secret_name}" "${value}"
  SECRET_NAMES+=("${secret_name}")
  SECRET_MAPPINGS+=("${env_var}=${secret_name}:latest")
}

say "Storing credentials in Secret Manager"

if [[ -n "${TELEGRAM_BOT_TOKEN:-}" ]]; then
  : "${TELEGRAM_WEBHOOK_SECRET:?set TELEGRAM_WEBHOOK_SECRET when TELEGRAM_BOT_TOKEN is set}"
fi

store TELEGRAM_BOT_TOKEN       telegram-bot-token       "${TELEGRAM_BOT_TOKEN:-}"
store TELEGRAM_WEBHOOK_SECRET  telegram-webhook-secret  "${TELEGRAM_WEBHOOK_SECRET:-}"
store ALTEGIO_PARTNER_TOKEN    altegio-partner-token    "${ALTEGIO_PARTNER_TOKEN:-}"
store ALTEGIO_USER_TOKEN       altegio-user-token       "${ALTEGIO_USER_TOKEN:-}"
store OPENAI_API_KEY           openai-api-key           "${OPENAI_API_KEY:-}"

SECRET_FLAGS=()
if [[ ${#SECRET_MAPPINGS[@]} -gt 0 ]]; then
  # The runtime service account is granted read access to exactly the secrets
  # this deployment uses, and no others.
  for secret in "${SECRET_NAMES[@]}"; do
    gcloud secrets add-iam-policy-binding "${secret}" \
      --member="serviceAccount:${RUNTIME_SA}" \
      --role=roles/secretmanager.secretAccessor \
      --quiet >/dev/null
  done
  echo "  granted ${RUNTIME_SA} read access to ${#SECRET_NAMES[@]} secret(s)"

  SECRET_FLAGS+=(--set-secrets "$(IFS=,; echo "${SECRET_MAPPINGS[*]}")")
fi

# Settings that are not credentials travel as plain environment variables.
EXISTING_SERVICE_URL="$(gcloud run services describe "${SERVICE}" --region "${REGION}" \
  --format='value(status.url)' 2>/dev/null || true)"

ENV_VARS="APP_ENV=production,LOG_LEVEL=${LOG_LEVEL},STORAGE_BACKEND=firestore,GCP_PROJECT_ID=${PROJECT_ID},REMINDER_LEAD_TIME=${REMINDER_LEAD_TIME}"
if [[ -n "${EXISTING_SERVICE_URL}" ]]; then
  ENV_VARS+=",PUBLIC_BASE_URL=${EXISTING_SERVICE_URL}"
  ENV_VARS+=",REMINDER_BACKEND=cloudtasks,CLOUD_TASKS_LOCATION=${REGION},CLOUD_TASKS_QUEUE=${REMINDER_QUEUE}"
  ENV_VARS+=",CLOUD_TASKS_TARGET_URL=${EXISTING_SERVICE_URL}/tasks/reminders,CLOUD_TASKS_AUDIENCE=${EXISTING_SERVICE_URL}"
  ENV_VARS+=",CLOUD_TASKS_SERVICE_ACCOUNT=${RUNTIME_SA}"
else
  # The first revision is needed to learn Cloud Run's stable service URL.
  ENV_VARS+=",REMINDER_BACKEND=disabled"
fi
[[ -n "${BUSINESS_NAME:-}" ]]     && ENV_VARS+=",BUSINESS_NAME=${BUSINESS_NAME}"

# The description can contain commas and newlines, which the comma-separated
# --set-env-vars form cannot carry. It travels as its own delimited assignment.
if [[ -n "${BUSINESS_DESCRIPTION:-}" ]]; then
  DESCRIPTION_FLAG=(--set-env-vars "^@@^BUSINESS_DESCRIPTION=${BUSINESS_DESCRIPTION}")
else
  DESCRIPTION_FLAG=()
fi
[[ -n "${ALTEGIO_COMPANY_ID:-}" ]] && ENV_VARS+=",ALTEGIO_COMPANY_ID=${ALTEGIO_COMPANY_ID}"
[[ -n "${ALTEGIO_TIMEZONE:-}" ]]  && ENV_VARS+=",ALTEGIO_TIMEZONE=${ALTEGIO_TIMEZONE}"
[[ -n "${ALTEGIO_CURRENCY:-}" ]]  && ENV_VARS+=",ALTEGIO_CURRENCY=${ALTEGIO_CURRENCY}"
[[ -n "${OPENAI_MODEL:-}" ]]      && ENV_VARS+=",OPENAI_MODEL=${OPENAI_MODEL}"
[[ -n "${TELEGRAM_STAFF_CHAT_ID:-}" ]] && ENV_VARS+=",TELEGRAM_STAFF_CHAT_ID=${TELEGRAM_STAFF_CHAT_ID}"

VERSION="$(git rev-parse --short HEAD 2>/dev/null || echo dev)"

say "Building and deploying revision ${VERSION}"
# --allow-unauthenticated is required: messaging providers call the webhook
# endpoints and cannot present a Google identity. The endpoints authenticate
# each provider themselves, by shared secret or signature.
gcloud run deploy "${SERVICE}" \
  --source . \
  --region "${REGION}" \
  --allow-unauthenticated \
  --max-instances "${MAX_INSTANCES}" \
  --concurrency "${CONCURRENCY}" \
  --memory "${MEMORY}" \
  --service-account "${RUNTIME_SA}" \
  --set-env-vars "${ENV_VARS}" \
  "${DESCRIPTION_FLAG[@]}" \
  "${SECRET_FLAGS[@]}" \
  --quiet

SERVICE_URL="$(gcloud run services describe "${SERVICE}" --region "${REGION}" --format='value(status.url)')"

if [[ -z "${EXISTING_SERVICE_URL}" ]]; then
  say "Enabling webhooks and authenticated reminder tasks at the service address"
  gcloud run services update "${SERVICE}" \
    --region "${REGION}" \
    --update-env-vars "PUBLIC_BASE_URL=${SERVICE_URL},REMINDER_BACKEND=cloudtasks,CLOUD_TASKS_LOCATION=${REGION},CLOUD_TASKS_QUEUE=${REMINDER_QUEUE},CLOUD_TASKS_TARGET_URL=${SERVICE_URL}/tasks/reminders,CLOUD_TASKS_AUDIENCE=${SERVICE_URL},CLOUD_TASKS_SERVICE_ACCOUNT=${RUNTIME_SA}" \
    --quiet
fi

say "Deployed"
echo "  service:  ${SERVICE_URL}"
echo "  health:   ${SERVICE_URL}/health"
echo
echo "Checking the deployment answers:"
curl -fsS "${SERVICE_URL}/health" && echo
