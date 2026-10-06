package firestore

import (
	"fmt"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/cabinet"
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
	phone := fmt.Sprintf("+37491%06d", time.Now().UnixNano()%1000000)
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

func TestTelegramCabinetProofAndReceiptSurviveFirestore(t *testing.T) {
	store := newStore(t)
	ctx := opCtx(t)
	user := unique(t, "contact")
	identity := &cabinet.Identity{Repo: store}
	if err := identity.Begin(ctx, messaging.ProviderTelegram, user); err != nil {
		t.Fatal(err)
	}
	first, err := identity.AcceptTelegramContact(ctx, user, "+37491123456", "42")
	if err != nil || !first {
		t.Fatalf("first=%v err=%v", first, err)
	}
	first, err = identity.AcceptTelegramContact(ctx, user, "+37491123456", "42")
	if err != nil || first {
		t.Fatalf("retry first=%v err=%v", first, err)
	}
	if phone, err := identity.Phone(ctx, messaging.ProviderTelegram, user); err != nil || phone != "+37491123456" {
		t.Fatalf("persisted phone=%s err=%v", phone, err)
	}
}
