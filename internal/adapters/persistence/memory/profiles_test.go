package memory

import (
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"testing"
	"time"
)

func TestVerifiedProfilePreservesOldBookingsWithoutMergingNames(t *testing.T) {
	store := New()
	create := func(provider messaging.Provider, user, id string) (customer.ChannelIdentity, customer.Customer) {
		identity := customer.ChannelIdentity{CustomerID: id, Provider: provider, ExternalUserID: user}
		cust, err := store.FindOrCreateByChannelIdentity(t.Context(), identity, customer.Customer{ID: id, Name: "Same name", CreatedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveBooking(t.Context(), booking.Booking{ID: "booking-" + id, ExternalID: "record-" + id, CustomerID: id}); err != nil {
			t.Fatal(err)
		}
		return identity, cust
	}
	tg, tgCustomer := create(messaging.ProviderTelegram, "123", "old-tg")
	wa, waCustomer := create(messaging.ProviderWhatsApp, "37491123456", "old-wa")
	other, _ := create(messaging.ProviderInstagram, "456", "other")
	linked, err := store.LinkVerifiedIdentity(t.Context(), tg, tgCustomer, "+37491123456")
	if err != nil {
		t.Fatal(err)
	}
	linked2, err := store.LinkVerifiedIdentity(t.Context(), wa, waCustomer, "+37491123456")
	if err != nil {
		t.Fatal(err)
	}
	if linked.ID != linked2.ID {
		t.Fatal("matching verified phones not unified")
	}
	booked, err := store.ListBookings(t.Context(), linked.ID)
	if err != nil || len(booked) != 2 {
		t.Fatal("pre-link bookings were lost")
	}
	unlinked, err := store.FindOrCreateByChannelIdentity(t.Context(), other, customer.Customer{})
	if err != nil || unlinked.ID == linked.ID {
		t.Fatal("a matching name granted profile access")
	}
	moved, err := store.LinkVerifiedIdentity(t.Context(), tg, linked, "+37499123456")
	if err != nil {
		t.Fatal(err)
	}
	booked, err = store.ListBookings(t.Context(), moved.ID)
	if err != nil || len(booked) != 0 {
		t.Fatal("previous phone profile leaked after relink")
	}
}
