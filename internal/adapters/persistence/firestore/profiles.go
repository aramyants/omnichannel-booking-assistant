package firestore

import (
	"cloud.google.com/go/firestore"
	"context"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"slices"
	"strings"
	"time"
)

// LinkVerifiedIdentity atomically joins a proven channel to one canonical
// client. Old customer IDs remain aliases, so no booking or reminder is lost.
func (s *Store) LinkVerifiedIdentity(ctx context.Context, identity customer.ChannelIdentity, old customer.Customer, rawPhone string) (customer.Customer, error) {
	phone, err := customer.NormalizePhone(rawPhone)
	if err != nil {
		return customer.Customer{}, err
	}
	canonicalID := customer.ProfileID(phone)
	profileRef := s.client.Collection(collectionCustomers).Doc(canonicalID)
	identityRef := s.client.Collection(collectionIdentities).Doc(identity.Key())
	var resolved customer.Customer
	err = s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		identitySnap, err := tx.Get(identityRef)
		if err != nil {
			return err
		}
		var existing identityDoc
		if err := identitySnap.DataTo(&existing); err != nil {
			return err
		}
		if existing.CustomerID != old.ID {
			return customer.ErrNotFound
		}
		snapshot, err := tx.Get(profileRef)
		doc := customerDoc{Name: old.Name, Phone: phone, CreatedAt: old.CreatedAt, UpdatedAt: time.Now().UTC()}
		if err == nil {
			if err := snapshot.DataTo(&doc); err != nil {
				return err
			}
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		if old.ID != canonicalID && !strings.HasPrefix(old.ID, "verified_phone_") && !slices.Contains(doc.LinkedCustomerIDs, old.ID) {
			doc.LinkedCustomerIDs = append(doc.LinkedCustomerIDs, old.ID)
		}
		resolved = customer.Customer{ID: canonicalID, Name: doc.Name, Phone: doc.Phone, VerifiedPhone: phone, CreatedAt: doc.CreatedAt, UpdatedAt: doc.UpdatedAt}
		if err := tx.Set(profileRef, doc); err != nil {
			return err
		}
		return tx.Update(identityRef, []firestore.Update{{Path: "customer_id", Value: canonicalID}})
	})
	return resolved, err
}
