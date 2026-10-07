package assistant

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
)

func purposeText(lang language) string {
	switch lang {
	case languageArmenian:
		return "Մենք օգնում ենք մեր ստուդիայի ծառայությունների, ամրագրումների և այցերի հարցերով։ Խնդրում ենք ընտրել ծառայություն կամ ուղարկել այցի մասին Ձեր հարցը։"
	case languageRussian:
		return "Мы помогаем с услугами нашей студии, записями и визитами. Пожалуйста, выберите услугу или отправьте Ваш вопрос о посещении."
	default:
		return "We help with our studio's services, appointments and visits. Please choose a service or send your question about a visit."
	}
}

func boundedPurposeMessages(history []conversation.Message) []ai.Message {
	if len(history) > 4 {
		history = history[len(history)-4:]
	}
	messages := toAIMessages(history)
	for i := range messages {
		text := []rune(messages[i].Text)
		limit := 500
		if i == len(messages)-1 {
			limit = 4000
		}
		if len(text) > limit {
			messages[i].Text = string(text[:limit])
		}
	}
	return messages
}

func boundedStudioMessages(history []conversation.Message) []ai.Message {
	messages := toAIMessages(history)
	remaining := 16000
	start := len(messages)
	for i := len(messages) - 1; i >= 0 && remaining > 0; i-- {
		text := messages[i].Text
		if messages[i].Role == ai.RoleUser && outsidePurposeReply(text) {
			text = "[An unrelated code request was declined.]"
		}
		runes := []rune(text)
		limit := min(2000, remaining)
		if i == len(messages)-1 {
			limit = min(4000, remaining)
		}
		if len(runes) > limit {
			runes = runes[:limit]
		}
		messages[i].Text = string(runes)
		remaining -= len(runes)
		start = i
	}
	return messages[start:]
}

func (s *Service) purposeReply(ctx context.Context, sess *session, text string, history []conversation.Message) (string, bool) {
	// Oversized messages never enter the reasoning loop. We ask for a concise
	// studio question without echoing or attempting to solve their content.
	if utf8.RuneCountInString(text) > 4000 {
		sess.offerFixed(offerMenu)
		return purposeText(sess.language), true
	}
	checker, ok := s.ai.(ai.PurposeChecker)
	if !ok {
		return "", false
	}
	purpose, err := checker.CheckPurpose(ctx, boundedPurposeMessages(history))
	if err != nil {
		s.logger.WarnContext(ctx, "purpose check unavailable", "conversation_id", sess.conv.ID)
		sess.offerFixed(offerHelp)
		return speak(sess.language).apology, true
	}
	if purpose == ai.PurposeUnrelated {
		s.logger.InfoContext(ctx, "declined unrelated request", "conversation_id", sess.conv.ID)
		sess.offerFixed(offerMenu)
		return purposeText(sess.language), true
	}
	if purpose == ai.PurposeAppointments && s.identity != nil {
		if sess.customer.VerifiedPhone == "" {
			return s.tools.verificationPrompt(ctx, sess), true
		}
		return s.appointmentList(ctx, sess), true
	}
	return "", false
}

// A second guard keeps executable/code answers out of customer delivery even
// when a mixed message was admitted for its legitimate studio question.
func outsidePurposeReply(text string) bool {
	return strings.Contains(text, "```") || strings.Contains(text, "func main(") || strings.Contains(text, "def main(")
}
