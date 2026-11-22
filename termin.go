package main

import (
	"net/http"
	"strings"
	"time"
)

// beginnLesen nimmt den Wert eines datetime-local-Felds, leer ist kein Termin.
func beginnLesen(s string) (*time.Time, bool) {
	if s = strings.TrimSpace(s); s == "" {
		return nil, true
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", s, time.Local)
	if err != nil || t.Year() < 2000 || t.Year() > 2200 {
		return nil, false
	}
	return &t, true
}

func icsText(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r", "", "\n", " ").Replace(s)
}

// termin gibt den Raum als Kalenderdatei aus, ohne das Kennwort.
func (a *App) termin(w http.ResponseWriter, r *http.Request) {
	x, err := a.raumLaden(r, r.PathValue("kennung"))
	if err != nil || x.Beginn == nil {
		http.NotFound(w, r)
		return
	}
	if _, _, ok := a.wer(r); !ok && !x.Gaeste {
		http.Error(w, "Bitte anmelden", http.StatusUnauthorized)
		return
	}
	const form = "20060102T150405Z"
	zeilen := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Meetr//DE", "BEGIN:VEVENT",
		"UID:" + x.ID + "@meetr", "DTSTAMP:" + x.Erstellt.UTC().Format(form),
		"DTSTART:" + x.Beginn.UTC().Format(form), "DURATION:PT1H",
		"SUMMARY:" + icsText(x.Name), "DESCRIPTION:" + icsText(basis(r)+"/r/"+x.Kennung),
		"END:VEVENT", "END:VCALENDAR", "",
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="termin.ics"`)
	if _, err := w.Write([]byte(strings.Join(zeilen, "\r\n"))); err != nil {
		return // die Gegenseite ist weg
	}
}
