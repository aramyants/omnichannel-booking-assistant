package altegio

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type nativeRecord struct {
	ID             int64          `json:"id"`
	CompanyID      int64          `json:"company_id"`
	Datetime       string         `json:"datetime"`
	Length         int64          `json:"seance_length"`
	TechnicalBreak int64          `json:"technical_break_duration"`
	Deleted        bool           `json:"deleted"`
	Created        string         `json:"create_date"`
	Changed        string         `json:"last_change_date"`
	APIID          nativeSourceID `json:"api_id"`
	Online         bool           `json:"online"`
	Attendance     int            `json:"attendance"`
	Client         *struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Phone string `json:"phone"`
	} `json:"client"`
	Staff struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"staff"`
	Services []struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	} `json:"services"`
}

// Altegio represents an absent API source as null, "", "0" or numeric zero.
// A real source must remain distinct from a native/admin appointment.
type nativeSourceID string

func (v *nativeSourceID) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		*v = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		var number json.Number
		if err = json.Unmarshal(raw, &number); err != nil {
			return err
		}
		text = number.String()
	}
	if text == "0" {
		text = ""
	}
	*v = nativeSourceID(text)
	return nil
}

// ReadNativeBooking is privileged and is never exposed as a customer AI tool.
func (c *Client) ReadNativeBooking(ctx context.Context, id string) (notifications.Snapshot, error) {
	numeric, err := strconv.ParseInt(id, 10, 64)
	if err != nil || numeric <= 0 {
		return notifications.Snapshot{}, booking.ErrNotFound
	}
	dto, err := call[nativeRecord](ctx, c, request{method: http.MethodGet, path: "/record/" + c.companyID + "/" + id, repeatable: true})
	if err != nil {
		return notifications.Snapshot{}, err
	}
	return c.nativeSnapshot(dto, numeric)
}

func (c *Client) nativeSnapshot(dto nativeRecord, numeric int64) (notifications.Snapshot, error) {
	if dto.ID != numeric || strconv.FormatInt(dto.CompanyID, 10) != c.companyID {
		return notifications.Snapshot{}, fmt.Errorf("%w: native appointment identity mismatch", booking.ErrUnavailable)
	}
	parse := func(raw string) time.Time {
		for _, format := range []string{time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			t, e := time.ParseInLocation(format, strings.TrimSpace(raw), c.location)
			if e == nil {
				return t.UTC()
			}
		}
		return time.Time{}
	}
	b := booking.Booking{ExternalID: strconv.FormatInt(numeric, 10), StartsAt: parse(dto.Datetime), Duration: time.Duration(dto.Length) * time.Second, CreatedAt: parse(dto.Created), StaffID: strconv.FormatInt(dto.Staff.ID, 10), StaffName: dto.Staff.Name, Status: booking.StatusConfirmed}
	if dto.TechnicalBreak > 0 && dto.TechnicalBreak < dto.Length {
		b.TreatmentDuration = time.Duration(dto.Length-dto.TechnicalBreak) * time.Second
	}
	if dto.Deleted {
		b.Status = booking.StatusCancelled
	}
	b.Attended = dto.Attendance == 1
	phone := ""
	if dto.Client != nil {
		b.CustomerName = dto.Client.Name
		phone = dto.Client.Phone
		if normalized, err := normalizeAltegioPhone(phone); err == nil {
			phone = normalized
		}
	}
	for _, service := range dto.Services {
		b.ServiceIDs = append(b.ServiceIDs, strconv.FormatInt(service.ID, 10))
		b.ServiceNames = append(b.ServiceNames, service.Title)
	}
	// Staff can reserve a real appointment before adding its treatment. The
	// calendar's time, duration and specialist still establish that appointment;
	// absent treatment names must stay absent in its customer-facing copy.
	if b.StartsAt.IsZero() || (!dto.Deleted && (dto.Length <= 0 || dto.Staff.ID <= 0)) {
		return notifications.Snapshot{}, fmt.Errorf("%w: incomplete native appointment", booking.ErrUnavailable)
	}
	source := string(dto.APIID)
	if source == "0" {
		source = ""
	}
	return notifications.Snapshot{Booking: b, Phone: phone, ChangedAt: parse(dto.Changed), APIID: source, Online: dto.Online, Attended: dto.Attendance == 1}, nil
}
