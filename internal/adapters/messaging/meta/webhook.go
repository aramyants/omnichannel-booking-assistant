// Package meta adapts Meta's messaging platforms to the application's ports.
//
// WhatsApp, Instagram and Messenger share one webhook contract, one signing
// scheme and one Graph API, so the parts that are genuinely common live here
// and each channel adds only what differs. Meta payload shapes do not leave
// this package.
package meta

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// SignatureHeader carries an HMAC of the request body, signed with the app
// secret.
const SignatureHeader = "X-Hub-Signature-256"

// ErrMalformedUpdate reports a webhook body that is not a Meta update.
var ErrMalformedUpdate = errors.New("malformed meta update")

// Webhook verifies inbound deliveries from Meta.
//
// Two separate mechanisms are involved and both matter. Subscribing to a
// webhook is a GET carrying a token Meta echoes back, which proves to Meta that
// the endpoint is ours. Every delivery after that is a POST signed with the app
// secret, which proves to us that the request is Meta's.
type Webhook struct {
	appSecret   string
	verifyToken string
}

// NewWebhook returns a Webhook for one Meta app.
func NewWebhook(appSecret, verifyToken string) (*Webhook, error) {
	if appSecret == "" {
		return nil, errors.New("meta: app secret is required to verify signatures")
	}
	if verifyToken == "" {
		return nil, errors.New("meta: verify token is required to complete subscription")
	}
	return &Webhook{appSecret: appSecret, verifyToken: verifyToken}, nil
}

// VerifyChallenge answers Meta's subscription handshake.
//
// Meta sends a GET with a token it was configured with and a challenge. Echoing
// the challenge only when the token matches is what proves the endpoint belongs
// to whoever set the app up.
func (w *Webhook) VerifyChallenge(query map[string][]string) (string, bool) {
	first := func(key string) string {
		if values := query[key]; len(values) > 0 {
			return values[0]
		}
		return ""
	}

	if first("hub.mode") != "subscribe" {
		return "", false
	}
	// Constant time: a byte-by-byte comparison leaks the token one character
	// at a time to anyone able to measure the response.
	if !constantTimeEqual(first("hub.verify_token"), w.verifyToken) {
		return "", false
	}

	challenge := first("hub.challenge")
	if challenge == "" {
		return "", false
	}
	return challenge, true
}

// VerifySignature reports whether body was signed with the app secret.
//
// The signature covers the exact bytes Meta sent. It must be checked before the
// body is parsed or re-encoded, because any normalisation, including the
// Unicode escaping a JSON round trip performs, changes the bytes and so changes
// the hash.
func (w *Webhook) VerifySignature(header string, body []byte) bool {
	const prefix = "sha256="

	if !strings.HasPrefix(header, prefix) {
		return false
	}

	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(w.appSecret))
	mac.Write(body)

	return hmac.Equal(mac.Sum(nil), want)
}

// constantTimeEqual compares two strings without leaking their contents through
// how long the comparison takes.
func constantTimeEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

// ChallengeResponse writes the answer to a subscription handshake.
func ChallengeResponse(w http.ResponseWriter, challenge string) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, challenge)
}
