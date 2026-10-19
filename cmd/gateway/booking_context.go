package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/calendar"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Domain-separated signing material never discloses an Altegio credential to a
// browser. This server-to-server bridge accepts fresh signed bodies only.
func contextSignature(userToken, timestamp string, body []byte) string {
	derive := hmac.New(sha256.New, []byte(userToken))
	derive.Write([]byte("emotion-ecosystem-auth-v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte(timestamp + "\nPOST\n/internal/booking-context\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
func bookingContextHandler(service *notifications.Service, token, base string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		timestamp := r.Header.Get("X-Emotion-Time")
		seconds, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil || token == "" || time.Since(time.Unix(seconds, 0)).Abs() > time.Minute {
			http.Error(w, "Unauthorized", 401)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(raw) > 4096 {
			http.Error(w, "Bad request", 400)
			return
		}
		expected := contextSignature(token, timestamp, raw)
		if !hmac.Equal([]byte(r.Header.Get("X-Emotion-Signature")), []byte(expected)) {
			http.Error(w, "Unauthorized", 401)
			return
		}
		var body struct {
			Action          string `json:"action"`
			Phone           string `json:"phone"`
			Locale          string `json:"locale"`
			Explicit        bool   `json:"explicit"`
			Consent         bool   `json:"consent"`
			RecordID        string `json:"recordId"`
			ManagementProof string `json:"managementProof"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "Bad request", 400)
			return
		}
		phone, err := customer.NormalizePhone(body.Phone)
		if err != nil {
			http.Error(w, "Bad request", 400)
			return
		}
		if body.Action != "context" && body.Action != "prepare" && body.Action != "confirmed" && body.Action != "record-context" {
			http.Error(w, "Bad request", 400)
			return
		}
		lang, err := service.ResolveLanguage(r.Context(), phone, body.Locale, body.Explicit)
		if err != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
		if body.Action == "prepare" {
			if !body.Consent {
				http.Error(w, "Consent required", 400)
				return
			}
			key := notifications.Key("web_phone", phone)
			err = service.Repo.TransactNotifications(r.Context(), []string{key}, func(rows map[string]*notifications.Entry) error {
				rows[key].Kind = "web_permission"
				rows[key].Contact = notifications.Contact{Phone: phone, Language: lang, RequestedAt: time.Now(), ExpiresAt: time.Now().Add(180 * 24 * time.Hour)}
				return nil
			})
		}
		output := map[string]any{"locale": lang}
		if body.Action == "confirmed" || body.Action == "record-context" {
			if _, parseErr := strconv.ParseUint(body.RecordID, 10, 64); parseErr != nil {
				http.Error(w, "Bad request", 400)
				return
			}
			snap, readErr := service.Reader.ReadNativeBooking(r.Context(), body.RecordID)
			if readErr != nil {
				http.Error(w, "Unavailable", 503)
				return
			}
			owned := strings.HasPrefix(snap.APIID, "emotion-web-v2-") || (body.Action == "record-context" && strings.HasPrefix(snap.APIID, "omnichannel-web-"))
			eligible := snap.Phone == phone && owned && snap.Booking.Status == booking.StatusConfirmed && snap.Booking.StartsAt.After(time.Now())
			if body.Action == "record-context" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"locale": lang, "eligible": eligible})
				return
			}
			if !eligible {
				http.Error(w, "Unavailable", 503)
				return
			}
			b, calendarErr := ensureNativeCalendar(r.Context(), service, body.RecordID, snap)
			if calendarErr != nil {
				http.Error(w, "Unavailable", 503)
				return
			}
			if body.ManagementProof != "" {
				reader, ok := service.Reader.(interface {
					ReadBooking(context.Context, booking.Booking) (booking.Booking, error)
				})
				if !ok || len(body.ManagementProof) > 256 {
					http.Error(w, "Unavailable", 503)
					return
				}
				b.ManagementToken = body.ManagementProof
				if _, proofErr := reader.ReadBooking(r.Context(), b); proofErr != nil {
					http.Error(w, "Unavailable", 503)
					return
				}
				key := notifications.Key("record", body.RecordID)
				if proofErr := service.Repo.TransactNotifications(r.Context(), []string{key}, func(rows map[string]*notifications.Entry) error {
					if rows[key].CalendarPhone != phone {
						return booking.ErrNotFound
					}
					rows[key].ManagementToken, rows[key].ManagementPhone = body.ManagementProof, phone
					return nil
				}); proofErr != nil {
					http.Error(w, "Unavailable", 503)
					return
				}
			}
			output["calendarUrl"] = calendar.Link(base, b, lang)
			eventID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("emotion-web-confirmation:"+body.RecordID)).String()
			err = service.Ingest(r.Context(), notifications.Event{ID: eventID, RecordID: body.RecordID, Status: "create"})
		}
		if err != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(output)
	})
}

// The caller supplies a phone proven by the channel, never a model argument.
func webManagementProof(s *notifications.Service) func(context.Context, string, string) (string, string, error) {
	return func(ctx context.Context, reference, phone string) (string, string, error) {
		if s == nil {
			return "", "", nil
		}
		row, err := s.Repo.GetNotification(ctx, notifications.Key("record", reference))
		if err != nil {
			return "", "", err
		}
		if phone == "" || row.ManagementPhone != phone || row.CalendarPhone != phone {
			return "", "", nil
		}
		return row.CalendarID, row.ManagementToken, nil
	}
}
func ensureNativeCalendar(ctx context.Context, s *notifications.Service, id string, snap notifications.Snapshot) (booking.Booking, error) {
	key := notifications.Key("record", id)
	b := snap.Booking
	err := s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*notifications.Entry) error {
		row := rows[key]
		if row.CalendarID == "" || row.CalendarPhone != snap.Phone {
			row.CalendarID = uuid.NewString()
			row.CalendarPhone = snap.Phone
		}
		b.ID = row.CalendarID
		return nil
	})
	return b, err
}
func nativeCalendarReader(s *notifications.Service) func(context.Context, string, string) (booking.Booking, error) {
	return func(ctx context.Context, id, key string) (booking.Booking, error) {
		row, err := s.Repo.GetNotification(ctx, notifications.Key("record", id))
		if err != nil {
			return booking.Booking{}, err
		}
		if row.CalendarID == "" {
			return booking.Booking{}, booking.ErrNotFound
		}
		if !calendar.ValidCapability(booking.Booking{ID: row.CalendarID, ExternalID: id}, key) {
			return booking.Booking{}, booking.ErrNotFound
		}
		snap, err := s.Reader.ReadNativeBooking(ctx, id)
		if err != nil {
			return booking.Booking{}, err
		}
		if snap.Phone != row.CalendarPhone {
			return booking.Booking{}, booking.ErrNotFound
		}
		b := snap.Booking
		b.ID = row.CalendarID
		if b.Duration <= 0 {
			return b, errors.New("invalid duration")
		}
		return b, nil
	}
}
