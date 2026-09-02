package cloudtasks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/api/idtoken"
)

type validateToken func(context.Context, string, string) (*idtoken.Payload, error)

// Authorizer validates the OIDC identity Cloud Tasks puts in Authorization.
// Cloud Tasks metadata headers are deliberately ignored because Google
// documents them as informational rather than proof of identity.
type Authorizer struct {
	audience       string
	serviceAccount string
	validate       validateToken
}

// NewAuthorizer trusts only tokens for audience issued to serviceAccount.
func NewAuthorizer(audience, serviceAccount string) (*Authorizer, error) {
	if audience == "" {
		return nil, errors.New("cloud tasks authorizer: audience is required")
	}
	if serviceAccount == "" {
		return nil, errors.New("cloud tasks authorizer: service account is required")
	}
	return &Authorizer{
		audience:       audience,
		serviceAccount: serviceAccount,
		validate:       idtoken.Validate,
	}, nil
}

// Authorize validates one Bearer header.
func (a *Authorizer) Authorize(ctx context.Context, authorization string) error {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return errors.New("cloud tasks authorizer: bearer token is required")
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	if token == "" {
		return errors.New("cloud tasks authorizer: bearer token is empty")
	}
	payload, err := a.validate(ctx, token, a.audience)
	if err != nil {
		return fmt.Errorf("cloud tasks authorizer: invalid token: %w", err)
	}
	email, _ := payload.Claims["email"].(string)
	if email != a.serviceAccount {
		return fmt.Errorf("cloud tasks authorizer: unexpected service account %q", email)
	}
	return nil
}
