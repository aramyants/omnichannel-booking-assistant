# Owned browser booking — Cloudflare Free

Current booking address: `https://book.motionconcept.rest/`. The checked-in
configuration uses the existing Free D1 database, exact production origin, unlimited
daily/monthly business SMS volume, and enabled appointment confirmation/reminder
flags. Missing credentials still fail closed. Build the sibling website with
`npm run build:booking` before `npm run deploy`. Its assets are isolated in
`.releases/booking/public`; `npm run build:marketing` prepares a separate landing
release. All three migrations are registered in the deployed D1 migration history.
Public DNS and HTTPS for `book` are active. `LEGACY_BOOKING_ACTIVE=false` redirects
old booking entries to the clean root. `e-motion.rest` and `www.e-motion.rest` also
redirect there. The main landing domain links directly to this origin and uses
the separate `emotion-studio-site` static Worker.

Worker + D1 + static assets with the existing Go service as the shared verified
language, calendar and durable confirmation owner. Browser booking
uses deterministic code; it makes no AI calls. Infrastructure is intended for a
Cloudflare **Free** account with no paid plan or paid add-ons. Free quotas may
stop the application; they are not an assurance of unlimited operation.

## Deployment contract

Use Node 22.18+ (Node 25 used for the tests). Install the locked dependencies,
run `npm test` and `npm run typecheck`. Root deployment must point the assets
directory at the rebuilt **booking profile** in `.releases/booking/public`, retaining
`public/_headers`. Worker-first routing is required for every path so canonical
redirects run before static assets; API paths
must never return the SPA fallback. Apply the D1 migration before enabling the
Worker. Keep the same `SESSION_SECRET` across deployments to preserve sessions.

`wrangler.toml` records the Free production deployment at
`https://book.motionconcept.rest`, its D1 binding, public widget key and the
owner's explicit unlimited SMS-volume settings and authorized worldwide SMS
destinations. Keep `PUBLIC_ORIGIN` and the Turnstile hostname aligned. Configure secret
bindings using the provider's
secret facility; never commit credentials or a `.dev.vars` file:

| Binding / variable | Required value |
| --- | --- |
| `DB` | D1 database binding with `migrations/0001_booking.sql` applied |
| `ASSETS` | Built static assets binding |
| `PUBLIC_ORIGIN` | Exact HTTPS origin, without slash; API and site share it |
| `LEGACY_BOOKING_ACTIVE` | Default `false`; temporary exact old-host support only while canonical origin is `https://book.motionconcept.rest` |
| `SESSION_SECRET` | Cryptographically random secret, at least 32 characters |
| `ALTEGIO_PARTNER_TOKEN` | Existing authorized studio integration token |
| `ALTEGIO_USER_TOKEN` | Required only for enabled reminders: server-only current recipient check on the exact owned record; public APIs never receive it |
| `ALTEGIO_COMPANY_ID` | `1389810` |
| `ALTEGIO_CURRENCY` | `AMD` |
| `ECOSYSTEM_GATEWAY_ORIGIN` | Exact existing Cloud Run origin for the signed internal bridge |
| `ECOSYSTEM_CONTEXT_KEY` | Domain-separated server signing key; secret binding, never browser-visible |
| `TURNSTILE_SECRET` | Secret verification key for this widget |
| `TURNSTILE_SITE_KEY` | Public widget key; returned by `/config` |
| `SMSGATE_CREDENTIALS_JSON` | Existing pinned JSON: `username`, `password`, `device_id`, integer `sim_number`, optional official `api_base_url` |
| `SMS_ALLOWED_COUNTRY_CODES` | `374` by default; explicit `*` permits all metadata-valid international numbers; a comma-separated full calling-code allowlist is also supported |
| `SMS_DAILY_CAP` | Positive message send cap, `0` to disable, or explicit `unlimited` |
| `SMS_MONTHLY_CAP` | Positive message send cap, `0` to disable, or explicit `unlimited` |
| `SMS_CONFIRMATIONS_ENABLED` | `false` by default; opt-in SMS confirmation route |
| `SMS_REMINDERS_ENABLED` | `false` by default; one consented reminder about two hours before an owned web appointment |
| `BOOKING_ENABLED` | `false` until review and deployment validation complete |

The SMS caps cover verification and optional confirmation together, in Yerevan
calendar days/months. Reserved, rejected and uncertain sends all consume budget.
Localized SMS is bounded at 201 UTF-16 units, up to three concatenated Unicode
parts. Caps count messages rather than carrier segments or monetary spend.
The owner has confirmed the SIM's automated and international SMS coverage.
The owner's explicit `unlimited` value removes only daily/monthly business-volume
caps. It is represented by SQL NULL and does not turn missing/invalid config into
unlimited mode. Phone/session/IP cooldowns, OTP attempts and Turnstile remain active.
Booking paused also blocks new OTP sends. No code bypass, test OTP, fabricated
proof or fallback around missing configuration exists.

Worldwide mode uses pinned `libphonenumber-js/max` metadata for complete country
calling-code and national-number validation. Input is parsed strictly without
extracting a phone from prose, extensions are rejected, and every accepted phone
is normalized to canonical E.164 before OTP budgets, hashing, provider sending or
booking. Armenian national entry remains supported; foreign numbers require an
explicit country code. Shared calling codes such as `+1` are matched as complete
codes, not arbitrary string prefixes. An explicit `*` is the only all-destination
mode; malformed or unsupported allowlist configuration fails closed. Number
validity does not prove that a handset exists or receives SMS; OTP and provider
delivery receipts still establish those separate facts. International sending
uses the existing owner-confirmed SIM plan, independent of Cloudflare's free
hosting quotas; the application does not buy or activate a new carrier service.

## Browser API

Every response has `Cache-Control: no-store`. Responses expose only explicit
customer-safe fields. Errors are `{error:{code,message}}` with an appropriate
HTTP status. API and page must have exactly the configured origin; no credentialed
CORS is enabled. All POSTs require that exact `Origin`. Except for `/session`,
they also require `X-CSRF-Token` from the current session response.

| Route under `/api/booking` | Input and output |
| --- | --- |
| `GET /config` | Public widget key, `smsEnabled`, `bookingEnabled`, country codes, currency, timezone |
| `POST /session` | `{}` → `{csrfToken,phoneVerified,phone?,unresolvedDraftId?}` and a secure session cookie |
| `GET /services` | `{services:[{id,name,description,durationMinutes,priceMin,priceMax,category}],constraints}` |
| `GET /staff?service_id=…` | `{staff:[{id,name,specialization,avatarUrl?}]}`; actual HTTPS Altegio photos |
| `GET /dates?service_id=…&staff_id=…` | `{dates:["YYYY-MM-DD"]}` |
| `GET /slots?service_id=…&staff_id=…&date=…` | `{slots:[{datetime,label}]}` |
| `POST /verification/request` | `{phone,turnstileToken}` → `{status:"pending",expiresIn:300}` |
| `POST /verification/confirm` | `{code}` → `{phoneVerified:true,phone}` |
| `GET /verification/status` | Current session's OTP `{status,expiresIn,retryAfter,phoneVerified?}`; no code, send ID or phone |
| `POST /drafts` | `{serviceId,staffId,datetime,name,consent:true}` → `{draft,phoneVerified:true}` |
| `POST /appointments` | `{draftId}` → confirmed, pending or uncertain status |
| `GET /appointments/:draftId` | Read this session's submitted draft status |

IDs are strings. Datetimes have explicit `+04:00` timezone and minute precision.
The draft includes authoritative service/staff names, price range and display
price, duration, name and start time; the browser cannot supply price/duration
or a recipient override. A separate confirmation creates the appointment.
Confirmed appointments expose ID, names, datetime, duration, price and price range.
No management hash is returned. The full appointment-history portal and universal
cancellation/rescheduling are intentionally outside this first release.

The session cookie is `__Host-motion_booking`, Secure, HttpOnly, SameSite Strict,
path `/`. Only its SHA-256 digest is stored. Session lifetime is 24 hours and
refreshing the page preserves verified ownership within that lifetime. OTPs are
single-use, HMAC protected, five-minute codes with five attempts. They do not
enter logs, customer-visible errors or stored plaintext. Requesting a new code
removes the session's old proof until the new code is confirmed. No first booking
is possible without proof of the exact phone held by that session.

The code-entry screen may poll `/verification/status` roughly every ten seconds.
It checks only the current authenticated session's latest OTP message and never
accepts a customer-supplied provider ID. `queued` is acceptance, `sent` is the
carrier/SMSC handoff, and `delivered` requires the provider receipt. Failure and
expiry are explicit. No code, phone, device, message body or provider error is
returned. Provider reads are paced; code expiry and successful verification stop
the browser status endpoint's further provider reads. Final delivery/failure
states cannot be overwritten by a delayed non-final response. This does not
automatically send another code. The background receipt monitor can retain the
final operational delivery outcome separately.

`X-Booking-Language` or `X-Booking-Locale` accepts `hy`, `ru` or `en`. Otherwise
the supported language with the highest `Accept-Language` quality wins, falling
back to Russian. `Content-Language` records that non-sensitive UI context; phone
verification and access rules do not depend on language.

The Turnstile widget must set action **`booking_otp`**. Server validation requires
success, exact hostname and that action. Cloudflare's trusted `CF-Connecting-IP`
is HMAC hashed for budgets. The Worker does not trust `X-Forwarded-For`. New
sessions have spam protections of 10/hour/IP and 100/minute globally. API traffic
has burst protections of 90/minute/IP and 300/minute globally; neither is a
daily business-volume allowance. OTP send budgets are one/minute/phone
and session, five/hour/phone and session, ten/hour/IP, plus hard global caps.
SMS IDs are durably reserved before a provider request; sends never auto-retry.

While the new canonical booking hostname is propagating, explicit
`LEGACY_BOOKING_ACTIVE=true` permits only `https://booking.motionconcept.rest`
as a temporary second incoming host, and only with canonical `PUBLIC_ORIGIN`
set to `https://book.motionconcept.rest`. Each mutation's Origin must match its
own incoming host; old-to-new and new-to-old credentialed requests remain blocked.
Turnstile must prove that same incoming hostname. Cookies remain host-only and
no credentialed CORS is introduced. With the flag off or absent, old-host GETs
redirect to the canonical host without query data. The canonical root returns
the booking variant's root HTML at `/`, preserving matching server-rendered and
client routing while the browser keeps `https://book.motionconcept.rest/`.
The normal marketing build does not use this booking-only flag. Duplicate booking paths
redirect to that root; marketing hosts and non-booking assets keep their paths.
Disable the transition flag after public DNS is verified.

## Calendar integrity and notifications

Treatments, including Four Hands, use their native service IDs. Optional paid
extra time is added to the same appointment. Staff qualification and service
duration/price use the filtered live catalog. The exact offered slot is checked
again before creation, followed immediately by `book_check`. Availability is a
snapshot, never a reservation. Changed quote/duration/name requires a new summary.
When the global catalog omits numeric duration, a duration with an explicit
minute unit in its maintained title supplies the display value. Known studio
copy is a fallback only when its immutable ID and category match. The filtered
specialist's duration remains authoritative at preparation. Any longer calendar
slot is stored separately as a private reservation length and rechecked; it is
not presented as a longer treatment or asserted to be a particular kind of buffer.

The code uses the live Go client's required `Accept` header, `fullname`, optional
validated email and comments, selected service, timezone and disabled native
notification flags. With the shared bridge enabled, `api_id` is
`emotion-web-v2-<draftId>` and Go owns the confirmation, using a deterministic
event and delivery ledger. The legacy standalone mode retains
`omnichannel-web-<draftId>`. The correlation field is not provider-guaranteed
idempotency.

D1 atomically changes `ready → checking → sending` before the single external
creation POST. Concurrent clicks only read that intent. Lost responses, transport
failures, 5xx, unreadable successful responses or interrupted send intents become
`uncertain`, and **never** trigger another creation POST. Operators must inspect
Altegio using the correlation ID in D1 before advising a retry. No automatic
expiry reset can repeat a send. A definitive slot rejection requires a fresh draft.
Refreshing the page recovers an unresolved draft ID for the same verified session.
A phone with a checking/sending/uncertain outcome cannot start another creation,
including through a new browser session after phone verification. A partial unique
index enforces one such outcome per phone across concurrent requests. Preparing
a newer draft supersedes that session's older unsubmitted summaries.
If the provider confirms but the D1 result save fails, the browser is told the
result is uncertain, without risking another create.

The page itself displays the confirmed receipt. Optional SMS confirmation uses
the explicit consent captured in the stored draft, only when enabled and within
the same global SMS caps. It records a unique outbox row before sending once.
Provider acceptance or `Sent` is never presented as recipient delivery. A failed
or uncertain SMS cannot make an existing appointment look failed. Every five
minutes, the scheduled worker reads at most three outstanding provider message
IDs, recording Pending/Processed/Sent/Delivered/Failed/Cancelled without resending.
Unknown reads remain unconfirmed and cannot become a guessed delivery or failure.
Monitoring ends after 24 hours. Localized messages are never truncated; a confirmed
provider Delivered status is retained as that provider receipt. Outbox records
remain private operator evidence; automatic staff alerts are not implemented.

When reminders are enabled, a confirmed appointment booked at least two hours
and five minutes ahead gets one job due two hours before it starts. Jobs share
the draft's explicit appointment-message consent. The scheduled worker checks at
most two due jobs, within a 15-minute due window. It rereads the exact appointment
using the private creation hash and public booking endpoint, then checks the exact
record/branch and its current normalized recipient phone with the authorized
private record endpoint. Public capability responses can omit the client phone;
they are never treated as proof of an unchanged recipient. Missing private
credentials, a different phone, canceled, changed, unreadable or incomplete
appointments do not trigger stale reminders. A changed
appointment is left for staff rather than inventing a new reminder schedule.
Bookings made closer to the visit do not get a late reminder. Reminder send intent
and unique draft/purpose outbox keys prevent duplicate SMS after concurrent runs,
crashes or uncertain sends. Only safe record/status GETs may repeat. The private
hash is never shown to a browser, put in a client link or logged.

New web bookings retain their provider management proof on the server, bound to
the current verified phone. The existing bot's two-step change/cancel flow can
use that proof. Manual records without a proof stay staff-assisted. The signed
context bridge shares locale, permissions, private calendar identity and durable
notification deduplication. This release does not enable Altegio's native browser
OTP or automatic review campaigns. No historical review requests are replayed.
Web reminders are this new
Worker's explicitly enabled route and do not use the existing Go reminder queue.

## Verification and operation

The test suite uses an isolated in-memory SQLite database implementing the D1
binding interface and mocked provider endpoints. It covers origin/CSRF, hashed
session cookies, OTP expiry/attempts/caps, global budget reservation, hidden
services, quote tampering, verified-phone gates, concurrent confirmations,
uncertain upstream results, stale quotes and cross-session status access. It
does not send SMS or modify a real calendar. `npm run typecheck` checks the
Worker source independently of the test harness.

Before production cutover, validate the actual deployed Cloudflare entrypoint,
static security headers and `/api/*` routing, configured public origin, real
catalog filtering, empty/available date states, and session/CSRF behavior. Any
live delivery/booking test needs an explicitly consenting studio test recipient
and a clearly identified controlled appointment; do not use customer records.

The scheduled task removes expired sessions and counters, eligible drafts older
than 30 days and SMS rows older than 62 days. Confirmed future appointments and
drafts needed by unexpired pending reminders survive this cleanup even when
booked months ahead. Uncertain send records are
retained for reconciliation; review these manually and set a retention policy
once resolved. Keep Worker/DB on Free, leave paid services disabled, and keep
Cloudflare free-quota failure behavior visible to users.

## Verified specialist reviews

`/reviews/` reuses the secure booking session and SMS phone proof. The Go bridge
lists only past bookings owned by that exact phone, within 90 days and already
finished. An arrival mark is not required; canceled records and explicit no-shows
are excluded. The public badge means verified booking ownership, not independently
verified attendance. Submission re-reads the private record. The
browser cannot choose a specialist or supply attendance, client ID or phone.
`verified_reviews` stores one immutable review per appointment with a unique
primary key, HMAC phone identifier, actual specialist, 1–5 rating and optional
plain-text comment. Private feedback stays out of public totals. Publication is
an explicit unchecked-by-default choice; all published ratings, including low
ratings, count equally. Public APIs contain no phone, appointment ID or private
capability. Contact information, links, HTML and oversized inputs are rejected.

The existing calendar receipt links to the review page; that link proves no
identity and cannot bypass SMS ownership or completion checks. Existing Altegio
ratings are not relabeled as our verified reviews. No review is fabricated during
live QA and no new paid email provider is enabled. Studio administrators can
inspect private feedback through the existing Cloudflare D1 dashboard. For example:

```sql
SELECT staff_name, rating, comment, display_name, published,
       datetime(created_at, 'unixepoch') AS received_utc
FROM verified_reviews ORDER BY created_at DESC LIMIT 100;
```

Apply `0004_verified_reviews.sql` before deploying the new endpoints. `/reviews`
is private/no-store at the API boundary, and the PWA does not cache API data.

References: [D1 prepared/batch transactions](https://developers.cloudflare.com/d1/worker-api/d1-database/),
[Turnstile server verification](https://developers.cloudflare.com/turnstile/get-started/server-side-validation/).
# Catalog, extra time and certificates — 20 October 2026

The owned catalog now returns published service images, live prices and distinct
`treatment` and `extra` kinds. Extra time is never a standalone
appointment. An optional `extra_id` joins the primary service in staff/date/time
queries; `extraId` joins the draft and one native appointment's `services` array.
Both specialist eligibility and the complete duration/quote are checked again
before creation. Slots too short for the combined treatment are hidden. Existing
single-service drafts retain their original representation. Reminder validation
compares the complete native service set.

Four Hands is not manually restricted. The current native widget treats Relax
and Sport as selectable services with a specialist and calendar. Their unfiltered
public catalog durations are null; the specialist-filtered catalog supplies 60
minutes for both. The published Sport name is used instead of the older private
title that says 80 minutes. `/dates` returns the specialist-specific selection
quote so the browser updates duration and price before phone verification. Draft
and confirmation re-read those values and call native `book_check`. No second
appointment, invented resource or separate therapist hold is added by our app.

`GET /certificate-types` exposes only sanitized catalog fields from the configured
location's canonical Altegio certificate-type endpoint. Archived types are hidden.
Certificate types, including deposits, are read live rather than counted or priced
in frontend code. On 20 October the live catalog had online sale disabled for all
published types. The UI shows value and validity and marks online purchase
unavailable, without a support-chat substitute. It never interprets a zero
online sale price as a free certificate.

`GET /benefits` requires successful session phone proof and only queries that
verified phone. Full certificate codes stay server-side; the client sees masked
references and its own balance/status. Optional `certificateId` is rechecked when
preparing and confirming a draft. Expired/exhausted or no-longer-owned certificates
cannot be attached. The actual reference reaches only the appointment's private
studio comment as a request to apply it at checkout. It does not debit credit,
record a payment, apply a discount or promise that service restrictions match.

Production certificate reads use the existing signed ecosystem bridge, backed by
the central API service's current Altegio credential. `certificate-types` accepts
no client identity; `client-certificates` requires the proven session phone.
The central adapter searches only the fixed location, matches the exact normalized
phone and reads `/locations/{location}/client/{client}/loyalty`. This documented
client summary works with `clients_loyalty_read_access`; the legacy certificate
endpoint returned 403 even after that read permission was installed. No balance,
payment, expiry-editing or loyalty-card issuance rights were added. Missing phone
fields, incomplete searches and missing certificate lists fail closed. Duplicate
client rows and identical certificate copies are deduplicated; conflicting copies
are rejected. The app installation was reconnected to apply the approved grant.
The legacy direct adapter is only a fallback for standalone installations without
the ecosystem gateway. Private credentials never reach public catalog,
availability or creation calls. The bridge remains fresh-body HMAC authenticated.
All currency values, activation, native availability and eligibility remain
Altegio-owned. Personal loyalty percentages are not inferred from product titles.

The native widget advertised 20 complimentary head/face minutes on 20 October,
while service 13914939 (`First visit +20min`) was inactive and the documented
location `booking_widget_promo` field was null. The localized banner is a reviewed
copy snapshot of that public offer, not an automatically synchronized rule. The
UI explicitly says the studio arranges it; it does not reserve or grant free time.
Do not reactivate it or invent a historical-client eligibility test implicitly.
Paid +20/+40 services use their current API prices and combined availability.

### Native automatic specialist assignment

`GET /staff` includes `anyStaff`, read through the signed central bridge from
Altegio's current `any_master` setting. Unreadable settings hide automatic
assignment; named-specialist booking stays available. Selecting staff `0` uses
native combined availability and a quote range across eligible staff. Exact
variation in treatment duration is deferred to the review instead of inventing
one value. The draft resolves a real eligible specialist and calls `book_check`.
The final review shows that specialist and exact quote. Confirmation revalidates
that same assignment and fails rather than silently switching staff or price.
No native appointment is sent with staff `0`, and no hold is created for a draft.

The form currently supports one primary treatment plus one paid time addition.
General multiple-treatment selection and importing historical native reviews
remain separate parity work; do not describe this release as full feature parity.
