package altegio

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaffProfessionUsesNativePositionAndNeverAccountRole(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":1,"name":"Therapist","specialization":"Massage","position":{"title":"Senior masseur"},"user":{"role":"Owner"}},{"id":2,"name":"Other","specialization":"Masseur","user":{"role":"Manager"}}]}`))
	}))
	defer server.Close()
	staff, err := newTestClient(t, server).ListStaff(t.Context())
	if err != nil || len(staff) != 2 || staff[0].Specialisation != "Senior masseur" || staff[1].Specialisation != "Masseur" {
		t.Fatal("native job title was replaced by an account role or inferred profession", staff, err)
	}
}
