package altegio

import (
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

// motionCatalogueCopy adds native category context to ambiguous native names.
// No service ID maps to stored names, descriptions, prices or durations.
func motionCatalogueCopy(s booking.Service) booking.Service {
	name, category := strings.TrimSpace(s.Name), strings.TrimSpace(s.Category)
	durationLabel := name != "" && (name[0] == '+' || (name[0] >= '0' && name[0] <= '9'))
	needsContext := durationLabel || strings.Contains(strings.ToLower(category), "four hands")
	if name != "" && category != "" && needsContext && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(category)) {
		s.Name = category + " · " + name
	}
	return s
}
