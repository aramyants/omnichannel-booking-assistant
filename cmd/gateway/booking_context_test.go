package main

import (
	"bytes"
	"context"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBookingContextRejectsExpiredUnsignedAndTamperedBodies(t *testing.T) {
	for _, mode := range []string{"unsigned", "expired", "tampered", "valid", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			s := &notifications.Service{Repo: memory.New()}
			handler := bookingContextHandler(s, "private-test-token", "https://book.motionconcept.rest/")
			raw := []byte(`{"action":"context","phone":"+37491123456","locale":"en","explicit":true}`)
			if mode == "trailing" {
				raw = append(raw, []byte(`{}`)...)
			}
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			if mode == "expired" {
				timestamp = strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10)
			}
			req := httptest.NewRequest(http.MethodPost, "/internal/booking-context", bytes.NewReader(raw))
			req.Header.Set("X-Emotion-Time", timestamp)
			signature := contextSignature("private-test-token", timestamp, raw)
			if mode == "tampered" {
				signature = strings.Repeat("0", 64)
			}
			if mode != "unsigned" {
				req.Header.Set("X-Emotion-Signature", signature)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			want := 401
			if mode == "valid" {
				want = 200
			}
			if mode == "trailing" {
				want = 400
			}
			if rec.Code != want {
				t.Fatal(mode, rec.Code, rec.Body.String())
			}
		})
	}
}

type contextRecordReader struct{ snapshot notifications.Snapshot }

func (reader contextRecordReader) ReadNativeBooking(context.Context, string) (notifications.Snapshot, error) {
	return reader.snapshot, nil
}

func TestWebManagementProofNeverCrossesVerifiedPhones(t *testing.T) {
	repo := memory.New()
	key := notifications.Key("record", "123")
	if err := repo.TransactNotifications(t.Context(), []string{key}, func(rows map[string]*notifications.Entry) error {
		rows[key].CalendarID, rows[key].CalendarPhone = "private-id", "+37491123456"
		rows[key].ManagementToken, rows[key].ManagementPhone = "private-proof", "+37491123456"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := webManagementProof(&notifications.Service{Repo: repo})
	for _, phone := range []string{"", "+37499123456", "+37491123456"} {
		id, proof, err := read(t.Context(), "123", phone)
		if err != nil {
			t.Fatal(err)
		}
		if phone == "+37491123456" {
			if id != "private-id" || proof != "private-proof" {
				t.Fatal("verified proof lost")
			}
		} else if id != "" || proof != "" {
			t.Fatal("proof crossed phone ownership")
		}
	}
}

func TestSignedReminderContextChecksCurrentRecipientAndStatus(t *testing.T) {
	for _, mode := range []string{"current", "reassigned", "cancelled", "unrelated_api"} {
		t.Run(mode, func(t *testing.T) {
			snap := notifications.Snapshot{Phone: "+37491123456", APIID: "emotion-web-v2-test", Booking: booking.Booking{Status: booking.StatusConfirmed, StartsAt: time.Now().Add(time.Hour)}}
			if mode == "reassigned" {
				snap.Phone = "+37499123456"
			}
			if mode == "cancelled" {
				snap.Booking.Status = booking.StatusCancelled
			}
			if mode == "unrelated_api" {
				snap.APIID = "another-integration"
			}
			s := &notifications.Service{Repo: memory.New(), Reader: contextRecordReader{snap}}
			raw := []byte(`{"action":"record-context","phone":"+37491123456","recordId":"123"}`)
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			req := httptest.NewRequest(http.MethodPost, "/internal/booking-context", bytes.NewReader(raw))
			req.Header.Set("X-Emotion-Time", ts)
			req.Header.Set("X-Emotion-Signature", contextSignature("test-token", ts, raw))
			rec := httptest.NewRecorder()
			bookingContextHandler(s, "test-token", "https://book.motionconcept.rest/").ServeHTTP(rec, req)
			want := `"eligible":false`
			if mode == "current" {
				want = `"eligible":true`
			}
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) || strings.Contains(rec.Body.String(), "374") {
				t.Fatal(rec.Code, rec.Body.String())
			}
		})
	}
}
