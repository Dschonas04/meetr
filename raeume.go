package main

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Raum struct {
	ID, Kennung, Name, Gastgeber, GastgeberName string
	Gaeste                                      bool
	Erstellt                                    time.Time
	Zuletzt                                     *time.Time
	Jetzt                                       int
}

func (a *App) raumLaden(r *http.Request, kennung string) (Raum, error) {
	var x Raum
	err := a.db.QueryRow(r.Context(), `SELECT id::text, kennung, name, gastgeber, gastgeber_name, gaeste, erstellt, zuletzt
		FROM raeume WHERE kennung=$1`, kennung).
		Scan(&x.ID, &x.Kennung, &x.Name, &x.Gastgeber, &x.GastgeberName, &x.Gaeste, &x.Erstellt, &x.Zuletzt)
	return x, err
}

func (a *App) start(w http.ResponseWriter, r *http.Request) {
	kennung, _, _ := a.wer(r)
	rows, err := a.db.Query(r.Context(), `SELECT id::text, kennung, name, gastgeber, gastgeber_name, gaeste, erstellt, zuletzt
		FROM raeume WHERE gastgeber=$1 ORDER BY COALESCE(zuletzt, erstellt) DESC`, kennung)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var l []Raum
	for rows.Next() {
		var x Raum
		if rows.Scan(&x.ID, &x.Kennung, &x.Name, &x.Gastgeber, &x.GastgeberName, &x.Gaeste, &x.Erstellt, &x.Zuletzt) == nil {
			x.Jetzt = a.hub.anzahl(x.Kennung)
			l = append(l, x)
		}
	}
	a.v.zeigen(w, r, "start", a.daten(r, "Räume", "start", map[string]any{"Raeume": l, "Basis": basis(r)}))
}

func basis(r *http.Request) string {
	schema := "http"
	if ssoSicher(r) {
		schema = "https"
	}
	return schema + "://" + r.Host
}

func (a *App) raumAnlegen(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	kennung, name, ok := a.wer(r)
	if !ok {
		http.Error(w, "Bitte anmelden", http.StatusUnauthorized)
		return
	}
	titel := strings.TrimSpace(r.FormValue("name"))
	if titel == "" {
		titel = "Besprechung"
	}
	if len(titel) > 100 {
		titel = titel[:100]
	}
	k := neueKennung()
	if _, err := a.db.Exec(r.Context(), `INSERT INTO raeume (kennung, name, gastgeber, gastgeber_name, gaeste) VALUES ($1, $2, $3, $4, $5)`,
		k, titel, kennung, name, r.FormValue("gaeste") == "ja"); err != nil {
		zurueck(w, r, "/", "Fehler: "+err.Error())
		return
	}
	http.Redirect(w, r, "/r/"+k, http.StatusSeeOther)
}

func (a *App) raumLoeschen(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	kennung, _, _ := a.wer(r)
	var k string
	if a.db.QueryRow(r.Context(), `DELETE FROM raeume WHERE id::text=$1 AND gastgeber=$2 RETURNING kennung`, r.PathValue("id"), kennung).Scan(&k) == nil {
		a.hub.schliessen(k)
	}
	zurueck(w, r, "/", "Raum gelöscht")
}

// raum: die Konferenz selbst. Ohne Anmeldung nur, wenn der Raum Gäste
// zulässt; sonst geht es zur Anmeldung und zurück.
func (a *App) raum(w http.ResponseWriter, r *http.Request) {
	x, err := a.raumLaden(r, r.PathValue("kennung"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		a.v.zeigen(w, r, "fehlt", a.daten(r, "Kein Raum", "", nil))
		return
	}
	kennung, name, angemeldet := a.wer(r)
	if !angemeldet && !x.Gaeste {
		http.Redirect(w, r, "/sso/seite?ziel="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	a.v.zeigen(w, r, "raum", a.daten(r, x.Name, "", map[string]any{
		"R": x, "Angemeldet": angemeldet, "Name": name, "Gastgeber": kennung == x.Gastgeber,
		"ICE": a.ice, "Link": basis(r) + "/r/" + x.Kennung,
	}))
}
