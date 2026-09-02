package altegio_test

import (
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "time/tzdata"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/altegio"
)

// These tests run against a real Altegio account and are skipped unless
// ALTEGIO_LIVE is set, so an ordinary `go test ./...` never touches the network
// or a live business.
//
// They read nothing and change nothing: catalogue, staff and availability only.
// No booking is created.
//
// Run them when connecting a new location, which is when the response shapes
// this adapter assumes are most likely to differ:
//
//	ALTEGIO_LIVE=1 \
//	ALTEGIO_PARTNER_TOKEN=... ALTEGIO_USER_TOKEN=... ALTEGIO_COMPANY_ID=... \
//	go test ./internal/adapters/altegio/ -run Live -v
func liveClient(t *testing.T) *altegio.Client {
	t.Helper()

	if os.Getenv("ALTEGIO_LIVE") == "" {
		t.Skip("set ALTEGIO_LIVE=1 with real credentials to run these")
	}

	partner := os.Getenv("ALTEGIO_PARTNER_TOKEN")
	company := os.Getenv("ALTEGIO_COMPANY_ID")
	if partner == "" || company == "" {
		t.Fatal("ALTEGIO_PARTNER_TOKEN and ALTEGIO_COMPANY_ID are required")
	}

	location := time.UTC
	if name := os.Getenv("ALTEGIO_TIMEZONE"); name != "" {
		loaded, err := time.LoadLocation(name)
		if err != nil {
			t.Fatalf("ALTEGIO_TIMEZONE %q: %v", name, err)
		}
		location = loaded
	}

	client, err := altegio.NewClient(
		partner,
		os.Getenv("ALTEGIO_USER_TOKEN"),
		company,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		altegio.WithLocation(location),
		altegio.WithCurrency(os.Getenv("ALTEGIO_CURRENCY")),
	)
	if err != nil {
		t.Fatalf("NewClient() returned error: %v", err)
	}
	return client
}

func TestLiveCatalogue(t *testing.T) {
	client := liveClient(t)

	services, err := client.ListServices(t.Context())
	if err != nil {
		t.Fatalf("ListServices() returned error: %v", err)
	}
	if len(services) == 0 {
		t.Fatal("the account offers no bookable services")
	}

	for _, service := range services {
		t.Logf("service %s  %q  category=%q  duration=%s  price=%q",
			service.ID, service.Name, service.Category, service.Duration, service.PriceLabel())

		if service.ID == "" || service.Name == "" {
			t.Errorf("service decoded with an empty id or name: %+v", service)
		}
	}
}

func TestLiveStaff(t *testing.T) {
	client := liveClient(t)

	staff, err := client.ListStaff(t.Context())
	if err != nil {
		t.Fatalf("ListStaff() returned error: %v", err)
	}
	if len(staff) == 0 {
		t.Fatal("the account has nobody taking appointments")
	}

	for _, member := range staff {
		t.Logf("staff %s  %q  %q  bookable=%v",
			member.ID, member.Name, member.Specialisation, member.Bookable)

		if member.ID == "" || member.Name == "" {
			t.Errorf("staff decoded with an empty id or name: %+v", member)
		}
	}
}

// TestLiveAvailability walks the real calendar. It is the test most likely to
// catch a mapping difference, because slot times are where timezone handling
// and optional fields both show up.
func TestLiveAvailability(t *testing.T) {
	client := liveClient(t)

	staff, err := client.ListStaff(t.Context())
	if err != nil {
		t.Fatalf("ListStaff() returned error: %v", err)
	}

	var bookable string
	for _, member := range staff {
		if member.Bookable {
			bookable = member.ID
			break
		}
	}
	if bookable == "" {
		t.Skip("nobody at this location is currently bookable")
	}

	dates, err := client.AvailableDates(t.Context(), bookable)
	if err != nil {
		t.Fatalf("AvailableDates() returned error: %v", err)
	}
	t.Logf("staff %s has %d bookable dates", bookable, len(dates))

	if len(dates) == 0 {
		t.Skip("no free dates to read slots from")
	}

	slots, err := client.AvailableSlots(t.Context(), bookable, dates[0])
	if err != nil {
		t.Fatalf("AvailableSlots() returned error: %v", err)
	}
	t.Logf("%s has %d free slots", dates[0].Format("2006-01-02"), len(slots))

	for _, slot := range slots {
		if slot.Start.IsZero() {
			t.Error("a slot decoded with no start time")
		}
		// A zero offset on a business that is not in UTC means the timezone is
		// misconfigured, which would put every appointment at the wrong hour.
		t.Logf("  slot %s (%s)", slot.Start.Format(time.RFC3339), slot.Duration)
	}
}
