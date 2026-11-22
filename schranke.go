package main

import (
	"context"
	"net"
	"sync"
	"time"
)

// Die Schranke vor dem Kennwort eines Raums, mit drei Grenzen:
//   - je Konto oder Netz des Gastes (IPv6 als /64), damit Raten nur den Ratenden sperrt
//   - je Raum für unbekannte Gäste, die gilt auch bei gefälschter Adresse
//   - gleichzeitiges Hashen, mit Wartezeit statt endloser Schlange
//
// Konten und Netze, aus denen das Kennwort schon stimmte, trifft die Raumgrenze nicht.

const (
	versucheVoll      = 8
	versucheJeSekunde = 0.1
	raumVoll          = 60
	raumJeSekunde     = 1
	merkeHoechstens   = 1000
	hashWarten        = 5 * time.Second
)

// Konten haben eigene Plätze: Gäste können sie nicht verdrängen.
var (
	hashGaeste = make(chan struct{}, 4)
	hashKonten = make(chan struct{}, 2)
)

type bekannter struct {
	eimer
	stimmte bool
}

type schranke struct {
	mu    sync.Mutex
	je    map[string]*bekannter
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

// nimm reserviert einen Versuch, richtig gibt ihn zurück.
func (s *schranke) nimm(wer string, konto bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.je == nil {
		s.je = map[string]*bekannter{}
	}
	b := s.je[wer]
	neu := b == nil
	if neu {
		b = &bekannter{}
	}
	if !b.nimm(versucheJeSekunde, versucheVoll) {
		return false
	}
	if !konto && !b.stimmte && !s.alle.nimm(raumJeSekunde, raumVoll) {
		return false // und nichts gemerkt: abgewiesene Adressen kosten keinen Speicher
	}
	if neu {
		if len(s.je) >= merkeHoechstens {
			s.verdraengen()
		}
		s.je[wer] = b
	}
	return true
}

// verdraengen wirft den ältesten hinaus, zuerst einen, der nie richtig lag.
func (s *schranke) verdraengen() {
	var raus string
	var opfer *bekannter
	for k, b := range s.je {
		if opfer == nil || (opfer.stimmte && !b.stimmte) || (opfer.stimmte == b.stimmte && b.seit.Before(opfer.seit)) {
			raus, opfer = k, b
		}
	}
	delete(s.je, raus)
}

func (s *schranke) richtig(wer string, konto bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.je[wer]; b != nil {
		b.rest = min(versucheVoll, b.rest+1)
		if !konto && !b.stimmte {
			s.alle.rest = min(raumVoll, s.alle.rest+1)
		}
		b.stimmte = true
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
