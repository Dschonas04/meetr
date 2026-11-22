// Meetr: Videokonferenzen im Browser. Räume per Link, Video und Ton über
// WebRTC direkt zwischen den Teilnehmern (Mesh), der Server vermittelt nur die
// Verbindung über WebSocket. Gäste ohne Konto warten, bis jemand mit Konto sie
// hereinlässt.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	admins []string // Vorgabe des Betreibers
	db     *pgxpool.Pool
	sso    *SSO
	v      Vorlagen
	hub    *Hub
	ice    json.RawMessage
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &App{
		db:  datenbank(ctx, umgebung("MEETR_DB", "postgres://meetr:meetr@localhost:5432/meetr?sslmode=disable")),
		sso: NeuesSSO("Meetr", umgebung("MEETR_SSO_DATEI", "./einstellungen/sso.json"), "MEETR_SSO_AUS"),
		v:   ladeVorlagen(),
		hub: neuerHub(),
		ice: json.RawMessage("[]"),
	}
	if s := os.Getenv("MEETR_ICE"); s != "" {
		if !json.Valid([]byte(s)) {
			log.Fatal("MEETR_ICE ist kein gültiges JSON")
		}
		a.ice = json.RawMessage(s)
	}
	// Räume und ihre Verbindung prüfen selbst, ob Gäste erlaubt sind.
	a.admins = adminListe(umgebung("MEETR_ADMIN", ""))
	a.sso.ErstesKonto = a.ersterAdmin
	a.sso.Frei = []string{"/statisch/", "/healthz", "/r/", "/ws/"}

	adresse := ":" + umgebung("MEETR_PORT", "8370")
	srv := &http.Server{Addr: adresse, Handler: a.wege(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("Meetr %s lauscht auf %s", version, adresse)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	ende, abbruch := context.WithTimeout(context.Background(), 10*time.Second)
	defer abbruch()
	srv.Shutdown(ende)
}

func (a *App) wege() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", gesund)
	m.HandleFunc("GET /favicon.svg", logo)
	m.HandleFunc("GET /favicon.ico", logo)
	m.Handle("GET /statisch/", statisch())
	a.sso.Wege(m, a.angemeldet)
	m.HandleFunc("GET /{$}", a.start)
	m.HandleFunc("POST /raum", a.raumAnlegen)
	m.HandleFunc("POST /raum/{id}/loeschen", a.raumLoeschen)
	m.HandleFunc("GET /r/{kennung}", a.raum)
	m.HandleFunc("GET /ws/{kennung}", a.verbindung)
	inner := nurAngemeldet(a.sso, m)
	return sicherheitskopf(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Kamera und Mikrofon nur für diese Seite selbst.
		w.Header().Set("Permissions-Policy", "camera=(self), microphone=(self), display-capture=(self)")
		inner.ServeHTTP(w, r)
	}))
}

func (a *App) angemeldet(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Anmeldung und Konten verwalten nur Admins; vor der Einrichtung, wer
		// den Code aus dem Log hat.
		if !a.sso.Geschuetzt() && a.sso.EinrichtungErlaubt(w, r) {
			h(w, r)
			return
		}
		if _, ok := a.sso.Wer(r); !ok {
			http.Error(w, "Bitte anmelden", http.StatusUnauthorized)
			return
		}
		if !a.istAdmin(r) {
			http.Error(w, "Nur für Admins", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (a *App) daten(r *http.Request, titel, nav string, d any) Daten {
	n, _ := a.sso.Wer(r)
	return Daten{App: "Meetr", Titel: titel, Nav: nav, Nutzer: n, Admin: a.istAdmin(r), D: d}
}

// wer: angemeldet (mit Kennung und Name) oder Gast.
func (a *App) wer(r *http.Request) (kennung, name string, ok bool) {
	n, ok := a.sso.Wer(r)
	if !ok {
		if a.sso.notaus {
			return "notaus", "Gastgeber", true
		}
		return "", "", false
	}
	kennung = n.Kennung
	if kennung == "" {
		kennung = "lokal:" + n.Nutzer
	}
	name = n.Name
	if name == "" {
		name = n.Nutzer
	}
	return kennung, name, true
}

const zeichen = "abcdefghjkmnpqrstuvwxyz23456789"

// neueKennung: drei Gruppen zu vier Zeichen, etwa kq7m-x2ra-9fhd. Gut
// vorzulesen und mit rund 60 Bit nicht zu erraten.
func neueKennung() string {
	var b strings.Builder
	for i := 0; i < 12; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(zeichen))))
		b.WriteByte(zeichen[n.Int64()])
	}
	return b.String()
}
