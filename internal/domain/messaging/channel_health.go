package messaging

import "time"

// ChannelHealth contains only operational facts, never provider error bodies or
// customer data. A failed health lookup cannot undo a confirmed restriction.
type ChannelHealth struct {
	Known           bool            `firestore:"known"`
	Blocked         bool            `firestore:"blocked"`
	ProactivePaused bool            `firestore:"proactive_paused"`
	TemplatesKnown  bool            `firestore:"templates_known"`
	Templates       map[string]bool `firestore:"templates"`
	CheckedAt       time.Time       `firestore:"checked_at"`
}

func (h ChannelHealth) Allows(template, language string) bool {
	if h.Blocked {
		return false
	}
	if template == "" {
		return true
	}
	if h.ProactivePaused {
		return false
	}
	approved, known := h.Templates[template+":"+language]
	return !known || approved
}
