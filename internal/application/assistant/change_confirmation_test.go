package assistant

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestChangeConfirmationAcrossChannelsAndLanguages(t *testing.T) {
	providers := []messaging.Provider{messaging.ProviderTelegram, messaging.ProviderWhatsApp, messaging.ProviderMessenger, messaging.ProviderInstagram}
	for _, provider := range providers {
		for _, lang := range languages {
			for _, kind := range []booking.ChangeKind{booking.ChangeCancel, booking.ChangeReschedule} {
				t.Run(fmt.Sprintf("%s/%s/%s", provider, lang, kind), func(t *testing.T) {
					prepare := prepareCancellationCall("prepare")
					if kind == booking.ChangeReschedule {
						prepare = prepareRescheduleCall("prepare")
					}
					// Reproduces the model's old behavior if it ever sees the yes:
					// it prepares the same change again instead of confirming it.
					model := &scriptedAI{responses: []ai.Response{prepare, textResponse("Confirm this appointment change?"), prepare}}
					sender := &fakeSender{}
					scheduling := defaultScheduling()
					svc, store := newAIService(t, model, scheduling, sender)
					seedOwnedBooking(t, store)
					svc.senders[provider] = sender
					if _, err := store.FindOrCreateByChannelIdentity(t.Context(), customer.ChannelIdentity{
						ID: "channel-identity", CustomerID: "cust-1", Provider: provider,
						ExternalUserID: "219847362", CreatedAt: testNow,
					}, customer.Customer{ID: "cust-1", Name: "Anna", CreatedAt: testNow, UpdatedAt: testNow}); err != nil {
						t.Fatal(err)
					}
					msg := incomingText("prepare", "Please change my appointment")
					msg.Provider, msg.Sender.Language = provider, string(lang)
					if err := svc.Handle(t.Context(), msg); err != nil {
						t.Fatal(err)
					}
					if len(scheduling.cancelled)+len(scheduling.moved) != 0 {
						t.Fatal("preparation changed the appointment")
					}
					msg.ExternalMessageID, msg.Content.Text = "consent", speak(lang).confirmChange
					if err := svc.Handle(t.Context(), msg); err != nil {
						t.Fatal(err)
					}
					if model.calls != 2 {
						t.Fatalf("consent was sent back to the model: %d calls", model.calls)
					}
					if len(scheduling.cancelled)+len(scheduling.moved) != 1 {
						t.Fatal("one consent did not cause exactly one change")
					}
					conv, err := store.FindOrOpen(t.Context(), conversation.Conversation{ID: "unused", Provider: provider, ExternalThreadID: msg.ExternalThreadID})
					if err != nil || conv.BookingChange != nil {
						t.Fatalf("pending change after success: %+v, %v", conv.BookingChange, err)
					}
					booked, err := store.ListBookings(t.Context(), "cust-1")
					if err != nil || len(booked) != 1 {
						t.Fatal("appointment not persisted")
					}
					if kind == booking.ChangeCancel && booked[0].Status != booking.StatusCancelled {
						t.Fatal("cancellation not persisted")
					}
					if kind == booking.ChangeReschedule && !booked[0].StartsAt.Equal(appointmentAt(3)) {
						t.Fatal("reschedule not persisted")
					}
					if len(sender.sent) != 2 || !strings.Contains(sender.sent[1].Text, "998877") {
						t.Fatalf("missing factual change confirmation: %+v", sender.sent)
					}
					// Redelivery and a second click must not resurrect the draft.
					if err := svc.Handle(t.Context(), msg); err != nil {
						t.Fatal(err)
					}
					msg.ExternalMessageID = "second-consent"
					if err := svc.Handle(t.Context(), msg); err != nil {
						t.Fatal(err)
					}
					if model.calls != 2 || len(scheduling.cancelled)+len(scheduling.moved) != 1 {
						t.Fatal("a repeated consent restarted or repeated the change")
					}
				})
			}
		}
	}
}

func TestPreparingSameChangePreservesEarlierConsentBoundary(t *testing.T) {
	for _, kind := range []booking.ChangeKind{booking.ChangeCancel, booking.ChangeReschedule} {
		t.Run(string(kind), func(t *testing.T) {
			prepare, confirm := prepareCancellationCall("prepare"), toolConfirmCancel
			if kind == booking.ChangeReschedule {
				prepare, confirm = prepareRescheduleCall("prepare"), toolConfirmMove
			}
			model := &scriptedAI{responses: []ai.Response{prepare, textResponse("Please confirm."), prepare, toolResponse("confirm", confirm, `{}`)}}
			scheduling := defaultScheduling()
			svc, store := newAIService(t, model, scheduling, &fakeSender{})
			seedOwnedBooking(t, store)
			if err := svc.Handle(t.Context(), incomingText("prepare", "Change my appointment")); err != nil {
				t.Fatal(err)
			}
			// A natural phrase goes through the model; even a repeated prepare
			// must retain the original turn boundary so confirmation can succeed.
			if err := svc.Handle(t.Context(), incomingText("consent", "Please proceed with that appointment change")); err != nil {
				t.Fatal(err)
			}
			if len(scheduling.cancelled)+len(scheduling.moved) != 1 || openConversation(t, store).BookingChange != nil {
				t.Fatal("repeated preparation prevented confirmation")
			}
			if !strings.Contains(resultOf(t, model, 3), `"already_prepared":true`) {
				t.Fatal("repeated preparation was not identified")
			}
		})
	}
}

func TestChangeDeclineAndExpiredConsentDoNotMutate(t *testing.T) {
	for _, lang := range languages {
		for _, expired := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/expired=%t", lang, expired), func(t *testing.T) {
				model := &scriptedAI{responses: []ai.Response{prepareCancellationCall("prepare"), textResponse("Please confirm.")}}
				scheduling := defaultScheduling()
				sender := &fakeSender{}
				svc, store := newAIService(t, model, scheduling, sender)
				seedOwnedBooking(t, store)
				msg := incomingText("prepare", "Cancel the appointment")
				msg.Sender.Language = string(lang)
				if err := svc.Handle(t.Context(), msg); err != nil {
					t.Fatal(err)
				}
				msg.ExternalMessageID, msg.Content.Text = "answer", speak(lang).leaveItAlone
				if expired {
					conv := openConversation(t, store)
					conv.BookingChange.PreparedAt = testNow.Add(-2 * time.Hour)
					if err := store.Save(t.Context(), conv); err != nil {
						t.Fatal(err)
					}
					msg.Content.Text = speak(lang).confirmChange
				}
				if err := svc.Handle(t.Context(), msg); err != nil {
					t.Fatal(err)
				}
				if len(scheduling.cancelled) != 0 || model.calls != 2 || openConversation(t, store).BookingChange != nil {
					t.Fatal("declined or expired consent changed or restarted the appointment")
				}
			})
		}
	}
}

func TestConditionalChangeAnswerIsNotAutomaticConsent(t *testing.T) {
	for _, text := range []string{"Yes, but move it to tomorrow", "No, actually cancel another booking", "Այո, բայց ուրիշ ժամի", "Да, но на другое время", "yes?"} {
		if changeAnswer(text) != 0 {
			t.Errorf("%q was treated as an unqualified answer", text)
		}
	}
}

func TestUncertainConfirmedChangeCannotLoopOrRetryInSameTurn(t *testing.T) {
	scheduling := defaultScheduling()
	scheduling.cancelErr = booking.ErrOutcomeUnknown
	model := &scriptedAI{responses: []ai.Response{
		prepareCancellationCall("prepare"), textResponse("Please confirm."),
		prepareCancellationCall("reprepare"), toolResponse("retry", toolConfirmCancel, `{}`),
		textResponse("Our team will check the appointment status."),
	}}
	svc, store := newAIService(t, model, scheduling, &fakeSender{})
	seedOwnedBooking(t, store)
	if err := svc.Handle(t.Context(), incomingText("prepare", "Cancel my appointment")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Handle(t.Context(), incomingText("consent", speak(languageArmenian).confirmChange)); err != nil {
		t.Fatal(err)
	}
	if len(scheduling.cancelled) != 1 {
		t.Fatal("an uncertain provider result was retried")
	}
	conv := openConversation(t, store)
	if conv.State != conversation.StateHumanRequested || conv.BookingChange.PreparedFromMessageID != "prepare" {
		t.Fatal("uncertain change did not retain its original boundary for staff reconciliation")
	}
	for _, tool := range model.requests[2].Tools {
		if tool.Name == toolPrepareCancel || tool.Name == toolConfirmCancel || tool.Name == toolPrepareMove || tool.Name == toolConfirmMove {
			t.Fatal("a mutation was offered after confirmation had already been attempted")
		}
	}
}
