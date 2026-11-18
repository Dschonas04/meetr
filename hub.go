package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Der Hub vermittelt je Raum zwischen den Teilnehmern: wer da ist, Angebote,
// Antworten und ICE-Kandidaten für WebRTC (signal), Chat, Wartebereich und
// Moderation. Video und Ton gehen nicht über den Server.

const maxTeilnehmer = 12

type Nachricht struct {
	Typ   string          `json:"typ"`
	Von   string          `json:"von,omitempty"`
	An    string          `json:"an,omitempty"`
	Name  string          `json:"name,omitempty"`
	Text  string          `json:"text,omitempty"`
	Daten json.RawMessage `json:"daten,omitempty"`
	Liste []Person        `json:"liste,omitempty"`
	ICE   json.RawMessage `json:"ice,omitempty"`
}

type Person struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Angemeldet bool   `json:"angemeldet"`
	Gastgeber  bool   `json:"gastgeber"`
}

type teilnehmer struct {
	Person
	zugelassen bool
	raus       chan Nachricht
	weg        context.CancelFunc
}

type raumZustand struct {
	mu   sync.Mutex
	alle map[string]*teilnehmer
}

type Hub struct {
	mu     sync.Mutex
	raeume map[string]*raumZustand
}

func neuerHub() *Hub { return &Hub{raeume: map[string]*raumZustand{}} }

func (h *Hub) raum(k string) *raumZustand {
	h.mu.Lock()
	defer h.mu.Unlock()
	z := h.raeume[k]
	if z == nil {
		z = &raumZustand{alle: map[string]*teilnehmer{}}
		h.raeume[k] = z
	}
	return z
}

func (h *Hub) anzahl(k string) int {
	h.mu.Lock()
	z := h.raeume[k]
	h.mu.Unlock()
	if z == nil {
		return 0
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	n := 0
	for _, t := range z.alle {
		if t.zugelassen {
			n++
		}
	}
	return n
}

func (h *Hub) schliessen(k string) {
	h.mu.Lock()
	z := h.raeume[k]
	delete(h.raeume, k)
	h.mu.Unlock()
	if z == nil {
		return
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	for _, t := range z.alle {
		t.weg()
	}
}

// schicken legt eine Nachricht in die Warteschlange eines Teilnehmers. Ist sie
// voll, ist die Verbindung zu langsam und fliegt.
func (t *teilnehmer) schicken(n Nachricht) {
	select {
	case t.raus <- n:
	default:
		t.weg()
	}
}

func (z *raumZustand) anwesende() []Person {
	var l []Person
	for _, t := range z.alle {
		if t.zugelassen {
			l = append(l, t.Person)
		}
	}
	return l
}

// anAlle: an alle Zugelassenen außer von.
func (z *raumZustand) anAlle(n Nachricht, ausser string) {
	for id, t := range z.alle {
		if t.zugelassen && id != ausser {
			t.schicken(n)
		}
	}
}

func zufallsID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sauber(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	for len(s) > n {
		_, g := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-g]
	}
	return s
}

func (a *App) verbindung(w http.ResponseWriter, r *http.Request) {
	x, err := a.raumLaden(r, r.PathValue("kennung"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	kennung, kontoName, angemeldet := a.wer(r)
	if !angemeldet && !x.Gaeste {
		http.Error(w, "Bitte anmelden", http.StatusUnauthorized)
		return
	}
	name := kontoName
	if !angemeldet {
		name = sauber(r.URL.Query().Get("name"), 40)
		if name == "" {
			name = "Gast"
		}
		name += " (Gast)"
	}
	// Accept prüft die Herkunft: nur Seiten von diesem Host dürfen verbinden.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(64 << 10)
	ctx, weg := context.WithCancel(r.Context())
	defer weg()

	z := a.hub.raum(x.Kennung)
	t := &teilnehmer{Person: Person{ID: zufallsID(), Name: name, Angemeldet: angemeldet, Gastgeber: angemeldet && kennung == x.Gastgeber},
		zugelassen: angemeldet, raus: make(chan Nachricht, 256), weg: weg}

	z.mu.Lock()
	voll := len(z.anwesende()) >= maxTeilnehmer
	if !voll {
		z.alle[t.ID] = t
		if t.zugelassen {
			t.schicken(Nachricht{Typ: "willkommen", Von: t.ID, Liste: z.anwesende(), ICE: a.ice})
			z.anAlle(Nachricht{Typ: "neu", Von: t.ID, Name: t.Name}, t.ID)
		} else {
			t.schicken(Nachricht{Typ: "warten"})
			for _, o := range z.alle {
				if o.zugelassen && o.Angemeldet {
					o.schicken(Nachricht{Typ: "anfrage", Von: t.ID, Name: t.Name})
				}
			}
		}
	}
	z.mu.Unlock()
	if voll {
		wsjson.Write(ctx, c, Nachricht{Typ: "voll"})
		c.Close(websocket.StatusPolicyViolation, "voll")
		return
	}
	if angemeldet {
		a.db.Exec(context.Background(), `UPDATE raeume SET zuletzt=now() WHERE id::text=$1`, x.ID)
	}

	defer func() {
		z.mu.Lock()
		delete(z.alle, t.ID)
		if t.zugelassen {
			z.anAlle(Nachricht{Typ: "weg", Von: t.ID}, t.ID)
		}
		z.mu.Unlock()
		c.Close(websocket.StatusNormalClosure, "")
	}()

	// Schreiben in eigener Goroutine; Lebenszeichen alle 25 Sekunden.
	go func() {
		takt := time.NewTicker(25 * time.Second)
		defer takt.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case n := <-t.raus:
				sctx, ab := context.WithTimeout(ctx, 10*time.Second)
				err := wsjson.Write(sctx, c, n)
				ab()
				if err != nil {
					weg()
					return
				}
			case <-takt.C:
				pctx, ab := context.WithTimeout(ctx, 10*time.Second)
				err := c.Ping(pctx)
				ab()
				if err != nil {
					weg()
					return
				}
			}
		}
	}()

	for {
		var n Nachricht
		if err := wsjson.Read(ctx, c, &n); err != nil {
			return
		}
		z.mu.Lock()
		a.behandeln(z, t, n)
		z.mu.Unlock()
	}
}

// behandeln läuft unter der Sperre des Raums.
func (a *App) behandeln(z *raumZustand, t *teilnehmer, n Nachricht) {
	if !t.zugelassen {
		return // im Wartebereich wird nichts weitergereicht
	}
	switch n.Typ {
	case "signal":
		if ziel := z.alle[n.An]; ziel != nil && ziel.zugelassen {
			ziel.schicken(Nachricht{Typ: "signal", Von: t.ID, Daten: n.Daten})
		}
	case "chat":
		if text := sauber(n.Text, 2000); text != "" {
			z.anAlle(Nachricht{Typ: "chat", Von: t.ID, Name: t.Name, Text: text}, "")
		}
	case "zustand": // Kamera/Mikro an oder aus, Hand gehoben: an alle
		if len(n.Daten) < 256 {
			z.anAlle(Nachricht{Typ: "zustand", Von: t.ID, Daten: n.Daten}, t.ID)
		}
	case "zulassen", "ablehnen":
		if !t.Angemeldet {
			return
		}
		g := z.alle[n.An]
		if g == nil || g.zugelassen {
			return
		}
		if n.Typ == "ablehnen" {
			g.schicken(Nachricht{Typ: "abgelehnt"})
			go func() { time.Sleep(500 * time.Millisecond); g.weg() }()
			return
		}
		if len(z.anwesende()) >= maxTeilnehmer {
			return
		}
		g.zugelassen = true
		g.schicken(Nachricht{Typ: "willkommen", Von: g.ID, Liste: z.anwesende(), ICE: a.ice})
		z.anAlle(Nachricht{Typ: "neu", Von: g.ID, Name: g.Name}, g.ID)
		for _, o := range z.alle {
			if o.zugelassen && o.Angemeldet && o.ID != g.ID {
				o.schicken(Nachricht{Typ: "erledigt", Von: g.ID})
			}
		}
	case "entfernen":
		// Nur der Gastgeber entfernt; Gäste nur von Angemeldeten.
		ziel := z.alle[n.An]
		if ziel == nil || ziel.ID == t.ID || !(t.Gastgeber || (t.Angemeldet && !ziel.Angemeldet)) {
			return
		}
		ziel.schicken(Nachricht{Typ: "entfernt"})
		go func() { time.Sleep(300 * time.Millisecond); ziel.weg() }()
	case "stumm":
		if t.Gastgeber || t.Angemeldet {
			if ziel := z.alle[n.An]; ziel != nil {
				ziel.schicken(Nachricht{Typ: "stumm", Von: t.ID, Name: t.Name})
			}
		}
	default:
		log.Printf("Meetr: unbekannte Nachricht %q", n.Typ)
	}
}
