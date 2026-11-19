package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Jede Seite muss sich mit typischen Daten rendern lassen.
func TestSeitenRendern(t *testing.T) {
	v := ladeVorlagen()
	x := Raum{ID: "1", Kennung: "abcd-efgh-jkmn", Name: "Wochenrunde", Gastgeber: "lokal:jonas", GastgeberName: "Jonas", Gaeste: true, Erstellt: time.Now(), Jetzt: 2}
	faelle := map[string]any{
		"start": map[string]any{"Raeume": []Raum{x}, "Basis": "https://10.0.2.7:8370"},
		"raum": map[string]any{"R": x, "Angemeldet": true, "Name": "Jonas", "Gastgeber": true,
			"ICE": json.RawMessage(`[{"urls":"stun:stun.example:3478"}]`), "Link": "https://10.0.2.7:8370/r/abcd-efgh-jkmn"},
		"fehlt": nil,
	}
	for name, d := range faelle {
		rec := httptest.NewRecorder()
		v.zeigen(rec, httptest.NewRequest("GET", "/", nil), name, Daten{App: "Meetr", Titel: "Wochenrunde", D: d})
		b := rec.Body.String()
		if !strings.Contains(b, "</html>") || strings.Contains(b, "Vorlage") {
			t.Errorf("%s: %s", name, b)
		}
		if name != "fehlt" && !strings.Contains(b, "Wochenrunde") {
			t.Errorf("%s: Raumname fehlt", name)
		}
	}
}

func TestKennungUndSauber(t *testing.T) {
	k := neueKennung()
	if len(k) != 14 || k[4] != '-' || k[9] != '-' {
		t.Errorf("Kennung %q", k)
	}
	if s := sauber("  a\x00b\nc  ", 40); s != "abc" {
		t.Errorf("sauber %q", s)
	}
	if s := sauber(strings.Repeat("ä", 50), 10); len(s) > 10 {
		t.Errorf("zu lang %q", s)
	}
}
