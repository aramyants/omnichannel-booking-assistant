package altegio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCertificatesUseScopedPrivateReadsAndNormalizeAmounts(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer "+testPartnerToken+", User "+testUserToken {
			t.Error("private read contract")
		}
		switch r.URL.Path {
		case "/locations/" + testCompanyID + "/loyalty/certificate_types/search":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":5,"title":"Gift","balance":"50000.00","is_multi":true,"is_archived":false,"expiration_type_id":2,"expiration_timeout":6,"expiration_timeout_unit_id":3,"online_sale_is_enabled":false},{"id":6,"title":"Archived","balance":1,"is_archived":true}]}`))
		case "/company/" + testCompanyID + "/clients/search":
			var search clientSearchRequest
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&search) != nil || len(search.Filters) != 1 || search.Filters[0].State.Value != "+37491123456" || search.PageSize != 100 {
				t.Error("unscoped customer search")
			}
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":88,"phone":"37491123456"},{"id":99,"phone":"37491123457"}]}`))
		case "/locations/" + testCompanyID + "/client/88/loyalty":
			if r.Method != http.MethodGet {
				t.Error("loyalty summary must be read only")
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"certificates":[{"id":7,"number":"PRIVATE-1234","balance":50000,"expiration_date":"2027-10-21","type":{"title":"Gift"},"status":{"title":"Active"}}]}}`))
		default:
			t.Error("unexpected path")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server)
	types, err := client.ListCertificateTypes(t.Context())
	if err != nil || len(types) != 1 || types[0].Value != 50000 || types[0].OnlineSaleEnabled {
		t.Fatal(types, err)
	}
	cards, err := client.ListPhoneCertificates(t.Context(), "+37491123456")
	if err != nil || len(cards) != 1 || cards[0].MaskedNumber != "•••• 1234" || cards[0].ExpiresAt != "2027-10-21" {
		t.Fatal(cards, err)
	}
	if _, err = client.ListPhoneCertificates(t.Context(), "not-a-phone"); err == nil {
		t.Fatal("invalid phone accepted")
	}
	if calls != 3 {
		t.Fatal("invalid phone reached provider")
	}
}

func TestCertificatesFailClosedForUnprovenOrIncompleteClientResults(t *testing.T) {
	for _, tc := range []struct {
		name      string
		clients   string
		summary   string
		wantError bool
		wantReads int
		wantCards int
	}{
		{name: "fuzzy other phone", clients: `[{"id":99,"phone":"37491123457"}]`},
		{name: "hidden phone", clients: `[{"id":88}]`, wantError: true},
		{name: "invalid client id", clients: `[{"id":0,"phone":"37491123456"}]`, wantError: true},
		{name: "missing certificate field", clients: `[{"id":88,"phone":"37491123456"}]`, summary: `{}`, wantError: true, wantReads: 1},
		{name: "null certificate field", clients: `[{"id":88,"phone":"37491123456"}]`, summary: `{"certificates":null}`, wantError: true, wantReads: 1},
		{name: "empty owned account", clients: `[{"id":88,"phone":"37491123456"}]`, summary: `{"certificates":[]}`, wantReads: 1},
		{name: "duplicate client and certificate", clients: `[{"id":88,"phone":"37491123456"},{"id":88,"phone":"37491123456"}]`, summary: `{"certificates":[{"id":7,"number":"PRIVATE-1234","balance":50000},{"id":7,"number":"PRIVATE-1234","balance":50000}]}`, wantReads: 1, wantCards: 1},
		{name: "conflicting certificate state", clients: `[{"id":88,"phone":"37491123456"}]`, summary: `{"certificates":[{"id":7,"number":"PRIVATE-1234","balance":50000},{"id":7,"number":"PRIVATE-1234","balance":0}]}`, wantError: true, wantReads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/company/" + testCompanyID + "/clients/search":
					_, _ = w.Write([]byte(`{"success":true,"data":` + tc.clients + `}`))
				case "/locations/" + testCompanyID + "/client/88/loyalty":
					reads++
					_, _ = w.Write([]byte(`{"success":true,"data":` + tc.summary + `}`))
				default:
					t.Error("read escaped exact verified phone and studio scope", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			cards, err := newTestClient(t, server).ListPhoneCertificates(t.Context(), "+37491123456")
			if (err != nil) != tc.wantError || reads != tc.wantReads || (!tc.wantError && len(cards) != tc.wantCards) {
				t.Fatal("unexpected certificate ownership result", err, reads, len(cards))
			}
		})
	}
}

func TestCertificateCodesAndEmptyAccount(t *testing.T) {
	for raw, want := range map[string]string{`"001234"`: "001234", `1234`: "1234", `9007199254740993`: "9007199254740993", `null`: "", `{}`: "", `12.5`: "", `-1`: ""} {
		if got := certificateNumber([]byte(raw)); got != want {
			t.Fatal(raw, got)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "JSON media type required", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[],"meta":{"count":0}}`))
	}))
	defer server.Close()
	rows, err := newTestClient(t, server).ListPhoneCertificates(t.Context(), "+37491123456")
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty certificate account must be an empty list", rows, err)
	}
	if got := (request{path: "/loyalty/certificates?phone=37491123456&location_id=1"}).logPath(); got != "/loyalty/certificates" {
		t.Fatal("private query in log", got)
	}
	if got := (request{path: "/locations/1/client/88/loyalty"}).logPath(); got != "/locations/1/client/[redacted]/loyalty" {
		t.Fatal("private client id in log", got)
	}
}

func TestCertificateExpiryCannotTurnMalformedDataIntoUnlimitedCredit(t *testing.T) {
	for raw, want := range map[string]string{`null`: "", `0`: "", `""`: "", `"2027-10-21"`: "2027-10-21", `"invalid"`: "invalid", `{}`: "invalid"} {
		if got := certificateExpiry([]byte(raw)); got != want {
			t.Fatal(raw, got)
		}
	}
}
