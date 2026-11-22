package main

import (
	"fmt"
	"testing"
	"time"
)

func TestSchranke(t *testing.T) {
	if werFragt(false, "", "2001:db8:1:2::1") != werFragt(false, "", "2001:db8:1:2:ffff::9") {
		t.Error("jede Adresse eines /64 hat einen eigenen Eimer")
	}
	if werFragt(false, "", "erfunden-1") != werFragt(false, "", "erfunden-2") {
		t.Error("Unlesbares bekommt eigene Eimer")
	}

	var s schranke
	if !s.nimm("netz:freund", false) {
		t.Fatal("erster Versuch abgewiesen")
	}
	s.richtig("netz:freund", false)
	durch := 0
	for i := range 500 {
		if s.nimm(fmt.Sprint("netz:", i), false) {
			durch++
		}
	}
	if durch > raumVoll+2 {
		t.Errorf("mit wechselnden Adressen %d Versuche", durch)
	}
	if !s.nimm("netz:freund", false) {
		t.Error("wer richtig lag, ist ausgesperrt")
	}
	if !s.nimm("konto:anna", true) {
		t.Error("Konto von der Raumgrenze getroffen")
	}

	var voll schranke
	for i := range merkeHoechstens * 2 {
		voll.nimm(fmt.Sprint("konto:", i), true)
	}
	if len(voll.je) > merkeHoechstens {
		t.Errorf("merkt sich %d", len(voll.je))
	}
}

func TestHashPlatz(t *testing.T) {
	var s schranke
	var frei []func()
	for range 2 {
		f, ok := s.hashPlatz(t.Context(), false)
		if !ok {
			t.Fatal("kein Platz")
		}
		frei = append(frei, f)
	}
	beginn := time.Now()
	if _, ok := s.hashPlatz(t.Context(), false); ok || time.Since(beginn) > hashWarten+2*time.Second {
		t.Error("dritter Gast wartet nicht mit Frist")
	}
	var anderer schranke
	f, ok := anderer.hashPlatz(t.Context(), false)
	if !ok {
		t.Fatal("ein Raum belegt alle Plätze")
	}
	frei = append(frei, f)
	k, ok := s.hashPlatz(t.Context(), true)
	if !ok {
		t.Fatal("Gäste verdrängen Konten")
	}
	frei = append(frei, k)
	for _, f := range frei {
		f()
	}
}

func TestSchrankeBlock(t *testing.T) {
	var s schranke
	durch := 0
	for i := range 200 {
		if s.nimm(werFragt(false, "", fmt.Sprintf("2001:db8:7:%x::1", i)), false) {
			durch++
		}
	}
	if durch > blockVoll+1 {
		t.Errorf("ein /48 nimmt dem Raum %d Versuche", durch)
	}
	if !s.nimm(werFragt(false, "", "2001:db8:8::1"), false) {
		t.Error("ein /48 sperrt die anderen aus")
	}
}

func TestSchrankeVergisstKeineSperre(t *testing.T) {
	var s schranke
	for range versucheVoll {
		s.nimm("konto:raetselt", true)
	}
	if s.nimm("konto:raetselt", true) {
		t.Fatal("keine Grenze")
	}
	for i := range merkeHoechstens + 10 {
		s.nimm(fmt.Sprint("konto:", i), true)
	}
	if s.nimm("konto:raetselt", true) {
		t.Error("Sperre durch Verdrängen gelöscht")
	}
}
