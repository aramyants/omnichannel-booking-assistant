package customer

import "testing"

func TestNormalizePhone(t *testing.T) {
	for _, input := range []string{"+374 91 123-456", "091123456", "91123456", "37491123456", "00374 (91) 123.456"} {
		got, err := NormalizePhone(input)
		if err != nil || got != "+37491123456" {
			t.Errorf("%q => %q, %v", input, got, err)
		}
	}
	for input, want := range map[string]string{"+1 (202) 555-0123": "+12025550123", "+44 20 7946 0018": "+442079460018"} {
		got, err := NormalizePhone(input)
		if err != nil || got != want {
			t.Errorf("%q => %q %v", input, got, err)
		}
	}
	for _, input := range []string{"", "123", "+37400000000", "+999123456789", "call me", "++37491123456", "2025550123", "+37491123456 ext 2", "+37491123456/+37494123456"} {
		if value, err := NormalizePhone(input); err == nil {
			t.Errorf("accepted ambiguous/invalid %q as %q", input, value)
		}
	}
}
