package altegio

import (
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

func TestMotionCatalogueUsesOwnerCopyOnlyAsAnEmptyDescriptionFallback(t *testing.T) {
	service := motionCatalogueCopy(booking.Service{ID: "13827004", Category: "Motion Relax"})
	if service.Name != "Motion Relax · 80 min" {
		t.Fatalf("name = %q", service.Name)
	}
	for _, text := range []string{
		"Calm, focused bodywork.",
		"Спокойная работа с основными зонами напряжения.",
		"Հանգիստ տեմպով",
	} {
		if !strings.Contains(service.Description, text) {
			t.Fatalf("owner description missing %q from %q", text, service.Description)
		}
	}

	live := motionCatalogueCopy(booking.Service{
		ID:          "13827004",
		Category:    "Motion Relax",
		Description: "Current Altegio description",
	})
	if live.Description != "Current Altegio description" {
		t.Fatalf("live Altegio description was overwritten: %q", live.Description)
	}
}

func TestMotionCatalogueAliasRequiresBothServiceIDAndCategory(t *testing.T) {
	service := motionCatalogueCopy(booking.Service{
		ID:       "13827244",
		Name:     "Unrelated live service",
		Category: "Other",
	})
	if service.Name != "Unrelated live service" {
		t.Fatalf("service with reused id but different category was renamed: %q", service.Name)
	}
}

func TestFaceMotionServicesHaveCustomerFacingDescriptions(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want string
	}{
		{id: "13815928", want: "classic manual massage techniques"},
		{id: "13827244", want: "Gua Sha tools"},
	} {
		service := motionCatalogueCopy(booking.Service{ID: tc.id, Category: "Face Motion"})
		if !strings.Contains(service.Description, tc.want) || strings.Count(service.Description, "\n") != 2 {
			t.Fatalf("service %s description = %q", tc.id, service.Description)
		}
	}
}
