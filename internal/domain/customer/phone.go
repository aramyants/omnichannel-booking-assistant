package customer

import (
	"errors"
	"strings"
	"unicode"

	"github.com/nyaruka/phonenumbers/v2"
)

// NormalizePhone accepts Armenian national numbers and explicit international
// numbers. It never guesses the country for an ambiguous foreign number.
// E.164 is used consistently for contacts and Altegio requests.
func NormalizePhone(raw string) (string, error) {
	var b strings.Builder
	for i, r := range strings.TrimSpace(raw) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && i == 0:
			b.WriteRune(r)
		case unicode.IsSpace(r) || strings.ContainsRune("()-.", r):
		default:
			return "", errors.New("not a phone number; please provide digits with country code, for example +374 91 123456")
		}
	}
	input := b.String()
	if strings.HasPrefix(input, "00") {
		input = "+" + input[2:]
	}
	if strings.HasPrefix(input, "374") && len(input) == 11 {
		input = "+" + input
	}
	national := len(input) == 8 || (len(input) == 9 && input[0] == '0')
	if !strings.HasPrefix(input, "+") && !national {
		return "", errors.New("not a usable phone number; please include the international country code, for example +374 91 123456")
	}
	parsed, err := phonenumbers.Parse(input, "AM")
	if err != nil || !phonenumbers.IsValidNumber(parsed) {
		return "", errors.New("that phone number is not valid; please check the digits and country code")
	}
	return phonenumbers.Format(parsed, phonenumbers.E164), nil
}
