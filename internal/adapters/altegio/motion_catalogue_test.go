package altegio

import (
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

func TestNativeCatalogueEditsOverrideHistoricalServiceIdentity(t *testing.T) {
	service := booking.Service{ID: "13827004", Category: "Motion Relax", Name: "95 min", Description: "Current native description", Duration: 95 * time.Minute, PriceMin: 31000, PriceMax: 33000}
	got := motionCatalogueCopy(service)
	if got.Name != "Motion Relax · 95 min" || got.Description != service.Description || got.Duration != service.Duration || got.PriceMin != service.PriceMin || got.PriceMax != service.PriceMax {
		t.Fatalf("native catalog fields were overridden: %#v", got)
	}
	service.Name = "New native treatment"
	if got := motionCatalogueCopy(service); got.Name != "New native treatment" {
		t.Fatal("native rename was ignored", got.Name)
	}
}

func TestNativeCatalogueNeverInventsMissingContent(t *testing.T) {
	for _, id := range []string{"13815928", "13827244", "13827004"} {
		got := motionCatalogueCopy(booking.Service{ID: id, Category: "Face Motion"})
		if got.Name != "" || got.Description != "" || got.Duration != 0 {
			t.Fatal("historical service ID fabricated missing native data", got)
		}
	}
	got := motionCatalogueCopy(booking.Service{Name: "Motion Relax 80 min", Category: "Motion Relax"})
	if got.Name != "Motion Relax 80 min" {
		t.Fatal("native category was duplicated", got.Name)
	}
}
