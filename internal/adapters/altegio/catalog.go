package altegio

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

// ListServices returns the services customers can book.
//
// Services Altegio marks inactive are dropped here rather than passed on: the
// assistant must not be able to offer something the business has withdrawn,
// and filtering at the boundary means no later stage has to remember to.
func (c *Client) ListServices(ctx context.Context) ([]booking.Service, error) {
	return c.ListServicesForStaff(ctx, "")
}

// ListServicesForStaff reads the services this specialist can actually perform,
// including their duration and price. The unfiltered catalogue can contain a
// service another specialist offers and returns null durations on real accounts.
func (c *Client) ListServicesForStaff(ctx context.Context, staffID string) ([]booking.Service, error) {
	query := url.Values{}
	if staffID != "" {
		query.Set("staff_id", staffID)
	}
	catalogue, err := call[serviceCatalogue](ctx, c, request{
		method:     http.MethodGet,
		path:       withQuery("/book_services/"+c.companyID, query),
		repeatable: true,
	})
	if err != nil {
		return nil, err
	}

	categories := make(map[int64]string, len(catalogue.Categories))
	for _, category := range catalogue.Categories {
		categories[category.ID] = category.Title
	}

	services := make([]booking.Service, 0, len(catalogue.Services))
	for _, dto := range catalogue.Services {
		if dto.Active == 0 {
			continue
		}
		services = append(services, booking.Service{
			ID:          strconv.FormatInt(dto.ID, 10),
			Name:        dto.Title,
			Category:    categories[dto.CategoryID],
			Description: serviceDescription(dto),
			Duration:    time.Duration(dto.SeanceLength) * time.Second,
			PriceMin:    dto.PriceMin,
			PriceMax:    dto.PriceMax,
			Currency:    c.currency,
		})
	}
	return services, nil
}

func serviceDescription(dto serviceDTO) string {
	// Prefer the documented Online Booking field. The fallback costs nothing and
	// keeps compatibility with response variants observed in older accounts.
	raw := strings.TrimSpace(dto.Comment)
	if raw == "" {
		raw = strings.TrimSpace(dto.Description)
	}
	if raw == "" {
		return ""
	}

	// Some catalogue editors store rich text. Messaging channels need plain
	// text; a small tolerant stripper is enough here and avoids leaking tags
	// into a customer's service list.
	var plain strings.Builder
	inTag := false
	for _, r := range html.UnescapeString(raw) {
		switch r {
		case '<':
			inTag = true
			plain.WriteByte('\n')
		case '>':
			inTag = false
			plain.WriteByte('\n')
		default:
			if !inTag {
				plain.WriteRune(r)
			}
		}
	}
	lines := make([]string, 0)
	for _, line := range strings.Split(plain.String(), "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// ListStaff returns the people who can perform services.
//
// Anyone Altegio reports as no longer working at the business is dropped, and
// anyone it reports as not bookable is returned marked so rather than removed,
// because the assistant may still need to explain that a named person is
// unavailable rather than pretend they do not exist.
func (c *Client) ListStaff(ctx context.Context) ([]booking.Staff, error) {
	return c.ListStaffForServices(ctx, nil)
}

// ListStaffForServices limits the choice to specialists qualified for every
// selected service. A person being bookable in general does not establish that.
func (c *Client) ListStaffForServices(ctx context.Context, serviceIDs []string) ([]booking.Staff, error) {
	query, err := serviceQuery(serviceIDs)
	if err != nil {
		return nil, err
	}
	dtos, err := call[[]staffDTO](ctx, c, request{
		method:     http.MethodGet,
		path:       withQuery("/book_staff/"+c.companyID, query),
		repeatable: true,
	})
	if err != nil {
		return nil, err
	}

	staff := make([]booking.Staff, 0, len(dtos))
	for _, dto := range dtos {
		if dto.Fired != 0 {
			continue
		}

		// A missing bookable flag is treated as bookable. Availability is read
		// separately and will be empty for anyone who is not.
		bookable := true
		if dto.Bookable != nil {
			bookable = *dto.Bookable
		}

		staff = append(staff, booking.Staff{
			ID:             strconv.FormatInt(dto.ID, 10),
			Name:           dto.Name,
			Specialisation: dto.Specialization,
			Bookable:       bookable,
		})
	}
	return staff, nil
}
