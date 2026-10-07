# Booking and messaging audit — 7 October 2026

## Confirmed production failures

Instagram delivery was rejected because Meta invalidated its Instagram Login
token. Meta reported a password change or security-session invalidation; the
response does not establish which occurred. App publication and webhook
subscription were still enabled. The owner renewed the token through Instagram
Login. Secret Manager version 2 is pinned in the running service, and the token
passed a read-only account check. No test customer message was sent.

Facebook Messenger's Page token is valid, includes `pages_messaging` and
`pages_manage_metadata`, and its Page subscription includes `messages` for this
app. A Page metadata read requires an additional unrelated permission, so it
must not be used to conclude that messaging is disabled. WhatsApp's system-user
token is valid; messaging is LIMITED with GREEN quality because the display
name is pending, rather than BLOCKED. Calling/SIP warnings do not disable chat.

The reported Telegram-to-WhatsApp test already resolved to the same verified
phone profile. Two older local bookings had been deleted in Altegio. Their
online-management reads returned 404, and the first failure aborted the entire
appointment list before the valid booking could be returned. The native,
verified-phone history correctly included the valid visit and the deletions.

## Implemented protections

- Verified-phone native history is authoritative. Merge local management proofs
  into live entries; do not revive missing/deleted visits or read obsolete hashes
  before the native list. Provider failures remain failures, never empty history.
- Natural appointment queries use the same verified cabinet as its menu button.
  A failed list ends the turn with localized retry/person controls, preserving
  assistant state instead of inviting an automatic model handoff.
- A separate bounded `gpt-4.1-mini` classification returns only studio,
  appointments, or unrelated intent. It uses no tools, no reasoning request,
  96 output tokens, four bounded context messages and a five-second deadline.
  Failure does not bypass the gate. Menus and verification remain available.
- The booking prompt restricts the mission. Structured final replies declare
  their purpose; unrelated or executable/code replies are replaced with studio
  navigation. Maximum input is 4,000 characters for non-menu model requests;
  the reasoning loop has a 16,000 output-token total and eight calls per batch.
- Instagram/Messenger token health is stored separately from WhatsApp health.
  Probes are leased and cached. Definite authentication rejection immediately
  blocks additional reasoning on that channel; incoming messages are retained
  for staff. Transient probe errors preserve the last confirmed state.
- Appointment/tool faults are observable without logging message bodies or
  management proofs. Retiring an already-cleared Telegram keyboard is idempotent.

## SMS and review activation

The backend's SMSGate device/SIM integration and `NATIVE_SMS_READY` are enabled
for eligible transactional notices and cabinet verification. This is separate
from Altegio's native SMS-provider registration. Altegio's notification-channel
page still lists SMS and WhatsApp as not connected. Its review requests are
enabled for email only. Native review SMS is therefore not activated.

The last recorded provider-contract request is support ticket **141720495**.
The current signed-in email account has no reply for that ticket, and the
Telegram support account is not signed into this browser. Do not claim support
has approved/closed it. Obtain the exact send/status callback contract, provider
activation and sender configuration, then implement and validate the bridge
before enabling review SMS. Do not guess the callback schema or replay old
reviews. See [native notifications](native-notifications.md) for the active
transactional outbox and [Altegio notifications](altegio-notifications.md) for
the provider onboarding request.

## Verification

Normal Go tests and vet cover the regression paths. Synthetic live classifier
evaluation passed 28 cases, including the reported Armenian transliterations,
phone/name replies, bot/SMS problems, programming, role changes, prompt/key
requests, mixed queries and an unrelated follow-up after a booking conversation.
English, Russian and Armenian category replies passed synthetic live evaluation.
The expanded 13-case Armenian voice evaluation also passed with the scope gate
enabled. SMSGate authenticated and returned the configured Samsung work device.
All evaluations use recording senders and fake calendars. Deployment CI runs
Linux race checks and vulnerability scanning before promoting an immutable
Cloud Run candidate. These checks reduce risk; they do not establish that every
future provider outage or adversarial message is impossible.
