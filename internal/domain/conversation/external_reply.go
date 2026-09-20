package conversation

import "time"

// ObserveExternalReply takes over immediately for new manual activity. A late
// delivery from before an explicit resume is still transcript history, but it
// must not silently undo that resume. Replays are excluded by the repository.
func (c *Conversation) ObserveExternalReply(sentAt, receivedAt time.Time) {
	if !c.AssistantResumedAt.IsZero() && !sentAt.After(c.AssistantResumedAt) {
		return
	}
	c.ExternalReplyRevision++
	c.State = StateHumanActive
	c.UpdatedAt = receivedAt
	c.LastMessageAt = receivedAt
	c.LastChoiceMessageID = ""
	c.PendingChoiceMessageID, c.PendingChoiceEventID = "", ""
	c.PresentedChoices = nil
	// Staff may have changed the booking outside the assistant workflow.
	c.Draft, c.BookingChange = nil, nil
}
