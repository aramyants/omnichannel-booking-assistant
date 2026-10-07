# Bot quality and Armenian voice

The [7 October reliability audit](reliability-audit-2026-10-07.md) documents the
active scope classifier, structured reply purpose, output/tool budgets, token
health guards and verified appointment lookup fixes. These controls supplement
the voice policy below. The booking model keeps its 8,192-token per-call ceiling,
with a 16,000-token total across a reply's tool loop.

The assistant speaks for the studio team, addresses clients formally and answers
their information request before offering a relevant next step. Garik's policy
is no hearts, no familiar address, no individual first-person self-description,
and no aggressive selling. Client-owned button labels such as “my appointments”
remain in the client's voice.

The prompt and phrasebook establish these rules. A deterministic guard also
removes hearts before both sending and transcript storage, preserving prices,
phone numbers, booking details and paragraphs. A heart-only model reply uses the
existing retry/staff-choice fallback; it does not silently mute the assistant.

Configured studio visit facts are reused from the appointment-message renderer.
An address question should not require a booking or a staff handoff. Staff
experience, qualifications, training and discount conditions remain unknown
unless supplied by verified studio sources. Do not use scheduling eligibility
as evidence of professional qualifications or promise staff reply times.

## Evaluation

Normal tests make no paid AI requests. To run the opt-in, synthetic Armenian
evaluation with an API key supplied securely in the environment:

```powershell
$env:LIVE_VOICE_EVAL = '1'
$env:VOICE_EVAL_MODELS = 'gpt-6.1-sol'
$env:VOICE_EVAL_REPORT = 'C:/private-work/voice-eval.json'
go test ./internal/application/assistant -run '^TestLiveArmenianVoiceEval$' -count=1 -timeout=8m -v
```

The evaluation uses an in-memory inbox, staff notifier and calendar. It never
sends a customer message or creates a real appointment. It covers formal help,
Latin-script Armenian location questions, missing staff qualifications,
price-only requests, ending a conversation without sales pressure, and requests
for personal emotional responses. Token usage and elapsed time are reported.
Lexical warnings are screening aids, not a complete Armenian grammar grader.

Review replies for natural Eastern Armenian, formal verbs, team voice, concise
answers, correct source use, appropriate handoff and no repeated booking
pressure. Expand the set with anonymized failure cases from real conversations,
with held-out cases and booking/tool safety checks before changing models.
Never commit raw client transcripts or credentials to this public repository.

The business requested an upgrade from GPT-5.6 Luna to GPT-6.1 Sol. The new
configuration explicitly uses high reasoning and an 8192-token budget shared
by reasoning and the concise customer reply. Opaque encrypted reasoning and
assistant phase are replayed in order during tool calls, held only in memory
for that reply, and never shown to clients or saved to their transcript.

The expanded synthetic evaluation covers 13 Armenian cases, including a
conversation correction, transliteration, surname questions, reschedule safety
and a request for a human. A focused check supplies all seven live categories
and requires every category in the rendered choices. Review replies alongside
the lexical screens; these are not a complete Armenian grammar grade.
The stronger model costs more than the previous Luna model and can take longer.
It still cannot supply missing biographies or studio information.

References: [OpenAI evaluation guidance](https://developers.openai.com/api/docs/guides/evaluation-best-practices),
[model selection](https://developers.openai.com/api/docs/guides/model-selection).
