// Package customer models the people a business serves, separately from the
// messaging accounts they happen to contact it from.
package customer

import (
	"errors"
	"fmt"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// ErrNotFound reports a customer or identity that does not exist.
var ErrNotFound = errors.New("customer not found")

// Customer is one person, independent of how they reach the business.
//
// A customer is deliberately not the same thing as a messaging account. The
// same person may write from Telegram today and WhatsApp tomorrow, and the
// business needs to recognise them as one person with one booking history.
type Customer struct {
	ID string

	// Name is what to call the customer. It starts as whatever the messaging
	// provider volunteered and is replaced when the customer states their own.
	Name string

	// Phone is the number bookings are made against. It is empty until the
	// customer provides one, because provider profiles do not reliably carry it.
	Phone string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ChannelIdentity is one messaging account belonging to a customer.
//
// Identities are never merged automatically on a resemblance such as a matching
// name. Two people can share a name, and wrongly merging them would expose one
// customer's appointments to another. Linking is an explicit act, normally
// confirmed by a verified phone number.
type ChannelIdentity struct {
	ID             string
	CustomerID     string
	Provider       messaging.Provider
	ExternalUserID string

	// DisplayName and Language are what the provider reported at the time this
	// identity was first seen. They are display hints, never identity.
	DisplayName string
	Language    string

	CreatedAt time.Time
}

// Key is the unique address of a messaging account across all channels.
func (c ChannelIdentity) Key() string {
	return string(c.Provider) + ":" + c.ExternalUserID
}

// Validate reports whether the identity is complete enough to store.
func (c ChannelIdentity) Validate() error {
	switch {
	case c.CustomerID == "":
		return fmt.Errorf("channel identity: customer id is empty")
	case c.Provider == "":
		return fmt.Errorf("channel identity: provider is empty")
	case c.ExternalUserID == "":
		return fmt.Errorf("channel identity: external user id is empty")
	}
	return nil
}
