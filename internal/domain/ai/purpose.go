package ai

import "context"

// PurposeChecker classifies a bounded message without solving it or using tools.
// Only studio requests reach the booking model and its scheduling capabilities.
type PurposeChecker interface {
	CheckPurpose(context.Context, []Message) (Purpose, error)
}

type Purpose string

const (
	PurposeStudio       Purpose = "studio"
	PurposeAppointments Purpose = "appointments"
	PurposeUnrelated    Purpose = "unrelated"
)
