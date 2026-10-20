package altegio

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCertificatesUseScopedPrivateReadsAndNormalizeAmounts(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+testPartnerToken+", User "+testUserToken {
			t.Error("private read contract")
		}
		switch r.URL.Path {
		case "/locations/" + testCompanyID + "/loyalty/certificate_types/search":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":5,"title":"Gift","balance":"50000.00","is_multi":true,"is_archived":false,"expiration_type_id":2,"expiration_timeout":6,"expiration_timeout_unit_id":3,"online_sale_is_enabled":false},{"id":6,"title":"Archived","balance":1,"is_archived":true}]}`))
		case "/loyalty/certificates":
			if r.URL.Query().Get("phone") != "37491123456" || r.URL.Query().Get("location_id") != testCompanyID {
				t.Error("unscoped customer lookup")
			}
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":7,"number":"PRIVATE-1234","balance":50000,"expiration_date":"2027-10-21","type":{"title":"Gift"},"status":{"title":"Active"}}]}`))
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
	if calls != 2 {
		t.Fatal("invalid phone reached provider")
	}
}

func TestCertificateExpiryCannotTurnMalformedDataIntoUnlimitedCredit(t *testing.T) {
	for raw, want := range map[string]string{`null`: "", `0`: "", `""`: "", `"2027-10-21"`: "2027-10-21", `"invalid"`: "invalid", `{}`: "invalid"} {
		if got := certificateExpiry([]byte(raw)); got != want {
			t.Fatal(raw, got)
		}
	}
}
