# Connect WhatsApp, Instagram and Messenger

Updated 11 September 2026. The integrations below are implemented; they need
deployment and channel credentials before they become live. Business
verification, permission approval and webhook subscriptions are separate steps.

## Callback URLs for this deployment

The origin below comes from Telegram's currently registered webhook. These Meta
routes become available after deploying with the matching channel credentials.

| Channel | Callback URL | Webhook object / field |
| --- | --- | --- |
| WhatsApp | `https://omnichannel-booking-assistant-q6l3x4lyuq-ew.a.run.app/webhooks/whatsapp` | `whatsapp_business_account` / `messages`, `smb_message_echoes` |
| Instagram | `https://omnichannel-booking-assistant-q6l3x4lyuq-ew.a.run.app/webhooks/instagram` | `instagram` / `messages` |
| Messenger | `https://omnichannel-booking-assistant-q6l3x4lyuq-ew.a.run.app/webhooks/messenger` | `page` / `messages` |

All three use `META_VERIFY_TOKEN` for the verification handshake. Generate that
value yourself, configure it in the service and enter the same value in Meta.
It is different from an access token and from the app secret. Incoming deliveries
are verified with the app secret; accounts are restricted to the configured IDs.

## 1. WhatsApp

First choose the appropriate onboarding path. The studio's existing number is
active in the WhatsApp Business app. To keep that app usable, use Embedded Signup
with **WhatsApp Business App Onboarding** (`featureType:
whatsapp_business_app_onboarding`), rather than standard new-number registration.
Meta's coexistence documentation requires a Solution Partner or Tech Provider,
session logging, and a working webhook. The current app is not yet approved for
production Embedded Signup. Do not delete the phone's WhatsApp account to work
around registration errors.

After successful coexistence signup, capture the **Phone Number ID** and **WABA
ID**; skip the ordinary phone-number registration call because the number is
already registered. Complete account subscription and the documented contact /
history synchronization process within 24 hours, respecting the business's
history-sharing choice.

Subscribe the webhook to `smb_message_echoes` as well as `messages`. Each echo
is a message a colleague sent from the WhatsApp Business app on the phone. The
service stores it in the transcript as an outbound message and hands the
conversation to that colleague, so the assistant stops answering until `/resume`
is sent from the staff chat. An assistant turn that was already running when the
echo arrived is dropped rather than sent. Echoes are deduplicated by message id,
so Meta redelivering one changes nothing, and an echo that arrives late for a
message sent before a `/resume` is kept as history without undoing the resume.
Media echoes are recorded as a note about the attachment; the file itself is not
fetched. Only `smb_message_echoes` has this effect: Cloud API `message_echoes`
and delivery `statuses` are ignored. The `history` and `smb_app_state_sync`
fields are not handled yet, so contact and chat history from the phone are not
imported.
[Meta's coexistence documentation](https://developers.facebook.com/documentation/business-messaging/whatsapp/embedded-signup/onboarding-business-app-users).

For a separate number that is not kept in the Business app, use standard Cloud API
registration and its two-step verification instead.

Create a system-user token with access to this app and WABA, using
`whatsapp_business_messaging` for messaging and `whatsapp_business_management` for
account management. Check expiration; do not use the temporary dashboard token
for an unattended service.

Set `WHATSAPP_ACCESS_TOKEN` and `WHATSAPP_PHONE_NUMBER_ID`. Configure the callback
above and subscribe to `messages`. Also subscribe the app to the WABA through
`POST /<WABA_ID>/subscribed_apps`; passing the callback verification alone does
not complete the account subscription. Check WhatsApp Manager's number, display
name and billing status. [Meta's WhatsApp Cloud API reference](https://www.postman.com/meta/whatsapp-business-platform/documentation/wlk6lh4/whatsapp-cloud-api).

## 2. Instagram

This implementation uses **Instagram API with Instagram Login**. Connect the
studio's professional account, enable access to messages, and authorize
`instagram_business_basic` and `instagram_business_manage_messages`. Store the
Instagram User access token as `INSTAGRAM_ACCESS_TOKEN` and the professional
account's numeric ID as `INSTAGRAM_ACCOUNT_ID`.

Configure the Instagram callback and the `messages` subscription in the app,
then subscribe the professional account to the app's webhooks through its
`/<IG_ACCOUNT_ID>/subscribed_apps` endpoint. Keep track of the token's expiry and
refresh it before it expires. This transport uses `graph.instagram.com` and must
not be supplied a token from the separate Facebook Login integration.

Check the access level required for the accounts this app serves: Standard Access
covers eligible accounts owned/managed and added to the app; access to accounts
outside that scope requires the applicable Advanced Access/App Review approval.
[Meta's Instagram API requirements](https://www.postman.com/meta/instagram/documentation/6yqw8pt/instagram-api?entity=request-23987686-23eacf45-3728-4e41-bcc7-6d164959327c).

## 3. Messenger

Connect the studio's Facebook Page in Messenger API Settings. Generate a Page
access token with `pages_messaging`, and configure `MESSENGER_ACCESS_TOKEN` and
`MESSENGER_PAGE_ID`. Configure the Page callback above, subscribe to `messages`,
and subscribe the app to the Page. Check the dashboard's applicable permission
access level, App Review and publication requirements before testing customers
without an app role. [Meta's Messenger setup](https://www.postman.com/meta/messenger-platform-api/documentation/iyp204x/messenger-platform-api).

## Shared configuration and activation

Set `META_APP_SECRET` to the signing app's secret and `META_VERIFY_TOKEN` to your
chosen verification secret. If Instagram or Messenger belongs to a different
signing app, use `INSTAGRAM_APP_SECRET` / `MESSENGER_APP_SECRET` as the respective
override. `META_GRAPH_VERSION` currently defaults to `v22.0`; configure a version
supported by your app. No channel activates without its own access token.

Use the existing [deployment guide](deployment.md). Tokens and app secrets go
through Secret Manager; account IDs are ordinary configuration. Do not paste
access tokens into chats or commit them to Git. After deployment, verify each
callback, complete its asset subscription and test from a designated account:
a greeting, category-specific prices, a voice question, and staff handover.
A full booking test creates an actual appointment unless Altegio is a test account.

## What customers can do

All channels share booking, guided category navigation, cancellation,
rescheduling and staff handover logic. Telegram uses inline keyboards;
Messenger and Instagram use quick replies; WhatsApp uses reply buttons or a
list. Numbered text remains the fallback. Audio is downloaded in memory and
transcribed with the configured OpenAI key. The transcript becomes ordinary
customer context; raw audio is not persisted and bot replies are always text.
Files over 20 MiB and Telegram recordings reported longer than five minutes
receive a resend/type prompt. Supported audio formats are checked first.

Telegram reminders are enabled. WhatsApp reminders require explicit customer
opt-in and an approved utility template configured for that exact language; the
bot never falls back to a free-form message outside the customer service window.
Messenger and Instagram proactive reminders are disabled. Staff follow-ups also
remain subject to their channel's messaging rules. See
[booking experience](booking-experience.md) for the six template parameters,
calendar fallback and operational limits.
[WhatsApp messaging policy](https://whatsappbusiness.com/policy/).

## Studio catalogue correction to review

The latest read-only catalogue check did not return Face Motion Gua Sha, so the
bot does not advertise or book it. Add and activate it in Altegio before relying
on the owner-provided 60-minute / 29,000 AMD description. The bot uses the live,
staff-filtered duration rather than guessing a replacement.
