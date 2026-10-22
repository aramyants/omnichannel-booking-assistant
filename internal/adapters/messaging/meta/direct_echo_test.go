package meta

import (
	"fmt"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"testing"
	"time"
)

func TestDirectManualEchoIsBoundToBusinessAndDoesNotBecomeCustomerInput(t *testing.T) {
	for _, provider := range []messaging.Provider{messaging.ProviderInstagram, messaging.ProviderMessenger} {
		t.Run(string(provider), func(t *testing.T) {
			object := "page"
			if provider == messaging.ProviderInstagram {
				object = "instagram"
			}
			event := `{"sender":{"id":"business-1"},"recipient":{"id":"client-1"},"timestamp":1792634400000,"message":{"mid":"manual-1","is_echo":true,"text":"I will handle this personally"%s}}`
			for _, tc := range []struct {
				asset, extra string
				want         int
			}{{"business-1", "", 1}, {"other-business", "", 0}, {"business-1", `,"app_id":12345`, 0}} {
				// A different asset is never accepted merely because an app signature is valid.
				body := directUpdate(object, tc.asset, fmt.Sprintf(event, tc.extra))
				replies, err := parseDirectExternalReplies(body, time.Now(), provider, "business-1")
				if err != nil || len(replies) != tc.want {
					t.Fatalf("manual echo: %v %v", replies, err)
				}
				incoming, err := parseDirect(body, time.Now(), provider, "business-1")
				if err != nil || len(incoming) != 0 {
					t.Fatal("staff echo became customer input")
				}
			}
		})
	}
}
