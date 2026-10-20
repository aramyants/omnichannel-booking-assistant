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
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/benefit"
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
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(raw) > 4096 {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		expected := contextSignature(token, timestamp, raw)
		if !hmac.Equal([]byte(r.Header.Get("X-Emotion-Signature")), []byte(expected)) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
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
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		if body.Action == "certificate-types" {
			reader, ok := service.Reader.(interface {
				ListCertificateTypes(context.Context) ([]benefit.CertificateType, error)
			})
			if !ok {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			values, readErr := reader.ListCertificateTypes(r.Context())
			if readErr != nil {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			lang := body.Locale
			if lang != "en" && lang != "hy" {
				lang = "ru"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"locale": lang, "certificates": values})
			return
		}
		phone, err := customer.NormalizePhone(body.Phone)
		if err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		if body.Action != "context" && body.Action != "prepare" && body.Action != "confirmed" && body.Action != "record-context" && body.Action != "review-visits" && body.Action != "review-check" && body.Action != "client-certificates" {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		lang, err := service.ResolveLanguage(r.Context(), phone, body.Locale, body.Explicit)
		if err != nil {
			http.Error(w, "Unavailable", http.StatusServiceUnavailable)
			return
		}
		if body.Action == "prepare" {
			if !body.Consent {
				http.Error(w, "Consent required", http.StatusBadRequest)
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
		if body.Action == "client-certificates" {
			reader, ok := service.Reader.(interface {
				ListPhoneCertificates(context.Context, string) ([]benefit.Certificate, error)
			})
			if !ok {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			values, readErr := reader.ListPhoneCertificates(r.Context(), phone)
			if readErr != nil {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			output["certificates"] = values
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(output)
			return
		}
		if body.Action == "review-visits" || body.Action == "review-check" {
			if body.Action == "review-check" {
				if value, parseErr := strconv.ParseUint(body.RecordID, 10, 64); parseErr != nil || value == 0 {
					http.Error(w, "Bad request", http.StatusBadRequest)
					return
				}
				snap, readErr := service.Reader.ReadNativeBooking(r.Context(), body.RecordID)
				if readErr != nil {
					http.Error(w, "Unavailable", http.StatusServiceUnavailable)
					return
				}
				if snap.Phone != phone || !reviewEligible(snap.Booking, time.Now()) {
					http.Error(w, "Not found", http.StatusNotFound)
					return
				}
				output["visit"] = reviewVisit(snap.Booking)
			} else {
				reader, ok := service.Reader.(interface {
					ListPhoneBookings(context.Context, string, time.Time) ([]booking.Booking, error)
				})
				if !ok {
					http.Error(w, "Unavailable", http.StatusServiceUnavailable)
					return
				}
				bookings, listErr := reader.ListPhoneBookings(r.Context(), phone, time.Now().Add(-90*24*time.Hour))
				if listErr != nil {
					http.Error(w, "Unavailable", http.StatusServiceUnavailable)
					return
				}
				visits := []map[string]any{}
				for _, b := range bookings {
					if reviewEligible(b, time.Now()) {
						visits = append(visits, reviewVisit(b))
					}
				}
				output["visits"] = visits
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(output)
			return
		}
		if body.Action == "confirmed" || body.Action == "record-context" {
			if _, parseErr := strconv.ParseUint(body.RecordID, 10, 64); parseErr != nil {
				http.Error(w, "Bad request", http.StatusBadRequest)
				return
			}
			snap, readErr := service.Reader.ReadNativeBooking(r.Context(), body.RecordID)
			if readErr != nil {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			owned := strings.HasPrefix(snap.APIID, "emotion-web-v2-") || (body.Action == "record-context" && strings.HasPrefix(snap.APIID, "omnichannel-web-"))
			// Live public booking may omit text api_id values. Validate the exact
			// online record's provider hash and retain its verified phone binding.
			if body.Action == "confirmed" && body.ManagementProof != "" && snap.Online {
				owned = true
			}
			if body.Action == "record-context" && !owned {
				row, recordErr := service.Repo.GetNotification(r.Context(), notifications.Key("record", body.RecordID))
				if recordErr != nil {
					http.Error(w, "Unavailable", http.StatusServiceUnavailable)
					return
				}
				owned = row.ManagementToken != "" && row.ManagementPhone == phone && row.CalendarPhone == phone
			}
			eligible := snap.Phone == phone && owned && snap.Booking.Status == booking.StatusConfirmed && snap.Booking.StartsAt.After(time.Now())
			if body.Action == "record-context" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"locale": lang, "eligible": eligible})
				return
			}
			if !eligible {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			b, calendarErr := ensureNativeCalendar(r.Context(), service, body.RecordID, snap)
			if calendarErr != nil {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			if body.ManagementProof != "" {
				reader, ok := service.Reader.(interface {
					ReadBooking(context.Context, booking.Booking) (booking.Booking, error)
				})
				if !ok || len(body.ManagementProof) > 256 {
					http.Error(w, "Unavailable", http.StatusServiceUnavailable)
					return
				}
				b.ManagementToken = body.ManagementProof
				if _, proofErr := reader.ReadBooking(r.Context(), b); proofErr != nil {
					http.Error(w, "Unavailable", http.StatusServiceUnavailable)
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
					http.Error(w, "Unavailable", http.StatusServiceUnavailable)
					return
				}
			}
			output["calendarUrl"] = calendar.Link(base, b, lang)
			eventID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("emotion-web-confirmation:"+body.RecordID)).String()
			err = service.Ingest(r.Context(), notifications.Event{ID: eventID, RecordID: body.RecordID, Status: "create"})
		}
		if err != nil {
			http.Error(w, "Unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(output)
	})
}

func reviewEligible(b booking.Booking, now time.Time) bool {
	return b.Status != booking.StatusCancelled && !b.NoShow && b.StaffID != "" && b.StaffID != "0" && b.Duration > 0 && !b.StartsAt.IsZero() && !b.StartsAt.Before(now.Add(-90*24*time.Hour)) && !b.StartsAt.Add(b.Duration).After(now)
}
func reviewVisit(b booking.Booking) map[string]any {
	return map[string]any{"id": b.ExternalID, "staffId": b.StaffID, "staffName": b.StaffName, "serviceName": strings.Join(b.ServiceNames, ", "), "datetime": b.StartsAt.Format(time.RFC3339)}
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
