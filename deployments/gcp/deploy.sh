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
ENV_VARS="APP_ENV=production,LOG_LEVEL=${LOG_LEVEL},STORAGE_BACKEND=firestore,GCP_PROJECT_ID=${PROJECT_ID}"
[[ -n "${BUSINESS_NAME:-}" ]]     && ENV_VARS+=",BUSINESS_NAME=${BUSINESS_NAME}"
[[ -n "${ALTEGIO_COMPANY_ID:-}" ]] && ENV_VARS+=",ALTEGIO_COMPANY_ID=${ALTEGIO_COMPANY_ID}"
[[ -n "${ALTEGIO_TIMEZONE:-}" ]]  && ENV_VARS+=",ALTEGIO_TIMEZONE=${ALTEGIO_TIMEZONE}"
[[ -n "${ALTEGIO_CURRENCY:-}" ]]  && ENV_VARS+=",ALTEGIO_CURRENCY=${ALTEGIO_CURRENCY}"
[[ -n "${OPENAI_MODEL:-}" ]]      && ENV_VARS+=",OPENAI_MODEL=${OPENAI_MODEL}"

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
  "${SECRET_FLAGS[@]}" \
  --quiet

SERVICE_URL="$(gcloud run services describe "${SERVICE}" --region "${REGION}" --format='value(status.url)')"
CURRENT_BASE_URL="$(gcloud run services describe "${SERVICE}" --region "${REGION}" \
  --format='value(spec.template.spec.containers[0].env.filter("name", "PUBLIC_BASE_URL").extract("value"))' 2>/dev/null || true)"

if [[ "${CURRENT_BASE_URL}" != *"${SERVICE_URL}"* ]]; then
  say "Telling the service its own address so it can register webhooks"
  gcloud run services update "${SERVICE}" \
    --region "${REGION}" \
    --update-env-vars "PUBLIC_BASE_URL=${SERVICE_URL}" \
    --quiet
fi

say "Deployed"
echo "  service:  ${SERVICE_URL}"
echo "  health:   ${SERVICE_URL}/health"
echo
echo "Checking the deployment answers:"
curl -fsS "${SERVICE_URL}/health" && echo
