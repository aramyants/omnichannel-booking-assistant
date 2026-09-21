package assistant

import (
	"context"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

type serviceCategory struct {
	Name     string `json:"name"`
	Services int    `json:"services"`
}

func categoriesOf(services []booking.Service) []serviceCategory {
	categories := make([]serviceCategory, 0)
	positions := make(map[string]int)
	for _, service := range services {
		name := strings.TrimSpace(service.Category)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if position, ok := positions[key]; ok {
			categories[position].Services++
		} else {
			positions[key] = len(categories)
			categories = append(categories, serviceCategory{Name: name, Services: 1})
		}
	}
	return categories
}

func (t *toolset) listCategories(ctx context.Context, s *session) (string, error) {
	services, err := t.scheduling.ListServices(ctx)
	if err != nil {
		return "", err
	}
	categories := categoriesOf(services)
	names := make([]string, 0, len(categories))
	for _, category := range categories {
		names = append(names, category.Name)
	}
	s.present(names...)
	return encode(map[string]any{"categories": categories,
		"instruction": "For a specific category request, map the customer's meaning to the actual category name (for example face massage / массаж лица / դեմքի մերսում to Face Motion), then call list_services with that exact category. Do not respond with every category when the customer already chose one. When offering categories, write only a short heading or question and return the relevant exact category labels as native choices; do not repeat those action labels as a numbered menu in the text. If ambiguous, ask which category they mean."})
}
