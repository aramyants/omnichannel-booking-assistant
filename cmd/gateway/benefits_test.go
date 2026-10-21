package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/benefit"
)

type benefitsReader struct {
	contextRecordReader
	catalogCalls, clientCalls int
	phone                     string
}

func (r *benefitsReader) ListCertificateTypes(context.Context) ([]benefit.CertificateType, error) {
	r.catalogCalls++
	return []benefit.CertificateType{{ID: "5", Title: "Gift", Value: 50000}}, nil
}
func (r *benefitsReader) AnyStaffEnabled(context.Context) (bool, error) {
	return true, nil
}
func (r *benefitsReader) ListPhoneCertificates(_ context.Context, phone string) ([]benefit.Certificate, error) {
	r.clientCalls++
	r.phone = phone
	return []benefit.Certificate{{ID: "7", Title: "Gift", Number: "PRIVATE-1234", Balance: 50000}}, nil
}

func TestBookingSettingsBridgeRequiresSignature(t *testing.T) {
	handler := bookingContextHandler(&notifications.Service{Repo: memory.New(), Reader: &benefitsReader{}}, "test-key", "https://book.motionconcept.rest/")
	for _, signed := range []bool{false, true} {
		raw := []byte(`{"action":"booking-settings"}`)
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/booking-context", bytes.NewReader(raw))
		req.Header.Set("X-Emotion-Time", stamp)
		if signed {
			req.Header.Set("X-Emotion-Signature", contextSignature("test-key", stamp, raw))
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if !signed && rec.Code != 401 || signed && rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		if signed {
			var output struct {
				AnyStaff bool `json:"anyStaff"`
			}
			if json.Unmarshal(rec.Body.Bytes(), &output) != nil || !output.AnyStaff {
				t.Fatal("missing native studio setting")
			}
		}
	}
}
func TestRetiredCertificateBridgeKeepsSignatureGateAndNeverReadsNativeData(t *testing.T) {
	reader := &benefitsReader{}
	service := &notifications.Service{Repo: memory.New(), Reader: reader}
	handler := bookingContextHandler(service, "test-key", "https://book.motionconcept.rest/")
	for _, tc := range []struct {
		body   string
		signed bool
		want   int
	}{
		{`{"action":"certificate-types"}`, false, 401},
		{`{"action":"certificate-types"}`, true, 410},
		{`{"action":"client-certificates"}`, true, 410},
		{`{"action":"client-certificates","phone":"+37491123456"}`, true, 410},
	} {
		raw := []byte(tc.body)
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/booking-context", bytes.NewReader(raw))
		req.Header.Set("X-Emotion-Time", stamp)
		if tc.signed {
			req.Header.Set("X-Emotion-Signature", contextSignature("test-key", stamp, raw))
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatal(tc.body, rec.Code, rec.Body.String())
		}
	}
	if reader.catalogCalls != 0 || reader.clientCalls != 0 || reader.phone != "" {
		t.Fatal("unauthorized or incorrect phone read", reader)
	}
}
