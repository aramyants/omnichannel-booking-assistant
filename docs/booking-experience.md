# Booking experience — 20 September 2026

## Customer flow

Greetings and `/start` open the live Altegio service categories immediately.
Categories lead to short, paginated service lists with live prices, then one
treatment's description. Back, categories, next/previous and human help are
deterministic; free-text requests still work. Choosing a treatment asks for a
day before the existing availability → draft → explicit confirmation flow.
Browsing abandons only unconfirmed changes, never a confirmed appointment.

Telegram uses inline keyboards. Messenger and Instagram use quick replies;
WhatsApp uses up to three reply buttons or a list (at most ten rows). Numbered
text is always available as a fallback for clients that do not display buttons.
The payload preserves the full option even when the visible title is shortened.
Old button generations cannot act on a newer prompt or confirmation.

Altegio's `GET /book_services/{location_id}` supplies service `comment` descriptions.
Existing multilingual copy is selected by conversation language. Reviewed aliases
are matched to exact live service IDs and categories, so duplicate names like
`50 min` become meaningful without changing live prices or availability.
The staff-filtered catalogue and available slot determine bookable duration.

Observed catalogue issues requiring a business decision (no Altegio writes made):

- +20 minutes: owner list says **10,000 AMD**, live Altegio says **18,000 AMD**.
  The bot quotes the live price. +40 minutes is 20,000 AMD.
- Face Motion Gua Sha is not in the live online catalogue; it is not offered.
- Motion Duo / Sport Duo mean **one customer, two therapists**, not two customers.
  Four-hands sessions and extra-time add-ons route to staff. The existing
  single-specialist booking API path cannot safely reserve both therapists or
  attach extra time to an existing session, so both UI and booking tools block
  standalone automatic booking of these treatments.

## Contact and confirmation

Client phones are validated with libphonenumber metadata and sent as E.164.
Armenian national forms (for example `094 768067`), `374...`, `00374...`, and
explicit international `+...` numbers are accepted only when valid. Ambiguous
foreign national numbers and malformed input are rejected before booking.
This validates structure, not ownership or reachability.

Successful booking and rescheduling produce a deterministic EN/RU/HY message:
date, local time and zone, service, therapist, reference, calendar link, address,
phone and official links. No Body Soft address, amenities or preparation advice
was copied. Public studio details were verified against `motionconcept.rest`:
Myasnikyan 1/6, Yerevan; +37494768067; Instagram `e.motion.concept`.

## Add to calendar

`PUBLIC_BASE_URL` must be the stable HTTPS Cloud Run URL, not a candidate tag.
Private `/calendar/{reference}?key=...&lang=...` links offer Google Calendar,
Apple/other calendar `.ics`, Outlook.com and Microsoft 365. The user must save or
import the event. In-app browsers and calendar apps vary; the page explains how
to open in a browser/download an ICS file. No account permissions are requested.
Imported events do **not** synchronize automatically after a move/cancellation.

The event uses the real duration and UTC instants, with local appointment time
shown on the page. ICS follows RFC 5545 with UTF-8-safe folding, escaped fields,
and a 24-hour display alarm (the calendar/device controls whether it fires).
No customer name, phone, or Altegio management hash goes into calendar exports.

The link's read-only capability is HMAC-derived from the unexposed random UUIDv4
booking ID, separate from the Altegio management proof. Treat the complete link
as private; anyone receiving it can read that one appointment. Application logs
omit query strings and management hashes; platform request logs may contain the
calendar capability, so restrict log access. Responses are non-cacheable and
no-referrer. Each view/export refreshes Altegio; cancellations and missing records
return 410, outages fail closed, and ended appointments expire after 24 hours.

## Reminders

`REMINDER_BACKEND=cloudtasks`, Firestore and `REMINDER_LEAD_TIME=24h` are already
configured in production. New confirmed appointments more than 24 hours away
get a durable task. Short-notice bookings do not get an immediate duplicate
reminder. Tasks are OIDC-authenticated, use deterministic IDs and delivery leases,
and refresh Altegio before sending. Cancellation or a changed start suppresses
the obsolete reminder. Provider sends are at-least-once: a crash between sending
and recording success can still cause a duplicate; there is no exactly-once API.

Telegram reminders are enabled. WhatsApp reminders require explicit opt-in AND
an approved utility template in the exact selected language. No template means
no opt-in offer and no attempted proactive send. STOP or `/stopreminders` revokes
consent, including while staff handles the chat, without cancelling the booking.
Messenger/Instagram out-of-window reminders are not enabled; no legacy message
tag or free-form workaround is used. A saved calendar event is the fallback.

Optional template variables (empty by default):

```
WHATSAPP_REMINDER_TEMPLATE_EN=
WHATSAPP_REMINDER_TEMPLATE_RU=
WHATSAPP_REMINDER_TEMPLATE_HY=
```

Only configure a language supported and approved by Meta for the account. The
template language code must exactly match `en`, `ru` or `hy`; do not set an
unapproved translation or different locale such as `en_US` in these slots.
Each template must have six positional BODY text parameters, in this order:
customer name, DD.MM.YYYY date, HH:mm time, service, therapist, private calendar
URL. Keep it appointment-specific, with no promotional offers. Example RU draft
for review, **not yet approved or configured**:

```
Здравствуйте, {{1}}! 🌿 Напоминаем о вашем визите в E-motion Concept.
Дата: {{2}}
Время: {{3}}
Услуга: {{4}}
Специалист: {{5}}
Добавить в календарь: {{6}}
Если планы изменились, напишите нам здесь. До встречи!
Отключить напоминания: STOP.
```

Receptionist changes made directly in Altegio are detected when the existing
task wakes or the calendar page is opened, not by continuous synchronization.
Moving an appointment earlier outside the bot can therefore miss the 24-hour
reminder. Full external-edit synchronization needs Altegio event/webhook support
or a separately deployed reconciliation schedule. Booking/rescheduling through
the bot immediately plans the appropriate new task.

## Verification and release

Run `go test ./...`, `go vet ./...`, `golangci-lint run ./...` and
`govulncheck ./...`. Cloud Build additionally runs race-enabled tests and the
vulnerability scan before creating a zero-traffic candidate and promoting it
after a version-matched health check. A red security scan now prevents promotion.

Tests cover multilingual navigation, pagination, stale buttons, coordinated
treatments, phone formats, private links, calendar cancellation/rescheduling,
UTF-8/ICS injection, Meta payload limits, template-only WhatsApp delivery,
consent withdrawal and live Altegio refresh failures. Local responsive visual QA
uses synthetic data via `CALENDAR_PREVIEW=1 go test ./internal/application/calendar
-run TestPreviewCalendar -v`; no customer booking or calendar write is performed.

Still required before claiming every channel production-ready: studio WhatsApp
number onboarding, approved reminder templates, Meta review/access validation,
and consented end-to-end real-device tests. Credentials alone do not prove this.
