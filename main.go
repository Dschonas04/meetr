// Meetr: Videokonferenzen im Browser. Räume per Link, Video und Ton über
// WebRTC direkt zwischen den Teilnehmern (Mesh), der Server vermittelt nur die
// Verbindung über WebSocket. Gäste ohne Konto warten, bis jemand mit Konto sie
// hereinlässt.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
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
	// verwaltung: Änderungen an Konten, Rollen und Anmeldung laufen nacheinander.
	verwaltung Sperre
	admins     []string // Vorgabe des Betreibers
	db         *pgxpool.Pool
	sso        *SSO
	v          Vorlagen
	hub        *Hub
	ice        json.RawMessage
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
	a.sso.KontoWeg = a.adminEntziehen
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
	// Anmeldung und Konten verwalten nur Admins; vor der Einrichtung, wer den
	// Code aus dem Log hat.
	darf := func(w http.ResponseWriter, r *http.Request) bool {
		if !a.sso.Geschuetzt() && a.sso.EinrichtungErlaubt(w, r) {
			return true
		}
		if _, ok := a.sso.Wer(r); !ok {
			http.Error(w, "Bitte anmelden", http.StatusUnauthorized)
			return false
		}
		if !a.istAdmin(r) {
			http.Error(w, "Nur für Admins", http.StatusForbidden)
			return false
		}
		return true
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !inhaltZuerst(w, r) {
			return
		}
		// Lesen und Testen ändern nichts und brauchen keine Sperre.
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.URL.Path == "/api/sso/testen" {
			if darf(w, r) {
				h(w, r)
			}
			return
		}
		// Der Anbieter wird vor der Sperre gefragt, und nur von Admins.
		if r.URL.Path == "/api/sso/einstellungen" {
			if !darf(w, r) {
				return
			}
			roh, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Die Anfrage ist unvollständig", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(roh))
			var ok bool
			if r, ok = a.sso.ausstellerVorab(w, r, roh); !ok {
				return
			}
		}
		// Erst sperren, dann prüfen: bis zum Speichern kann niemand die Rechte
		// entziehen, auf die sich die Prüfung stützt.
		a.verwaltung.fuehreAus(w, r, func(w http.ResponseWriter, r *http.Request) {
			if darf(w, r) {
				h(w, r)
			}
		})
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
