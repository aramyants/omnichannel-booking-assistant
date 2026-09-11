package config

import (
	"strings"
	"testing"
)

func TestEachMetaChannelCanBeEnabledIndependently(t *testing.T) {
	for _, prefix := range []string{"MESSENGER", "INSTAGRAM"} {
		t.Run(prefix, func(t *testing.T) {
			t.Setenv("APP_ENV", "development")
			t.Setenv("META_APP_SECRET", "test-secret")
			t.Setenv("META_VERIFY_TOKEN", "test-verify")
			t.Setenv(prefix+"_ACCESS_TOKEN", "test-token")
			idName := "MESSENGER_PAGE_ID"
			if prefix == "INSTAGRAM" {
				idName = "INSTAGRAM_ACCOUNT_ID"
			}
			t.Setenv(idName, "123")
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.WhatsApp.Enabled() {
				t.Fatal("shared secrets enabled WhatsApp")
			}
			if prefix == "MESSENGER" && !cfg.Messenger.Enabled() || prefix == "INSTAGRAM" && !cfg.Instagram.Enabled() {
				t.Fatal("channel not enabled")
			}
			t.Setenv(idName, "")
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), idName) {
				t.Fatalf("missing account not rejected: %v", err)
			}
		})
	}
}

func TestMissingMetaSettingsAreReportedInAStableOrder(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("META_APP_SECRET", "")
	t.Setenv("META_VERIFY_TOKEN", "")
	t.Setenv("MESSENGER_ACCESS_TOKEN", "test-token")
	t.Setenv("MESSENGER_PAGE_ID", "")

	var first string
	for range 20 {
		_, err := Load()
		if err == nil {
			t.Fatal("missing settings not rejected")
		}
		if first == "" {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("error order changed between runs:\n%s\n---\n%s", first, err.Error())
		}
	}
	want := []string{"MESSENGER_PAGE_ID", "META_APP_SECRET", "META_VERIFY_TOKEN"}
	last := -1
	for _, name := range want {
		at := strings.Index(first, name+" is required")
		if at < 0 || at < last {
			t.Fatalf("expected %v in that order, got:\n%s", want, first)
		}
		last = at
	}
}
