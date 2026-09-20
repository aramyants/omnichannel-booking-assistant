package config

import (
	"strings"
	"testing"
)

func TestBusinessProfileLoadsVerbatimLocalizedFacts(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("BUSINESS_ADDRESS_EN", "Myasnikyan 1/6, Yerevan")
	t.Setenv("BUSINESS_ADDRESS_HY", "Մյասնիկյան 1/6, Երևան")
	t.Setenv("BUSINESS_ADDRESS_RU", "Мясникян 1/6, Ереван")
	t.Setenv("BUSINESS_PHONE", "+374 94 768067")
	t.Setenv("BUSINESS_PREPARATION_RU", "Приходите за 5 минут.")
	t.Setenv("BUSINESS_INSTAGRAM_URL", "https://www.instagram.com/e.motion.concept/")
	t.Setenv("BUSINESS_MAP_URL", "https://maps.example/studio")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if got := cfg.BusinessProfile.Address.Russian; got != "Мясникян 1/6, Ереван" {
		t.Errorf("Russian address = %q", got)
	}
	if got := cfg.BusinessProfile.Phone; got != "+374 94 768067" {
		t.Errorf("phone = %q", got)
	}
	if got := cfg.BusinessProfile.Preparation.English; got != "" {
		t.Errorf("an unset translation was synthesized: %q", got)
	}
}

func TestBusinessProfileRejectsNonHTTPSLinks(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("BUSINESS_MAP_URL", "http://maps.example/studio")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "BUSINESS_MAP_URL must be an absolute https URL") {
		t.Fatalf("Load() error = %v", err)
	}
}
