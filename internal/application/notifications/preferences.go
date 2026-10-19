package notifications

import (
	"context"
	"errors"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
)

// ResolveLanguage is shared by the verified website and every messaging route.
// A device hint can initialize a preference but cannot replace an explicit choice.
func (s *Service) ResolveLanguage(ctx context.Context, rawPhone, hint string, explicit bool) (string, error) {
	phone, err := customer.NormalizePhone(rawPhone)
	if err != nil {
		return "", err
	}
	if hint != "" && hint != "ru" && hint != "en" && hint != "hy" {
		return "", errors.New("unsupported language")
	}
	key := Key("language_phone", phone)
	language := "ru"
	err = s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		row := rows[key]
		if row.Contact.Language != "" {
			language = row.Contact.Language
		}
		if hint != "" && (explicit || row.Contact.Language == "") {
			language = hint
			row.Kind = "language"
			row.Contact.Language = hint
			row.UpdatedAt = s.now()
			if explicit {
				row.State = "explicit"
			} else {
				row.State = "inferred"
			}
		}
		return nil
	})
	return language, err
}
