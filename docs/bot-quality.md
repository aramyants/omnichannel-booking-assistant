# Bot quality and Armenian voice

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
$env:VOICE_EVAL_MODELS = 'gpt-5.6-luna,gpt-6-luna'
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

The initial small comparison did not establish a quality advantage for the
newer Luna model. Keep the deployed model until a larger review demonstrates
an improvement within the studio's reply-time and cost requirements. A model
upgrade alone cannot supply missing staff biographies or studio information.

References: [OpenAI evaluation guidance](https://developers.openai.com/api/docs/guides/evaluation-best-practices),
[model selection](https://developers.openai.com/api/docs/guides/model-selection).
