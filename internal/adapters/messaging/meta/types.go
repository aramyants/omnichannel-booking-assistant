package meta

// The types in this file mirror the Meta webhook and Graph API wire formats.
// They are unexported: nothing outside this package should depend on how Meta
// shapes its payloads.
//
// One envelope covers all three channels. What differs is which member of the
// change value is populated, which is why parsing is per channel.

// update is the top level of every webhook delivery.
type update struct {
	// Object names which platform sent it: whatsapp_business_account,
	// instagram or page.
	Object string  `json:"object"`
	Entry  []entry `json:"entry"`
}

type entry struct {
	ID string `json:"id"`

	// Changes carries WhatsApp and Instagram content.
	Changes []change `json:"changes"`

	// Messaging carries Messenger and Instagram Direct content, which Meta
	// shapes differently from WhatsApp for historical reasons.
	Messaging []messagingEvent `json:"messaging"`
}

type change struct {
	Field string      `json:"field"`
	Value changeValue `json:"value"`
}

type changeValue struct {
	MessagingProduct string           `json:"messaging_product"`
	Metadata         metadata         `json:"metadata"`
	Contacts         []contact        `json:"contacts"`
	Messages         []inboundMessage `json:"messages"`

	// Statuses are delivery and read receipts for messages this system sent.
	// They arrive on the same webhook as real messages and are not ones.
	Statuses []json0 `json:"statuses"`
}

// json0 is a payload this system deliberately ignores. Naming it keeps the
// decision visible rather than leaving a silently absent field.
type json0 struct{}

type metadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type contact struct {
	WaID    string `json:"wa_id"`
	Profile struct {
		Name string `json:"name"`
	} `json:"profile"`
}

type inboundMessage struct {
	From string `json:"from"`
	ID   string `json:"id"`

	// Timestamp is unix seconds as a string, which is how Meta sends it.
	Timestamp string `json:"timestamp"`

	Type string `json:"type"`
	Text struct {
		Body string `json:"body"`
	} `json:"text"`

	// Caption-bearing attachments, declared so an unreadable message can be
	// described to the customer rather than silently dropped.
	Image    *mediaWithCaption `json:"image"`
	Video    *mediaWithCaption `json:"video"`
	Document *mediaWithCaption `json:"document"`
	Audio    *media            `json:"audio"`
	Voice    *media            `json:"voice"`
	Sticker  *media            `json:"sticker"`
	Location *json0            `json:"location"`
	Contacts []json0           `json:"contacts"`
}

type media struct {
	ID string `json:"id"`
}

type mediaWithCaption struct {
	ID      string `json:"id"`
	Caption string `json:"caption"`
}

// messagingEvent is the Messenger and Instagram Direct shape.
type messagingEvent struct {
	Sender    party `json:"sender"`
	Recipient party `json:"recipient"`

	// Timestamp is unix milliseconds here, unlike WhatsApp's seconds.
	Timestamp int64 `json:"timestamp"`

	Message *struct {
		MID         string  `json:"mid"`
		Text        string  `json:"text"`
		Attachments []json0 `json:"attachments"`

		// IsEcho marks a message this system sent, echoed back. Treating one
		// as a customer message would have the assistant answer itself.
		IsEcho bool `json:"is_echo"`
	} `json:"message"`
}

type party struct {
	ID string `json:"id"`
}

// sendTextRequest is a WhatsApp Cloud API text message.
type sendTextRequest struct {
	MessagingProduct string `json:"messaging_product"`
	RecipientType    string `json:"recipient_type"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Text             struct {
		PreviewURL bool   `json:"preview_url"`
		Body       string `json:"body"`
	} `json:"text"`
}

// graphError is how the Graph API reports a refusal.
type graphError struct {
	Error struct {
		Message   string `json:"message"`
		Type      string `json:"type"`
		Code      int    `json:"code"`
		Subcode   int    `json:"error_subcode"`
		UserTitle string `json:"error_user_title"`
		UserMsg   string `json:"error_user_msg"`
	} `json:"error"`
}
