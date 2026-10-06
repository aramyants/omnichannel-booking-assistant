package notifications

import "time"

// Verification stores an expiring, single-use cabinet challenge. Hash is an
// HMAC; the code itself never enters persistence, logs or model context.
type PhoneChallenge struct {
	Phone            string    `firestore:"phone"`
	Hash             []byte    `firestore:"hash"`
	State            string    `firestore:"state"`
	ExpiresAt        time.Time `firestore:"expires_at"`
	NextSendAt       time.Time `firestore:"next_send_at"`
	WindowStarted    time.Time `firestore:"window_started"`
	Sends            int       `firestore:"sends"`
	Attempts         int       `firestore:"attempts"`
	ContactMessageID string    `firestore:"contact_message_id"`
}
