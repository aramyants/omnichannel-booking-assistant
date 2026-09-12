package assistant

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

func categoryCalendar() *stubScheduling {
	calendar := defaultScheduling()
	calendar.services = []booking.Service{
		{ID: "face", Name: "Face motion", Category: "Face Motion", PriceMin: 29000, PriceMax: 30000, Currency: "AMD"},
		{ID: "guasha", Name: "Face Motion Guasha", Category: "Face Motion", PriceMin: 29000, PriceMax: 29000, Currency: "AMD"},
		{ID: "sport", Name: "Motion sport", Category: "Motion Sport", PriceMin: 44000, PriceMax: 70000, Currency: "AMD"},
		{ID: "relax", Name: "Motion Relax", Category: "Motion Relax", PriceMin: 27000, PriceMax: 27000, Currency: "AMD"},
	}
	return calendar
}

func TestCategoryRequestsExcludeUnrelatedServicesAndButtons(t *testing.T) {
	for _, category := range []string{"Face Motion", " face motion "} {
		t.Run(category, func(t *testing.T) {
			sender := &fakeSender{}
			args, _ := json.Marshal(map[string]string{"category": category})
			model := &scriptedAI{responses: []ai.Response{
				toolResponse("categories", toolListCategories, `{}`),
				toolResponse("services", toolListServices, string(args)),
				{Text: "Для лица: Face motion — 29 000–30 000 AMD; Face Motion Guasha — 29 000 AMD. Какой вариант вам подходит?", Choices: []string{"Face motion", "Face Motion Guasha", "Motion sport"}},
			}}
			svc, _ := newAIService(t, model, categoryCalendar(), sender)
			if err := svc.Handle(t.Context(), incomingText("category", "Какие массажи лица есть?")); err != nil {
				t.Fatal(err)
			}
			result := resultOf(t, model, 2)
			if strings.Contains(result, "Motion sport") || strings.Contains(result, "Motion Relax") || !strings.Contains(result, "Face Motion Guasha") {
				t.Fatalf("wrong category results: %s", result)
			}
			if got := labelsOf(sender.sent[0].Choices); !slices.Equal(got, []string{"Face motion", "Face Motion Guasha"}) {
				t.Fatalf("unrelated buttons: %v", got)
			}
		})
	}
}

func TestUnknownCategoryNeverFallsBackToFullCatalogue(t *testing.T) {
	model := &scriptedAI{responses: []ai.Response{
		toolResponse("services", toolListServices, `{"category":"missing category"}`),
		textResponse("Which type of massage do you mean?"),
	}}
	svc, _ := newAIService(t, model, categoryCalendar(), &fakeSender{})
	if err := svc.Handle(t.Context(), incoming("unknown-category")); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Services   []any             `json:"services"`
		Categories []serviceCategory `json:"categories"`
	}
	if err := json.Unmarshal([]byte(resultOf(t, model, 1)), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Services) != 0 || len(result.Categories) != 3 {
		t.Fatalf("not a scoped clarification: %+v", result)
	}
}
