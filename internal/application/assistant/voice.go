package assistant

import (
	"regexp"
	"strings"
)

// Heart-free replies are a studio policy, including older models that ignore
// the prompt. Preserve all wording, factual details and paragraph boundaries.
// Variation selectors and joined fire/mending-heart suffixes belong to the
// removed emoji; leaving them behind would produce broken visible glyphs.
var hearts = regexp.MustCompile(`[♥♡❤❣❥❦❧💌💓💔💕💖💗💘💙💚💛💜💝💞💟🖤🤍🤎🩷🩵🩶🫶😍🥰😘][\x{FE0E}\x{FE0F}]?(?:\x{200D}[🔥🩹])?`)
var extraInlineSpace = regexp.MustCompile(`[ \t]{2,}`)

func withoutHearts(text string) string {
	if !hearts.MatchString(text) {
		return text
	}
	lines := strings.Split(hearts.ReplaceAllString(text, ""), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(extraInlineSpace.ReplaceAllString(line, " "))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
