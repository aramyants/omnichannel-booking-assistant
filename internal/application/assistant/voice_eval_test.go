package assistant

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/ai/openai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/appointmentmessage"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

type voiceEvalProvider struct {
	ai.Provider
	failed bool
	usage  ai.Usage
}

func (p *voiceEvalProvider) Complete(ctx context.Context, req ai.Request) (ai.Response, error) {
	response, err := p.Provider.Complete(ctx, req)
	p.failed = p.failed || err != nil
	p.usage.InputTokens += response.Usage.InputTokens
	p.usage.OutputTokens += response.Usage.OutputTokens
	return response, err
}

// TestLiveArmenianVoiceEval is opt-in, uses synthetic messages and an in-memory
// calendar/inbox, and never sends to Telegram, WhatsApp, SMS or Altegio. Passing
// the lexical checks does not replace Armenian editorial review of the report.
func TestLiveArmenianVoiceEval(t *testing.T) {
	if os.Getenv("LIVE_VOICE_EVAL") != "1" {
		t.Skip("set LIVE_VOICE_EVAL=1 and OPENAI_API_KEY for the synthetic model evaluation")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Fatal("OPENAI_API_KEY is required")
	}
	models := strings.Split(os.Getenv("VOICE_EVAL_MODELS"), ",")
	if len(models) == 1 && models[0] == "" {
		models = []string{openai.DefaultModel}
	}
	cases := []struct{ id, text string }{
		{"formal_help", "Բարև, կարո՞ղ եք օգնել ինձ ընտրել ծառայություն։"},
		{"location_latin", "Dzer salone vortex e gtnvum?"},
		{"staff_qualifications", "Մասնագետների փորձառությունը քանի՞ տարի է, որտե՞ղ են սովորել։"},
		{"price_information", "Հանգստացնող մերսման արժեքը կգրե՞ք։ Դեռ չեմ ուզում ամրագրել։"},
		{"no_pressure", "Շնորհակալություն, կմտածեմ ու հետո կգրեմ։"},
		{"personal_feelings", "Ուրախացա, որ կարող եք օգնել ինձ։ Դուք էլ եք ուրախ ինձ համար՞"},
	}
	type result struct {
		Model      string   `json:"model"`
		Case       string   `json:"case"`
		Input      string   `json:"input"`
		Reply      string   `json:"reply"`
		Failed     bool     `json:"failed"`
		DurationMS int64    `json:"duration_ms"`
		Usage      ai.Usage `json:"usage"`
		Warnings   []string `json:"warnings"`
	}
	results := []result{}
	singular := regexp.MustCompile(`(?i)(^|[^\p{L}])(?:կարող եմ|խնդրում եմ|գործընկերս|գործընկերոջս|շնորհակալ եմ|համաձայն եմ|ուրախ եմ|կօգնեմ|ես|քեզ|քո|դու)(?:[^\p{L}]|$)`)
	for _, modelName := range models {
		for _, tc := range cases {
			t.Run(modelName+"/"+tc.id, func(t *testing.T) {
				client, err := openai.NewClient(key, openai.WithModel(strings.TrimSpace(modelName)))
				if err != nil {
					t.Fatal("model initialization failed")
				}
				model := &voiceEvalProvider{Provider: client}
				sender := &fakeSender{}
				calendar := defaultScheduling()
				calendar.services = []booking.Service{{ID: "1001", Name: "Motion Relax · 80 min", Category: "Motion Relax", Duration: 80 * time.Minute, PriceMin: 27000, PriceMax: 27000, Currency: "AMD"}}
				svc, _ := newAIServiceWithStaff(t, model, calendar, sender, &recordingStaff{})
				svc.business.Name = "E-Motion Concept"
				svc.tools.messages = appointmentmessage.New(appointmentmessage.Business{
					Address: appointmentmessage.LocalizedText{Armenian: "Երևան, Մյասնիկյան 1/6"},
					Phone:   "+37494768067",
				}, time.UTC)
				started := time.Now()
				err = svc.Handle(t.Context(), incomingText("synthetic-"+tc.id, tc.text))
				r := result{Model: modelName, Case: tc.id, Input: tc.text, Failed: err != nil || model.failed, DurationMS: time.Since(started).Milliseconds(), Usage: model.usage, Warnings: []string{}}
				if len(sender.sent) > 0 {
					r.Reply = sender.sent[len(sender.sent)-1].Text
				}
				if hearts.MatchString(r.Reply) {
					r.Warnings = append(r.Warnings, "heart")
				}
				if singular.MatchString(strings.ToLower(r.Reply)) {
					r.Warnings = append(r.Warnings, "possible_singular_or_familiar_voice")
				}
				if tc.id == "location_latin" && !strings.Contains(r.Reply, "1/6") {
					r.Warnings = append(r.Warnings, "missing_configured_address")
				}
				results = append(results, r)
				if r.Failed {
					t.Error("model or orchestration failed; inspect credential/model availability separately")
				}
				if len(r.Warnings) > 0 {
					t.Errorf("voice warnings: %v", r.Warnings)
				}
			})
		}
	}
	if path := os.Getenv("VOICE_EVAL_REPORT"); path != "" {
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfiguredVisitFactsReachModelBeforeBooking(t *testing.T) {
	model := &scriptedAI{responses: []ai.Response{textResponse("Երևան, Մյասնիկյան 1/6։")}}
	svc, _ := newAIService(t, model, defaultScheduling(), &fakeSender{})
	svc.tools.messages = appointmentmessage.New(appointmentmessage.Business{Address: appointmentmessage.LocalizedText{Armenian: "Երևան, Մյասնիկյան 1/6", English: "Yerevan, Myasnikyan 1/6"}, Phone: "+37494768067"}, time.UTC)
	if err := svc.Handle(t.Context(), incomingText("address-question", "Ձեր սրահը որտե՞ղ է գտնվում։")); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) == 0 || !strings.Contains(model.requests[0].Instructions, "Երևան, Մյասնիկյան 1/6") || !strings.Contains(model.requests[0].Instructions, "+37494768067") {
		t.Fatal("model had no configured studio contact facts")
	}
	if strings.Contains(model.requests[0].Instructions, "Yerevan, Myasnikyan") {
		t.Error("other-language address leaked into Armenian context")
	}
}
