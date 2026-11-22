package main

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// Die Schranke vor dem Kennwort eines Raums, mit drei Grenzen:
//   - je Konto oder Netz des Gastes (IPv6 als /64 und zusammen als /48), damit
//     Raten nur den Ratenden sperrt und ein Anschluss den Raum nicht leer zieht
//   - je Raum für unbekannte Gäste, die gilt auch bei gefälschter Adresse
//   - gleichzeitiges Hashen, mit Wartezeit statt endloser Schlange
//
// Konten und Netze, aus denen das Kennwort schon stimmte, trifft die Raumgrenze nicht.

const (
	versucheVoll      = 8
	versucheJeSekunde = 0.1
	raumVoll          = 60
	raumJeSekunde     = 1
	blockVoll         = 24
	blockJeSekunde    = 0.3 // weniger, als der Raum nachfüllt
	erholtNach        = versucheVoll / versucheJeSekunde * time.Second
	merkeHoechstens   = 1000
	hashWarten        = 5 * time.Second
)

// Konten haben eigene Plätze: Gäste können sie nicht verdrängen.
var (
	hashGaeste = make(chan struct{}, 4)
	hashKonten = make(chan struct{}, 2)
)

type schranke struct {
	mu    sync.Mutex
	je    map[string]*eimer
	richt map[string]time.Time // wo das Kennwort schon stimmte, getrennt von den Grenzen
	alle  eimer
	platz chan struct{} // je Raum zwei Gäste zugleich, damit keiner alle Plätze belegt
}

func werFragt(angemeldet bool, kennung, adresse string) string {
	if angemeldet {
		return "konto:" + kennung
	}
	if ip := net.ParseIP(adresse); ip != nil {
		if ip.To4() == nil {
			return "netz:" + ip.Mask(net.CIDRMask(64, 128)).String()
		}
		return "netz:" + ip.String()
	}
	return "netz:unbekannt"
}

// blockVon ist das /48 eines IPv6-Netzes, sonst leer.
func blockVon(wer string) string {
	a, ok := strings.CutPrefix(wer, "netz:")
	if ip := net.ParseIP(a); ok && ip != nil && ip.To4() == nil {
		return "block:" + ip.Mask(net.CIDRMask(48, 128)).String()
	}
	return ""
}

// eimerVon holt einen Eimer. Ist kein Platz, wird nur verdrängt, was ohnehin
// wieder voll ist: vergessen hieße sonst, eine Sperre aufzuheben.
func (s *schranke) eimerVon(wer string) *eimer {
	if e := s.je[wer]; e != nil {
		return e
	}
	if len(s.je) >= merkeHoechstens {
		for k, e := range s.je {
			if time.Since(e.seit) >= erholtNach {
				delete(s.je, k)
			}
		}
		if len(s.je) >= merkeHoechstens {
			return nil
		}
	}
	e := &eimer{}
	s.je[wer] = e
	return e
}

// nimm reserviert einen Versuch, richtig gibt ihn zurück.
func (s *schranke) nimm(wer string, konto bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.je == nil {
		s.je = map[string]*eimer{}
	}
	_, stimmte := s.richt[wer]
	// Erst nur nachsehen: wen die Raumgrenze abweist, der kostet keinen Speicher.
	if !konto && !stimmte {
		probe := s.alle
		if !probe.nimm(raumJeSekunde, raumVoll) {
			return false
		}
	}
	e := s.eimerVon(wer)
	if e == nil || !e.nimm(versucheJeSekunde, versucheVoll) {
		return false
	}
	if konto || stimmte {
		return true
	}
	if block := blockVon(wer); block != "" {
		if b := s.eimerVon(block); b == nil || !b.nimm(blockJeSekunde, blockVoll) {
			return false
		}
	}
	return s.alle.nimm(raumJeSekunde, raumVoll)
}

func (s *schranke) richtig(wer string, konto bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.je[wer]; e != nil {
		e.rest = min(versucheVoll, e.rest+1)
	}
	if konto {
		return
	}
	if _, schon := s.richt[wer]; !schon {
		s.alle.rest = min(raumVoll, s.alle.rest+1)
		if e := s.je[blockVon(wer)]; e != nil {
			e.rest = min(blockVoll, e.rest+1)
		}
	}
	if s.richt == nil {
		s.richt = map[string]time.Time{}
	}
	s.richt[wer] = time.Now()
	if len(s.richt) > merkeHoechstens {
		raus, seit := "", time.Now()
		for k, z := range s.richt {
			if raus == "" || z.Before(seit) {
				raus, seit = k, z
			}
		}
		delete(s.richt, raus)
	}
}

// hashPlatz wartet kurz auf einen Platz zum Hashen. Das Ende der Anfrage meldet
// eine übernommene WebSocket-Verbindung nicht, deshalb die eigene Frist.
func (s *schranke) hashPlatz(ctx context.Context, konto bool) (frei func(), ok bool) {
	ctx, ende := context.WithTimeout(ctx, hashWarten)
	defer ende()
	if konto {
		select {
		case hashKonten <- struct{}{}:
			return func() { <-hashKonten }, true
		case <-ctx.Done():
			return nil, false
		}
	}
	s.mu.Lock()
	if s.platz == nil {
		s.platz = make(chan struct{}, 2)
	}
	raum := s.platz
	s.mu.Unlock()
	select {
	case raum <- struct{}{}:
	case <-ctx.Done():
		return nil, false
	}
	select {
	case hashGaeste <- struct{}{}:
		return func() { <-hashGaeste; <-raum }, true
	case <-ctx.Done():
		<-raum
		return nil, false
	}
}
