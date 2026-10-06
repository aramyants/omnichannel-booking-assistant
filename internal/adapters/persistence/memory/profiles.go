package memory

import (
	"context"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"slices"
	"strings"
)

// LinkVerifiedIdentity preserves historical booking ownership as aliases; it
// does not merge anyone on a name, email, or unverified booking phone.
func (s *Store) LinkVerifiedIdentity(_ context.Context, identity customer.ChannelIdentity, old customer.Customer, rawPhone string) (customer.Customer, error) {
	phone, err := customer.NormalizePhone(rawPhone)
	if err != nil {
		return customer.Customer{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.identities[identity.Key()]
	if !ok || stored.CustomerID != old.ID {
		return customer.Customer{}, customer.ErrNotFound
	}
	canonicalID := customer.ProfileID(phone)
	canonical, ok := s.customers[canonicalID]
	if !ok {
		canonical = customer.Customer{ID: canonicalID, Name: old.Name, Phone: phone, CreatedAt: old.CreatedAt, UpdatedAt: s.now()}
		s.customers[canonicalID] = canonical
	}
	if old.ID != canonicalID && !strings.HasPrefix(old.ID, "verified_phone_") && !slices.Contains(s.profileAliases[canonicalID], old.ID) {
		s.profileAliases[canonicalID] = append(s.profileAliases[canonicalID], old.ID)
	}
	stored.CustomerID = canonicalID
	s.identities[identity.Key()] = stored
	canonical.VerifiedPhone = phone
	return canonical, nil
}
