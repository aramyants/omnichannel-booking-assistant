# WhatsApp continuity

The existing authenticated notification reconciliation job checks the studio
phone's messaging health and the configured utility templates every five minutes.
Checks also run on demand when the saved result is older than five minutes.
Firestore shares the confirmed restrictions across instances; probes are leased.

- `can_send_message=BLOCKED` pauses WhatsApp sends, read receipts and typing.
  Incoming messages are recorded and handed to staff before the model or booking
  tools run. New messages in a handover are forwarded to staff on all channels.
- A LIMITED phone with GREEN quality remains usable. SIP/calling restrictions
  do not disable messaging.
- A paused, rejected, missing or recategorized utility template is skipped for
  that name and language. RED quality pauses proactive templates. Normal replies
  to customers remain available unless messaging itself is blocked.
- Eligible native booking notices continue through Telegram and then SMS.
  Consent, phone verification and STOP rules still apply. Unknown acceptance of
  an earlier send does not permit another channel to send the same notice.
- A failed read preserves the last confirmed restriction. A successful health
  check can restore availability. Initial healthy state is silent; restrictions
  and recovery generate one staff alert per change. Alert delivery is reserved
  before sending, so uncertain acceptance does not produce repeated alerts.

Use Meta Business Support Home to inspect an actual account restriction. Do not
automatically re-register the number, replace the business account or retry
rejected broadcasts. These controls reduce avoidable sends and preserve business
continuity; they cannot guarantee that Meta will never restrict the account.

## Native Altegio SMS login

The messaging assistant's verified-phone cabinet and the native Altegio web
cabinet are separate entry points into the same live booking data. SMSGate is
commissioned for eligible notices and the assistant's phone verification.
Native Altegio login remains unavailable until Altegio activates the private SMS
provider and supplies its production send/activation contract and sender list.
Do not simulate native OTP delivery from a booking-created webhook.

Reference: [Altegio web cabinet requirements](https://alteg.io/en/support/knowledge-base/4903552434461-Personal-account-in-Online-booking-widget).
