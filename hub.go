package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"slices"
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

const (
	maxTeilnehmer = 12
	maxWartende   = 20
	warteschlange = 512
)

var reaktionen = []string{"👍", "👏", "😂", "❤️"}

type Nachricht struct {
	Typ   string          `json:"typ"`
	Von   string          `json:"von,omitempty"`
	An    string          `json:"an,omitempty"`
	Name  string          `json:"name,omitempty"`
	Text  string          `json:"text,omitempty"`
	Ja    bool            `json:"ja,omitempty"`
	Daten json.RawMessage `json:"daten,omitempty"`
	Liste []Person        `json:"liste,omitempty"`
	Wer   *Person         `json:"wer,omitempty"`
	ICE   json.RawMessage `json:"ice,omitempty"`
}

// Zustand, wie ihn die anderen sehen.
type Zustand struct {
	Mikro  bool `json:"mikro"`
	Kamera bool `json:"kamera"`
	Bild   bool `json:"bild"`
	Hand   bool `json:"hand"`
}

type Person struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Angemeldet bool    `json:"angemeldet"`
	Gastgeber  bool    `json:"gastgeber"`
	Zustand    Zustand `json:"zustand"`
}

// eimer begrenzt, wie schnell etwas geschieht.
type eimer struct {
	rest float64
	seit time.Time
}

func (e *eimer) nimm(jeSekunde, voll float64) bool {
	jetzt := time.Now()
	if e.seit.IsZero() {
		e.rest = voll
	} else {
		e.rest = min(voll, e.rest+jetzt.Sub(e.seit).Seconds()*jeSekunde)
	}
	e.seit = jetzt
	if e.rest < 1 {
		return false
	}
	e.rest--
	return true
}

type teilnehmer struct {
	Person
	kennung    string
	zugelassen bool
	tempo      eimer
	raus       chan Nachricht
	weg        context.CancelFunc
}

type raumZustand struct {
	mu       sync.Mutex
	alle     map[string]*teilnehmer
	zu       bool // gelöscht, hier kommt keiner mehr herein
	gesperrt bool // vom Gastgeber abgeschlossen, bis der Raum leer ist
	versuche eimer
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
	return len(z.anwesende())
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
	z.zu = true
	for _, t := range z.alle {
		t.weg()
	}
}

// kontoWeg trennt alle Verbindungen eines Kontos.
func (h *Hub) kontoWeg(kennung string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	var l []*raumZustand
	for _, z := range h.raeume {
		l = append(l, z)
	}
	h.mu.Unlock()
	for _, z := range l {
		z.mu.Lock()
		for _, t := range z.alle {
			if t.kennung == kennung {
				t.weg()
			}
		}
		z.mu.Unlock()
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

// anAlle: an alle Zugelassenen außer ausser.
func (z *raumZustand) anAlle(n Nachricht, ausser string) {
	for id, t := range z.alle {
		if t.zugelassen && id != ausser {
			t.schicken(n)
		}
	}
}

// herein: ab hier gehört t dazu und bekommt die ICE-Server samt Zugangsdaten.
func (a *App) herein(z *raumZustand, t *teilnehmer) {
	t.zugelassen = true
	t.schicken(Nachricht{Typ: "willkommen", Von: t.ID, Liste: z.anwesende(), ICE: a.ice, Ja: z.gesperrt})
	p := t.Person
	z.anAlle(Nachricht{Typ: "neu", Von: t.ID, Wer: &p}, t.ID)
}

// darfZulassen: Angemeldete, in einem abgeschlossenen Raum nur der Gastgeber.
func (z *raumZustand) darfZulassen(t *teilnehmer) bool {
	return t.zugelassen && (t.Gastgeber || (t.Angemeldet && !z.gesperrt))
}

// darfModerieren: der Gastgeber alle, Angemeldete die Gäste.
func darfModerieren(t, ziel *teilnehmer) bool {
	return t.ID != ziel.ID && (t.Gastgeber || (t.Angemeldet && !ziel.Angemeldet))
}

func zufallsID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// sauber: eine Zeile gültiges UTF-8, höchstens n Byte.
func sauber(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(strings.ToValidUTF8(s, "")))
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
	if _, _, ok := a.wer(r); !ok && !x.Gaeste {
		http.Error(w, "Bitte anmelden", http.StatusUnauthorized)
		return
	}
	// Accept prüft die Herkunft: nur Seiten von diesem Host dürfen verbinden.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, weg := context.WithCancel(r.Context())
	defer weg()
	absage := func(typ string) {
		sctx, ab := context.WithTimeout(ctx, 5*time.Second)
		defer ab()
		if wsjson.Write(sctx, c, Nachricht{Typ: typ}) == nil {
			c.Close(websocket.StatusPolicyViolation, typ)
		}
	}

	// Name und Kennwort kommen als erste Nachricht, nicht in der Adresse: die
	// stünde in jedem Zugriffslog.
	c.SetReadLimit(4 << 10)
	var hallo Nachricht
	hctx, hab := context.WithTimeout(ctx, 10*time.Second)
	err = wsjson.Read(hctx, c, &hallo)
	hab()
	if err != nil || hallo.Typ != "hallo" {
		return
	}

	// Erst jetzt gilt, was gelesen wird: seit dem Aufbau kann der Raum weg oder
	// das Konto entzogen sein.
	z := a.hub.raum(x.Kennung)
	if x, err = a.raumLaden(r, x.Kennung); err != nil {
		absage("weg-raum")
		return
	}
	kennung, name, angemeldet := a.wer(r)
	if !angemeldet {
		if !x.Gaeste {
			absage("anmelden")
			return
		}
		if name = sauber(hallo.Name, 40); name == "" {
			name = "Gast"
		}
		name += " (Gast)"
	} else if name = sauber(name, 80); name == "" {
		name = "Konto"
	}
	gastgeber := angemeldet && kennung == x.Gastgeber
	if x.Kennwort != "" && !gastgeber {
		z.mu.Lock()
		darf := z.versuche.nimm(1, 20)
		z.mu.Unlock()
		if !darf {
			absage("zu-oft")
			return
		}
		if len(hallo.Text) > 200 || !ssoPasswortPruefen(x.Kennwort, hallo.Text) {
			absage("kennwort")
			return
		}
	}
	c.SetReadLimit(64 << 10)

	t := &teilnehmer{Person: Person{ID: zufallsID(), Name: name, Angemeldet: angemeldet, Gastgeber: gastgeber},
		kennung: kennung, raus: make(chan Nachricht, warteschlange), weg: weg}

	z.mu.Lock()
	nein := ""
	drin := len(z.anwesende())
	switch {
	case z.zu:
		nein = "weg-raum"
	case z.gesperrt && !gastgeber:
		nein = "gesperrt"
	case drin >= maxTeilnehmer, !angemeldet && len(z.alle)-drin >= maxWartende:
		nein = "voll"
	default:
		z.alle[t.ID] = t
		if angemeldet {
			a.herein(z, t)
			for _, o := range z.alle {
				if !o.zugelassen && z.darfZulassen(t) {
					t.schicken(Nachricht{Typ: "anfrage", Von: o.ID, Name: o.Name})
				}
			}
		} else {
			t.schicken(Nachricht{Typ: "warten"})
			for _, o := range z.alle {
				if z.darfZulassen(o) {
					o.schicken(Nachricht{Typ: "anfrage", Von: t.ID, Name: t.Name})
				}
			}
		}
	}
	z.mu.Unlock()
	if nein != "" {
		absage(nein)
		return
	}

	defer func() {
		z.mu.Lock()
		delete(z.alle, t.ID)
		if t.zugelassen {
			z.anAlle(Nachricht{Typ: "weg", Von: t.ID}, t.ID)
			if len(z.anwesende()) == 0 {
				z.gesperrt = false
			}
		} else {
			z.anAlle(Nachricht{Typ: "erledigt", Von: t.ID}, t.ID)
		}
		z.mu.Unlock()
		c.Close(websocket.StatusNormalClosure, "")
	}()
	if angemeldet {
		if _, err := a.db.Exec(ctx, `UPDATE raeume SET zuletzt=now() WHERE id::text=$1`, x.ID); err != nil {
			log.Printf("Raum: %v", err)
		}
	}

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
		// Wer flutet, fliegt selbst, bevor die Warteschlange der anderen voll ist.
		if !t.tempo.nimm(40, warteschlange*3/4) {
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
	ziel := z.alle[n.An]
	switch n.Typ {
	case "signal":
		if ziel != nil && ziel.zugelassen {
			ziel.schicken(Nachricht{Typ: "signal", Von: t.ID, Daten: n.Daten})
		}
	case "chat":
		if text := sauber(n.Text, 2000); text != "" {
			z.anAlle(Nachricht{Typ: "chat", Von: t.ID, Name: t.Name, Text: text}, "")
		}
	case "zustand":
		// Gelesen und neu geschrieben, nicht durchgereicht.
		var neu Zustand
		if json.Unmarshal(n.Daten, &neu) == nil {
			t.Zustand = neu
			p := t.Person
			z.anAlle(Nachricht{Typ: "zustand", Von: t.ID, Wer: &p}, t.ID)
		}
	case "reaktion":
		if slices.Contains(reaktionen, n.Text) {
			z.anAlle(Nachricht{Typ: "reaktion", Von: t.ID, Text: n.Text}, "")
		}
	case "abschliessen":
		if t.Gastgeber {
			z.gesperrt = n.Ja
			z.anAlle(Nachricht{Typ: "abschliessen", Ja: z.gesperrt}, "")
		}
	case "zulassen", "ablehnen":
		if ziel == nil || ziel.zugelassen || !z.darfZulassen(t) {
			return
		}
		if n.Typ == "ablehnen" {
			ziel.schicken(Nachricht{Typ: "abgelehnt"})
			z.anAlle(Nachricht{Typ: "erledigt", Von: ziel.ID}, "")
			delete(z.alle, ziel.ID)
			time.AfterFunc(500*time.Millisecond, ziel.weg)
			return
		}
		if len(z.anwesende()) >= maxTeilnehmer {
			t.schicken(Nachricht{Typ: "kein-platz"})
			return
		}
		a.herein(z, ziel)
		z.anAlle(Nachricht{Typ: "erledigt", Von: ziel.ID}, ziel.ID)
	case "entfernen":
		if ziel == nil || !ziel.zugelassen || !darfModerieren(t, ziel) {
			return
		}
		ziel.schicken(Nachricht{Typ: "entfernt"})
		// Sofort draußen: ab jetzt reicht niemand mehr etwas von ihm weiter.
		ziel.zugelassen = false
		delete(z.alle, ziel.ID)
		z.anAlle(Nachricht{Typ: "weg", Von: ziel.ID}, "")
		time.AfterFunc(300*time.Millisecond, ziel.weg)
	case "stumm", "kamera-aus":
		if ziel != nil && ziel.zugelassen && darfModerieren(t, ziel) {
			ziel.schicken(Nachricht{Typ: n.Typ, Von: t.ID, Name: t.Name})
		}
	}
}
