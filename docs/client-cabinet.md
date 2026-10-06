# One client, all booking channels

The appointment list reads Altegio for the client's verified phone, including
bookings created through the online form or by staff. It combines that live
history with appointments owned by the linked chat profiles, preserves old
customer IDs as aliases, deduplicates provider references, and excludes cancelled
and past bookings. A failed, redacted or incomplete provider read is an error;
it never becomes a claim that no appointment exists.

| Channel | Proof used to link the client |
| --- | --- |
| WhatsApp | Phone address on the authenticated inbound webhook; an explicitly verified alternate phone can override it |
| Telegram bot | Own contact whose user ID matches the private sender |
| Telegram studio confirmations | Exact user peer returned by Telegram's phone resolution |
| Instagram / Facebook | Requested, single-use SMS code sent through the studio's SMSGate phone |

Typed phone numbers, display names and email addresses are booking contact data,
not ownership proof. Private history is never answered in Telegram groups.
Verification expires after 180 days. SMS codes expire in five minutes, allow
five attempts, are bound to the channel and account, and are stored only as an
HMAC. Per-account and per-phone send limits survive restarts and concurrent
requests. SMS sends are not retried after uncertain acceptance. Codes are
redacted before entering the transcript or model context.

The cabinet opens from “My appointments” or /appointments. Unlinked Telegram
clients get an own-contact button. Other unlinked clients provide their booking
phone and enter one SMS code; their appointments then open automatically.
/verify relinks a different booking phone; /verify_cancel stops verification.
Linking cabinet access does not enable notification subscriptions.

Existing bot-created bookings retain their private management proof and can be
changed after the shared confirmation step. Form/staff bookings without this
proof are visible, with changes routed to a colleague. No broader calendar
mutation rights are exposed to the model.

WhatsApp's Meta account is currently restricted. Native WhatsApp notices and
reminder templates stay paused across deployments; this cabinet release does not
re-register the number or re-enable WhatsApp sends. Telegram and SMS continue.
