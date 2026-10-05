package config

import (
	"strings"
	"testing"
)

func TestAIReasoningBudgetValidation(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	for _, tc := range []struct {
		effort, budget string
		valid          bool
	}{{"high", "8192", true}, {"unknown", "8192", false}, {"high", "-1", false}, {"high", "999999", false}} {
		t.Setenv("OPENAI_REASONING_EFFORT", tc.effort)
		t.Setenv("OPENAI_MAX_OUTPUT_TOKENS", tc.budget)
		cfg, err := Load()
		if tc.valid {
			if err != nil || cfg.AI.ReasoningEffort != "high" || cfg.AI.MaxOutputTokens != 8192 {
				t.Fatal("valid reasoning configuration rejected")
			}
		} else if err == nil || !strings.Contains(err.Error(), "OPENAI_") {
			t.Fatal("invalid reasoning configuration accepted")
		}
	}
}
