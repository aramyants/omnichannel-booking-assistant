package altegio

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnyStaffFollowsLiveStudioSetting(t *testing.T) {
	for _, raw := range []string{`true`, `false`, `null`} {
		t.Run(raw, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/company/"+testCompanyID+"/settings/online" || r.Header.Get("Authorization") != "Bearer "+testPartnerToken+", User "+testUserToken {
					t.Error("settings must use scoped private studio read")
				}
				_, _ = w.Write([]byte(`{"success":true,"data":{"any_master":` + raw + `}}`))
			}))
			defer server.Close()
			got, err := newTestClient(t, server).AnyStaffEnabled(t.Context())
			if raw == "null" {
				if err == nil || got {
					t.Fatal("unknown setting enabled automatic assignment")
				}
			} else if err != nil || got != (raw == "true") {
				t.Fatal(got, err)
			}
		})
	}
}
