package booking

import (
	"errors"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func validRequest() Request {
	return Request{
		IdempotencyKey: "01a05d0c-df89-786b-a3cb-0d30ff15bf28",
		CustomerID:     "cust-1",
		CustomerName:   "Anna Petrosyan",
		Phone:          "+37411223344",
		ServiceIDs:     []string{"1001"},
		StaffID:        "501",
		StartsAt:       now.Add(48 * time.Hour),
		Duration:       time.Hour,
	}
}

func TestRequestValidate(t *testing.T) {
	if err := validRequest().Validate(now); err != nil {
		t.Fatalf("a complete request was rejected: %v", err)
	}

	tests := map[string]func(*Request){
		"no idempotency key": func(r *Request) { r.IdempotencyKey = "" },
		"no customer":        func(r *Request) { r.CustomerID = "" },
		"no phone number":    func(r *Request) { r.Phone = "" },
		"no service":         func(r *Request) { r.ServiceIDs = nil },
		"no staff member":    func(r *Request) { r.StaffID = "" },
		"no start time":      func(r *Request) { r.StartsAt = time.Time{} },
		"no duration":        func(r *Request) { r.Duration = 0 },

		// A booking in the past is always a bug or a misread date, and it must
		// be refused in code rather than left to whatever the model produced.
		"a start time in the past": func(r *Request) { r.StartsAt = now.Add(-time.Hour) },
		"a start time of now":      func(r *Request) { r.StartsAt = now },
	}

	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			req := validRequest()
			breakIt(&req)

			err := req.Validate(now)
			if err == nil {
				t.Fatal("Validate() accepted an incomplete request")
			}
			if !errors.Is(err, ErrRejected) {
				t.Errorf("error %v does not wrap ErrRejected", err)
			}
		})
	}
}

func TestServicePriceLabel(t *testing.T) {
	tests := map[string]struct {
		service Service
		want    string
	}{
		"a range":       {Service{PriceMin: 8000, PriceMax: 12000, Currency: "AMD"}, "8000-12000 AMD"},
		"a fixed price": {Service{PriceMin: 5000, PriceMax: 5000, Currency: "AMD"}, "5000 AMD"},
		"no price set":  {Service{Currency: "AMD"}, ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.service.PriceLabel(); got != tt.want {
				t.Errorf("PriceLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSlotEnd(t *testing.T) {
	slot := Slot{Start: now, Duration: 90 * time.Minute}
	if got, want := slot.End(), now.Add(90*time.Minute); !got.Equal(want) {
		t.Errorf("End() = %s, want %s", got, want)
	}
}

func TestBookingEndsAt(t *testing.T) {
	b := Booking{StartsAt: now, Duration: time.Hour}
	if got, want := b.EndsAt(), now.Add(time.Hour); !got.Equal(want) {
		t.Errorf("EndsAt() = %s, want %s", got, want)
	}
}
