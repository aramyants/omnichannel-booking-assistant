package conversation

import "testing"

func TestResolvePresentedChoiceAcceptsNumberOrVisibleLabel(t *testing.T) {
	conv := Conversation{PresentedChoices: []string{"Face Motion", "Motion Sport"}}
	tests := map[string]string{
		"1":            "Face Motion",
		"2)":           "Motion Sport",
		"motion sport": "Motion Sport",
	}
	for input, want := range tests {
		if got, ok := conv.ResolvePresentedChoice(input); !ok || got != want {
			t.Errorf("ResolvePresentedChoice(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
	for _, input := range []string{"0", "3", "077213273", "2 tomorrow", ""} {
		if got, ok := conv.ResolvePresentedChoice(input); ok {
			t.Errorf("ResolvePresentedChoice(%q) = %q, true; want no match", input, got)
		}
	}
}
