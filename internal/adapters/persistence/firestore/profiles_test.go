package firestore

import (
	"fmt"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"testing"
	"time"
)

func TestVerifiedProfilesResolveAliasesAtomically(t *testing.T) {
	store := newStore(t)
	ctx := opCtx(t)
	external := unique(t, "profile")
	phone := fmt.Sprintf("+374%08d", time.Now().UnixNano()%100000000)
	var first customer.Customer
	for i, provider := range []messaging.Provider{messaging.ProviderTelegram, messaging.ProviderWhatsApp} {
		id := unique(t, "old")
		identity := customer.ChannelIdentity{CustomerID: id, Provider: provider, ExternalUserID: external}
		old, err := store.FindOrCreateByChannelIdentity(ctx, identity, customer.Customer{ID: id, Name: "Client", CreatedAt: testNow})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveBooking(ctx, booking.Booking{ID: unique(t, "booking"), ExternalID: unique(t, "record"), CustomerID: id}); err != nil {
			t.Fatal(err)
		}
		got, err := store.LinkVerifiedIdentity(ctx, identity, old, phone)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = got
		} else if first.ID != got.ID {
			t.Fatal("verified channels split")
		}
	}
	booked, err := store.ListBookings(ctx, first.ID)
	if err != nil || len(booked) != 2 {
		t.Fatalf("alias bookings=%d err=%v", len(booked), err)
	}
}
