// Package calendar provides private, read-only calendar exports. Links grant
// access to one appointment, never to customer data or booking management.
package calendar

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/google/uuid"
)

type Repository interface {
	FindBooking(context.Context, string) (booking.Booking, error)
}
type Reader interface {
	ReadBooking(context.Context, booking.Booking) (booking.Booking, error)
}
type Settings struct {
	Name      string
	Addresses map[string]string
	Location  *time.Location
}
type Handler struct {
	reader   Reader
	store    Repository
	settings Settings
	now      func() time.Time
}

func (h *Handler) WithReader(reader Reader) *Handler { h.reader = reader; return h }

func New(store Repository, settings Settings) *Handler {
	if settings.Location == nil {
		settings.Location = time.UTC
	}
	return &Handler{store: store, settings: settings, now: time.Now}
}

// Booking.ID is an unexposed cryptographically random UUID, not the sequential
// external reference. Domain separation and HMAC prevent exposing that ID or
// the provider's management token. Rescheduling preserves this capability.
func capability(b booking.Booking) string {
	id, err := uuid.Parse(b.ID)
	if err != nil || id.Version() != 4 {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(b.ID))
	_, _ = mac.Write([]byte("calendar-read-v1:" + b.ExternalID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func Link(base string, b booking.Booking, lang string) string {
	origin, err := url.Parse(base)
	token := capability(b)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || token == "" || b.Duration <= 0 || b.Status != booking.StatusConfirmed {
		return ""
	}
	origin.Path = "/calendar/" + url.PathEscape(b.ExternalID)
	origin.RawQuery = url.Values{"key": {token}, "lang": {language(lang)}}.Encode()
	origin.Fragment = ""
	return origin.String()
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	reference := r.PathValue("reference")
	key := r.URL.Query().Get("key")
	if len(reference) > 80 || len(key) != 43 {
		http.NotFound(w, r)
		return
	}
	b, err := h.store.FindBooking(r.Context(), reference)
	if err != nil {
		if errors.Is(err, booking.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "Please try again shortly.", http.StatusServiceUnavailable)
		}
		return
	}
	expected := capability(b)
	if expected == "" || !hmac.Equal([]byte(key), []byte(expected)) {
		http.NotFound(w, r)
		return
	}
	lang := language(r.URL.Query().Get("lang"))
	p := words(lang)
	if h.reader != nil {
		b, err = h.reader.ReadBooking(r.Context(), b)
		if errors.Is(err, booking.ErrNotFound) {
			http.Error(w, p.expired, http.StatusGone)
			return
		}
		if err != nil {
			http.Error(w, "Please try again shortly.", http.StatusServiceUnavailable)
			return
		}
	}
	if b.Status != booking.StatusConfirmed || b.Duration <= 0 || h.now().After(b.StartsAt.Add(b.Duration).Add(24*time.Hour)) {
		http.Error(w, p.expired, http.StatusGone)
		return
	}
	title := strings.TrimSpace(h.settings.Name + " — " + strings.Join(b.ServiceNames, ", "))
	address := h.settings.Addresses[lang]
	description := p.note
	if b.StaffName != "" {
		description = b.StaffName + "\n" + description
	}
	if r.URL.Query().Get("format") == "ics" {
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="appointment.ics"`)
		_, _ = w.Write([]byte(ICS(b, title, address, description, h.now())))
		return
	}
	google := url.Values{"action": {"TEMPLATE"}, "text": {title}, "dates": {stamp(b.StartsAt) + "/" + stamp(b.StartsAt.Add(b.Duration))}, "details": {description}, "location": {address}}
	outlook := url.Values{"path": {"/calendar/action/compose"}, "rru": {"addevent"}, "subject": {title}, "startdt": {b.StartsAt.UTC().Format(time.RFC3339)}, "enddt": {b.StartsAt.Add(b.Duration).UTC().Format(time.RFC3339)}, "body": {description}, "location": {address}}
	download := *r.URL
	q := download.Query()
	q.Set("format", "ics")
	download.RawQuery = q.Encode()
	data := pageData{Lang: lang, Name: h.settings.Name, Title: title, Heading: p.heading, When: b.StartsAt.In(h.settings.Location).Format("02.01.2006 · 15:04"), Zone: h.settings.Location.String(), Address: address, Google: "https://calendar.google.com/calendar/render?" + google.Encode(), Outlook: "https://outlook.live.com/calendar/0/deeplink/compose?" + outlook.Encode(), Office: "https://outlook.office.com/calendar/0/deeplink/compose?" + outlook.Encode(), Download: download.String(), DownloadLabel: p.download, Note: p.note, Help: p.help}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, data)
}

func language(s string) string {
	s = strings.ToLower(strings.Split(s, "-")[0])
	if s == "ru" || s == "hy" {
		return s
	}
	return "en"
}

type copyWords struct{ heading, download, note, help, expired string }

func words(lang string) copyWords {
	switch lang {
	case "ru":
		return copyWords{"Время для себя", "Apple Calendar / скачать .ics", "Добавление в календарь не создаёт новую запись. Если визит перенесён или отменён, обновите событие в своём календаре — импортированная копия не синхронизируется автоматически.", "Выберите календарь и сохраните событие. Если ссылка не открывается внутри чата, откройте эту страницу в браузере. Для другого приложения скачайте .ics и откройте его в своём календаре. Напоминания календаря зависят от настроек вашего устройства.", "Эта запись отменена или уже завершена. Уточните детали в чате со студией."}
	case "hy":
		return copyWords{"Ժամանակ՝ ձեզ համար", "Apple Calendar / ներբեռնել .ics", "Օրացույցում ավելացնելը նոր ամրագրում չի ստեղծում։ Այցը փոխելու կամ չեղարկելու դեպքում թարմացրեք նաև ձեր օրացույցը․ ներմուծված իրադարձությունն ինքնաբերաբար չի թարմացվում։", "Ընտրեք օրացույցը և պահպանեք իրադարձությունը։ Եթե հղումը չատում չի բացվում, բացեք էջը բրաուզերում։ Այլ հավելվածի համար ներբեռնեք .ics ֆայլը և բացեք օրացույցում։ Հիշեցումները կախված են ձեր սարքի կարգավորումներից։", "Այս ամրագրումը չեղարկված է կամ ավարտված։ Մանրամասները ճշտեք ստուդիայի հետ չատում։"}
	default:
		return copyWords{"A little time for you", "Apple Calendar / download .ics", "Adding this event does not create another booking. If your visit changes or is cancelled, update your calendar too: imported events do not sync automatically.", "Choose your calendar and save the event. If the link does not open inside your chat app, open this page in your browser. For another calendar, download the .ics file and open it in that app. Calendar alerts depend on your device settings.", "This appointment is cancelled or has ended. Please check with the studio in your chat."}
	}
}

func stamp(t time.Time) string { return t.UTC().Format("20060102T150405Z") }
func escape(s string) string {
	return strings.NewReplacer("\\", "\\\\", "\r\n", "\\n", "\r", "\\n", "\n", "\\n", ",", "\\,", ";", "\\;").Replace(s)
}

// ICS follows RFC 5545: UTC instants, CRLF, escaping and UTF-8-safe folding at
// 75 octets. No client name, phone number, private link or management proof.
func ICS(b booking.Booking, title, address, description string, now time.Time) string {
	uid := sha256.Sum256([]byte("calendar-uid:" + b.ID))
	lines := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Motion Concept//Appointments//EN", "CALSCALE:GREGORIAN", "BEGIN:VEVENT", fmt.Sprintf("UID:%x@motionconcept.rest", uid[:16]), "DTSTAMP:" + stamp(now), "DTSTART:" + stamp(b.StartsAt), "DTEND:" + stamp(b.StartsAt.Add(b.Duration)), "SUMMARY:" + escape(title), "LOCATION:" + escape(address), "DESCRIPTION:" + escape(description), "STATUS:CONFIRMED", "TRANSP:OPAQUE", "BEGIN:VALARM", "TRIGGER:-PT24H", "ACTION:DISPLAY", "DESCRIPTION:" + escape(title), "END:VALARM", "END:VEVENT", "END:VCALENDAR"}
	var out strings.Builder
	for _, line := range lines {
		for len(line) > 75 {
			cut := 75
			for !utf8.RuneStart(line[cut]) {
				cut--
			}
			out.WriteString(line[:cut] + "\r\n")
			line = " " + line[cut:]
		}
		out.WriteString(line + "\r\n")
	}
	return out.String()
}

type pageData struct{ Lang, Name, Title, Heading, When, Zone, Address, Google, Outlook, Office, Download, DownloadLabel, Note, Help string }

var page = template.Must(template.New("calendar").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow"><title>{{.Name}} · Calendar</title>
<style> *{box-sizing:border-box}body{margin:0;background:#f3f0e8;color:#243e33;font:17px/1.6 Georgia,'Times New Roman',serif}main{max-width:620px;margin:5vh auto;padding:32px 26px}header{font:13px/1.4 Verdana,sans-serif;letter-spacing:.15em;text-transform:uppercase;border-bottom:1px solid #adb8a8;padding-bottom:24px}h1{font-size:clamp(36px,8vw,58px);font-weight:400;line-height:1.08;letter-spacing:-.03em;margin:36px 0 28px}h2{font-size:22px;font-weight:400;margin:0 0 8px}.when{font-size:24px;margin:0}.meta{font:13px/1.6 Verdana,sans-serif;color:#526759;margin:5px 0 20px}nav{display:grid;gap:10px;margin:28px 0}a{display:block;padding:15px 18px;color:inherit;border:1px solid #738673;text-decoration:none;border-radius:3px;font:15px/1.5 Verdana,sans-serif;min-height:52px}a:first-child{background:#243e33;color:#fff}a:hover{outline:2px solid #738673;outline-offset:2px}a:focus-visible{outline:3px solid #ad652e;outline-offset:3px}.note{border-top:1px solid #adb8a8;padding-top:20px;font-size:15px}.help{font:13px/1.7 Verdana,sans-serif;color:#526759}@media(max-width:400px){main{padding:22px 18px;margin:0}h1{margin-top:28px}}</style></head>
<body><main><header>{{.Name}}</header><h1>{{.Heading}}</h1><section aria-label="Appointment"><h2>{{.Title}}</h2><p class="when">{{.When}}</p><p class="meta">{{.Zone}}{{if .Address}}<br>{{.Address}}{{end}}</p></section><nav aria-label="Calendar"><a href="{{.Google}}" rel="noreferrer">Google Calendar ↗</a><a href="{{.Download}}">{{.DownloadLabel}} ↓</a><a href="{{.Outlook}}" rel="noreferrer">Outlook.com ↗</a><a href="{{.Office}}" rel="noreferrer">Microsoft 365 ↗</a></nav><p class="help">{{.Help}}</p><p class="note">{{.Note}}</p></main></body></html>`))
