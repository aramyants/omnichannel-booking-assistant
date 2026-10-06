package smsgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(Config{APIBaseURL: server.URL + "/api/3rdparty/v1",
		Username: "device-user", Password: "device-password", DeviceID: "work-phone", SIMNumber: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func validMessage() Message {
	return Message{ID: "booking-notice-123", Phone: "+37494768067",
		Text: "Ձեր ամրագրումը հաստատված է։", ValidUntil: time.Now().Add(5 * time.Minute), Priority: 100}
}

func TestSendPinsWorkDeviceAndSIMAndPreservesArmenian(t *testing.T) {
	msg := validMessage()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		username, password, ok := r.BasicAuth()
		if !ok || username != "device-user" || password != "device-password" ||
			r.Method != http.MethodPost || r.URL.Path != "/api/3rdparty/v1/messages" || r.URL.RawQuery != "" {
			t.Error("incorrect authenticated API request")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["deviceId"] != "work-phone" || payload["simNumber"] != float64(2) ||
			payload["id"] != msg.ID || payload["withDeliveryReport"] != true || payload["priority"] != float64(100) {
			t.Errorf("wrong routing or tracking: %+v", payload)
		}
		if payload["textMessage"].(map[string]any)["text"] != msg.Text ||
			payload["phoneNumbers"].([]any)[0] != msg.Phone || payload["ttl"] != nil || payload["message"] != nil {
			t.Errorf("incorrect content or expiration schema: %+v", payload)
		}
		if payload["validUntil"] != msg.ValidUntil.UTC().Format(time.RFC3339) {
			t.Error("expiration changed")
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintf(w, `{"id":%q,"state":"Pending"}`, msg.ID)
	}))
	defer server.Close()
	status, err := testClient(t, server).Send(t.Context(), msg)
	if err != nil || status.ID != msg.ID || status.State != Pending || calls != 1 {
		t.Fatalf("status=%+v err=%v calls=%d", status, err, calls)
	}
}

func TestAmbiguousSendIsNeverRetriedOrReportedRejected(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"server failure", 500, "private OTP 1234 phone +37494768067"},
		{"duplicate or conflict", 409, "private content"},
		{"malformed success", 202, `{"id":"different","state":"Pending"}`},
		{"empty success", 202, ""},
		{"redirect", 307, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			_, err := testClient(t, server).Send(t.Context(), validMessage())
			if !errors.Is(err, ErrUncertain) || calls != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("private provider details")
}

func TestTransportFailureNeedsReconciliation(t *testing.T) {
	c, err := NewClient(Config{Username: "user", Password: "password", DeviceID: "device", SIMNumber: 1},
		&http.Client{Transport: failingTransport{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Send(context.Background(), validMessage())
	if !errors.Is(err, ErrUncertain) || strings.Contains(err.Error(), "private") {
		t.Fatalf("err=%v", err)
	}
}

func TestInvalidOrExpiredMessageNeverReachesPhone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid send reached gateway")
	}))
	defer server.Close()
	c := testClient(t, server)
	for _, change := range []func(*Message){
		func(m *Message) { m.ID = "../escape" },
		func(m *Message) { m.ID = strings.Repeat("x", 33) },
		func(m *Message) { m.Phone = "094768067" },
		func(m *Message) { m.Text = " " },
		func(m *Message) { m.ValidUntil = time.Now().Add(-time.Minute) },
		func(m *Message) { m.Priority = 128 },
	} {
		msg := validMessage()
		change(&msg)
		if _, err := c.Send(t.Context(), msg); !errors.Is(err, ErrRejected) {
			t.Errorf("invalid request err=%v", err)
		}
	}
}

func TestStatusPreservesProviderDeliverySemantics(t *testing.T) {
	for _, state := range []State{Pending, Processed, Sent, Delivered, Failed, Cancelling, Cancelled} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/api/3rdparty/v1/messages/booking-notice-123" {
				t.Error("incorrect status request")
			}
			_, _ = fmt.Fprintf(w, `{"id":"booking-notice-123","state":%q}`, state)
		}))
		status, err := testClient(t, server).Status(t.Context(), "booking-notice-123")
		server.Close()
		if err != nil || status.State != state {
			t.Errorf("state=%s status=%+v err=%v", state, status, err)
		}
	}
}

func TestRejectsUnsafeConfiguration(t *testing.T) {
	for _, base := range []string{"http://private.example/api/3rdparty/v1",
		"https://user:password@private.example/api/3rdparty/v1", "https://private.example?token=secret", "https://private.example"} {
		if _, err := NewClient(Config{APIBaseURL: base, Username: "user", Password: "password",
			DeviceID: "device", SIMNumber: 1}, nil); err == nil {
			t.Errorf("accepted unsafe URL")
		}
	}
}
