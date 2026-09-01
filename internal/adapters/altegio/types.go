package altegio

// The types in this file mirror the Altegio wire format and are unexported: no
// caller should depend on how Altegio shapes its payloads.
//
// The field names follow the published API documentation. Response bodies are
// not fully specified there, so the mapping is deliberately tolerant: a field
// Altegio omits or renames becomes a zero value rather than a failed parse, and
// the fixture tests in this package are the place to correct any difference
// found against a live account.

// serviceCatalogue is the payload of book_services.
type serviceCatalogue struct {
	Services   []serviceDTO  `json:"services"`
	Categories []categoryDTO `json:"category"`
}

type serviceDTO struct {
	ID         int64   `json:"id"`
	Title      string  `json:"title"`
	CategoryID int64   `json:"category_id"`
	PriceMin   float64 `json:"price_min"`
	PriceMax   float64 `json:"price_max"`

	// SeanceLength is the appointment duration in seconds.
	SeanceLength int64 `json:"seance_length"`

	// Active is 0 for a service that is not offered. Altegio sends it as a
	// number rather than a boolean.
	Active int `json:"active"`
}

type categoryDTO struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type staffDTO struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Specialization string `json:"specialization"`

	// Bookable reports whether this person accepts appointments. Altegio omits
	// it in some responses, so the mapping treats a missing value as bookable
	// and relies on availability being empty for anyone who is not.
	Bookable *bool `json:"bookable"`

	// Fired marks a staff member who has left. Sent as a number.
	Fired int `json:"fired"`
}

// bookingDates is the payload of book_dates.
type bookingDates struct {
	// BookingDates are the days with at least one free slot, as YYYY-MM-DD.
	BookingDates []string `json:"booking_dates"`

	WorkingDates []string `json:"working_dates"`
}

// slotDTO is one entry from book_times.
type slotDTO struct {
	Time string `json:"time"`

	// Datetime is the fully qualified start, carrying the location's UTC
	// offset. It is preferred over Time, which has no date and no zone.
	Datetime string `json:"datetime"`

	SeanceLength int64 `json:"seance_length"`
}

// appointmentRequest is one appointment inside a book_check or book_record
// call.
type appointmentRequest struct {
	// ID numbers appointments within a single request. Altegio requires it
	// even when only one appointment is being made.
	ID int64 `json:"id"`

	Services []int64 `json:"services"`
	StaffID  int64   `json:"staff_id"`
	Datetime string  `json:"datetime"`
}

type bookCheckRequest struct {
	Appointments []appointmentRequest `json:"appointments"`
}

type bookRecordRequest struct {
	Phone    string `json:"phone"`
	FullName string `json:"full_name"`
	Email    string `json:"email,omitempty"`
	Comment  string `json:"comment,omitempty"`

	Appointments []appointmentRequest `json:"appointments"`

	// APIID is this system's own identifier for the request. Altegio stores it
	// against the appointment, which is what makes a repeated submission
	// recognisable rather than a second booking.
	APIID string `json:"api_id"`

	NotifyBySMS   int `json:"notify_by_sms"`
	NotifyByEmail int `json:"notify_by_email"`
}

// recordDTO is one created appointment.
type recordDTO struct {
	ID         int64  `json:"id"`
	RecordID   int64  `json:"record_id"`
	RecordHash string `json:"record_hash"`
}
