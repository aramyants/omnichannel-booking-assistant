package assistant

import "context"

func (s *Service) applyPreferredLanguage(ctx context.Context, sess *session) {
	if s.preferredLanguage == nil || sess.customer.VerifiedPhone == "" {
		return
	}
	// A verified saved choice wins over the messaging app's device language.
	// Different-channel unverified identities never receive another client's state.
	if lang, err := s.preferredLanguage(ctx, sess.customer.VerifiedPhone, "", false); err == nil {
		sess.language = languageOfTag(lang)
	}
}
