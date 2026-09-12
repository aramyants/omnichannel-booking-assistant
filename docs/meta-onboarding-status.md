# Meta setup status — 12 September 2026

This records dashboard observations, not a completed activation. The earlier bot
changes are now committed as `042ffef` on `fix/localised-menu-and-live-appointments`
(the local branch is in sync with its remote). A production deployment has not
been verified during this Meta setup session.

## Confirmed assets

| Asset | ID / status |
| --- | --- |
| E-motion Concept Massage Studio business portfolio | `1053050323780744`; business verified 7 September 2026 |
| Motion Concept AI Bot chat app | `3531775307007016`; unpublished |
| WhatsApp Business app account | `1522559759898453`; verified, approved; studio number ending `8067` connected to the Business app |
| Separate WhatsApp API account | `1425605796099324`; verified, approved; no phone numbers |
| Embedded Signup login configuration | `38131942366454357` |
| Free WhatsApp Cloud API test account | `1720864419030467` |
| Free test number | `+1 555-195-8338`; phone ID `1350823718109876` |

The WhatsApp production callback URL and verify-token fields are empty. The
Embedded Signup Builder states that App Review and Access Verification are not
completed and this app cannot yet be used in production for Embedded Signup.

## Work performed

- Saved the app category as **Messenger bots for business**.
- Submitted the public studio contact email, `emotionconceptstudio@gmail.com`,
  from the studio website in app settings.
- Selected Embedded Signup **v4**, session information version **3**, feature
  type **WhatsApp Business App Onboarding** (previously None).
- Opened that flow and, after the user's approval to continue, accepted the
  presented Meta onboarding terms. Those terms included event-activity sharing
  for marketing optimization.
- Entered the existing studio number with Armenia's `+374` calling code.
  Meta rejected it before phone verification or account selection.
- The user confirmed disconnecting the Inbox in Meta Business Suite platform
  connection on their phone. No WhatsApp account was deleted or successfully
  onboarded to the bot.
  No customer message was sent during that production onboarding attempt.

## WhatsApp blocker progression

Meta displayed: **This phone number is not eligible to register or migrate.**

- Error code: `3441034`
- Onboarding session: `01a085ba-373b-7f05-b33a-44350cfcc606`
- Feature: `whatsapp_business_app_onboarding`
- Phone number entry was visually checked after rejection.

The error alone does not identify its cause. The user then supplied a phone
screenshot of Settings > Account > Business Platform showing **Inbox in Meta
Business Suite** connected to this studio. The phone states that only one Business
Platform connection is allowed at a time, and that disconnecting this connection
removes access to Business Suite Inbox features but leaves the WhatsApp Business
app unaffected. This establishes an existing platform connection despite the
desktop inbox showing Get started. The user confirmed disconnecting that specific
connection on the phone. We retried Next once, and the number-eligibility error was
replaced by a specific app-permission error. Do not delete the WhatsApp account
or remove the WABA from the business portfolio.

Current error: **Partner app lacks required advanced WhatsApp Business Management
and messaging permissions for onboarding. Request these permissions through the
app review process.** Code `2655111`, same onboarding session as above.

App Review draft `3531840873667126` already includes
`whatsapp_business_messaging` and `whatsapp_business_management`; neither has
been submitted/approved in this flow. Opened the preparation checklist and entered
factual intended-use descriptions for the studio's own account. The checklist
initially showed Verification 100%, App settings 50%, Allowed usage 0%, Data handling 0%,
Reviewer instructions 0%. This review checklist's verification progress does not
establish Tech Provider/Embedded Signup production approval. Required evidence
includes a working end-to-end screencast and API test calls. No submission or
allowed-usage certification was made. The draft also contains other channel and
marketing/profile permissions; audit actual use before submitting those scopes.

Saved a detailed WhatsApp messaging rationale as a partial draft, explicitly
describing intended development work, text replies, audio transcription, and
coexistence. Did not use Meta's generated AI suggestion, which asserted completed
compliance work and opt-out instructions without evidence. Left the allowed-usage
agreement unchecked. The messaging form reports **0 of 1 API calls required**;
Meta says test-call status can take up to 24 hours to appear. No screencast exists
for submission yet.

## Website publication completed

The user supplied `C:\Users\aram\Local Sites\emotion-concept`, confirmed Vercel
automatically deploys GitHub `main`, and explicitly requested a push. Added public
server-rendered/prerendered privacy, data-deletion and assistant-terms pages with
links on all three locale homepages and the sitemap. The English pages declare
English document language. Copy reflects observed bot data flows, OpenAI voice
transcription, Altegio, Google Cloud, Vercel and Telegram staff handoff; deletion
requests go to the existing public studio email rather than an unimplemented form.

- Repository: `aramyants/emotion-body-balance`
- Commit: `a6a7b4f` on `main`
- GitHub's Vercel status: success, "Deployment has completed"
- Verified in Chrome at the actual production domain:
  - <https://www.motionconcept.rest/privacy>
  - <https://www.motionconcept.rest/data-deletion>
  - <https://www.motionconcept.rest/terms>
- Saved all three URLs in Meta Basic settings and verified persistence after
  reloading. The missing-privacy warning cleared. The icon was subsequently saved
  as described below.
- Validation: `npm run check` passed (8 pre-existing Fast Refresh warnings,
  no errors), 6 pages prerendered, local HTTP/HTML/footer/sitemap checks passed,
  and desktop visual inspection passed. Fixed the formatting command's unmatched
  `public/*.json` pattern and existing formatting failures to make the check pass.

The user supplied `e-motion_logo.jpg` for the Meta icon. Chrome chooser upload
failed with "Not allowed"; the user then uploaded it manually. We verified the
logo thumbnail and saved Basic settings. Meta displayed **Changes saved**.
The App Review checklist now shows **App settings 100%** (Verification 100%).
The upload restriction was not bypassed.

Added the Website platform with `https://www.motionconcept.rest/` as Site URL.
Saved public-site testing instructions that explicitly distinguish the marketing
website from the still-unconnected WhatsApp integration. These instructions are
not a replacement for a functioning Meta-channel demo. The draft's app-settings
progress moved to 75% after the public URLs were supplied.

Data-handling draft: disclosed that processors are used, entered the dashboard's
known controller name `Motion Concept Studio (E-motion Concept Massage Studio)`
and country Armenia. Meta requires each processor's service category and **all
processing countries, including remote access**. Left those locations unfilled
rather than inventing them from vendor headquarters or a single cloud region.
Asked the user for the mandatory national-security-request history and existing
government-data-request processes. Those answers remain pending. No compliance
certification or review submission was made.

Implementation/review alignment still needs attention: the local Instagram
transport uses Instagram Login (`instagram_business_basic` and
`instagram_business_manage_messages`). On 11 September the review request was
corrected to those Instagram Login scopes and the Facebook Login variants were
removed. Unused requests for gender, locale, timezone, utility/marketing
messaging, paid marketing, ads management, email, Page engagement reading,
Business Asset User Profile Access and the unimplemented Human Agent extension
were removed. The Messenger request retains `pages_messaging` plus the dashboard
dependencies `pages_show_list` and `pages_manage_metadata`. `business_management`
is retained for the Embedded Signup/portfolio onboarding flow. `public_profile`
is automatically granted by Meta.

After the user's approval, claimed the free WhatsApp test number listed above.
On 11 September a test token became available in Meta's Try it out dashboard.
The authorization flow had been restricted to the current test WABA instead of
all current and future WhatsApp accounts. Do not record token values in this
document or source control.

Completed a read-only Graph API Explorer call with that token:
`GET /v26.0/1720864419030467/phone_numbers`. Meta returned the test number,
phone ID `1350823718109876`, verified name `Test Number`, and platform type
`CLOUD_API` (response received in 1044 ms). This is a successful account-management
test, not evidence that the studio's production number is connected.

Added studio number `+374 94 768067` as the authorized test recipient. Meta
initially displayed **Code sent successfully** and requested a five-digit code
over WhatsApp. On resuming later on 11 September, the studio number was listed
as a verified recipient and Meta marked **Send a message from your test number**
as **Completed**. The message ID, template and delivery receipt were not observed
in that resumed session; do not invent them or send a duplicate just for evidence.

Saved a partial management-permission rationale for own-WABA/phone identification
and webhook account subscription. Both WhatsApp permission forms last showed
0 of 1 required API test calls before the successful management call; counters
can take up to 24 hours to update. Neither has a screencast or allowed-usage
certification attached. Review cannot be submitted yet.

Meta's official documentation requires a Solution Partner or Tech Provider for
coexistence. The app's own access-verification and review gates remain incomplete.
An approved provider may be necessary; do not promise that removing an old link
or passing business verification alone resolves this number's eligibility.

## App publication gaps

- App icon has been uploaded by the user and saved in Meta.
- Privacy, terms and data-deletion URLs are now live and saved (see above).
- Access-verification requirements remain on the publish page.
- Channel credentials, webhook verification/subscriptions, and a deployment
  remain necessary before any Meta channel is operational.

Studio website: <https://www.motionconcept.rest/>.

## Draft for Meta support (not sent)

We are connecting our existing WhatsApp Business app number ending 8067 to Cloud
API using coexistence, while retaining the phone app. Our business portfolio is
1053050323780744, developer app 3531775307007016, and login configuration
38131942366454357. Business verification is complete. The number belongs to
Business app WABA 1522559759898453, while WABA 1425605796099324 has no numbers.

Embedded Signup v4 with sessionInfoVersion 3 and featureType
whatsapp_business_app_onboarding rejects the correctly entered number with error
3441034 before verification: "This phone number is not eligible to register or
migrate." Session ID: 01a085ba-373b-7f05-b33a-44350cfcc606.

After the user disconnected the Inbox in Meta Business Suite connection, retrying
replaced error 3441034 with error 2655111: advanced WhatsApp Business Management
and messaging permissions are required through App Review. Both permissions are
in an unsubmitted review draft.

Please identify any remaining eligibility restriction and the supported coexistence path
for this account, including any provider/access-verification requirement. We
need to preserve the WhatsApp Business app account and chats and have not
deleted it. Only the Business Suite Inbox platform connection was disconnected.
Please distinguish any existing-link restriction
from app eligibility or number eligibility.

Meta's documented support topic is **WABiz: Onboarding / TechProvider:
Onboarding**, request type **Embedded Signup - Coexistence Onboarding**.

Source: [Meta coexistence documentation](https://developers.facebook.com/documentation/business-messaging/whatsapp/embedded-signup/onboarding-business-app-users).
