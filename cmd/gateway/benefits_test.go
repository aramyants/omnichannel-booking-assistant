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
func (r *benefitsReader) ListPhoneCertificates(_ context.Context, phone string) ([]benefit.Certificate, error) {
	r.clientCalls++
	r.phone = phone
	return []benefit.Certificate{{ID: "7", Title: "Gift", Number: "PRIVATE-1234", Balance: 50000}}, nil
}
func TestCertificateBridgeRequiresFreshSignatureAndProvenPhone(t *testing.T) {
	reader := &benefitsReader{}
	service := &notifications.Service{Repo: memory.New(), Reader: reader}
	handler := bookingContextHandler(service, "test-key", "https://book.motionconcept.rest/")
	for _, tc := range []struct {
		body   string
		signed bool
		want   int
	}{
		{`{"action":"certificate-types"}`, false, 401},
		{`{"action":"certificate-types"}`, true, 200},
		{`{"action":"client-certificates"}`, true, 400},
		{`{"action":"client-certificates","phone":"+37491123456"}`, true, 200},
	} {
		raw := []byte(tc.body)
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		req := httptest.NewRequest(http.MethodPost, "/internal/booking-context", bytes.NewReader(raw))
		req.Header.Set("X-Emotion-Time", stamp)
		if tc.signed {
			req.Header.Set("X-Emotion-Signature", contextSignature("test-key", stamp, raw))
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatal(tc.body, rec.Code, rec.Body.String())
		}
		if tc.want == 200 {
			var output map[string]any
			if json.Unmarshal(rec.Body.Bytes(), &output) != nil || output["certificates"] == nil {
				t.Fatal("missing typed result")
			}
		}
	}
	if reader.catalogCalls != 1 || reader.clientCalls != 1 || reader.phone != "+37491123456" {
		t.Fatal("unauthorized or incorrect phone read", reader)
	}
}
