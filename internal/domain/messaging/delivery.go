package messaging

import "errors"

// ErrDeliveryUncertain means a send may already have reached the recipient.
// Repeating it automatically can send the same answer twice.
var ErrDeliveryUncertain = errors.New("message delivery outcome is uncertain")

// ErrDeliveryRejected means the provider definitively refused this send.
// Redelivering the customer's webhook cannot repair the refusal.
var ErrDeliveryRejected = errors.New("message delivery was rejected")

func TerminalDelivery(err error) bool {
	return errors.Is(err, ErrDeliveryUncertain) || errors.Is(err, ErrDeliveryRejected)
}
