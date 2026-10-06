package config

import (
	"strings"
	"testing"
)

func TestSMSReadinessRequiresPrivateDeviceAndDisclosureTimestamp(t *testing.T) {
	for _, name := range []string{"missing", "bad SIM", "before disclosure"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("NATIVE_SMS_READY", "true")
			t.Setenv("NATIVE_BOOKING_PERMISSION_SINCE", "2026-10-05T19:21:45Z")
			t.Setenv("NATIVE_SMS_PERMISSION_SINCE", "2026-10-06T09:40:06Z")
			t.Setenv("SMSGATE_CREDENTIALS_JSON", `{"username":"user","password":"PRIVATE","device_id":"device","sim_number":1}`)
			if name == "missing" {
				t.Setenv("SMSGATE_CREDENTIALS_JSON", "")
			}
			if name == "bad SIM" {
				t.Setenv("SMSGATE_CREDENTIALS_JSON", `{"username":"user","password":"PRIVATE","device_id":"device","sim_number":0}`)
			}
			if name == "before disclosure" {
				t.Setenv("NATIVE_SMS_PERMISSION_SINCE", "2026-10-05T19:00:00Z")
			}
			want := map[string]string{"missing": "SMSGate credentials unavailable", "bad SIM": "explicit device and SIM", "before disclosure": "NATIVE_SMS_PERMISSION_SINCE"}[name]
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("unsafe SMS configuration error: %v", err)
			}
		})
	}
}
