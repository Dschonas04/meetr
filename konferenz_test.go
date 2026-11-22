package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Der ganze Weg über echte WebSockets: Kennwort, Wartebereich, Rechte, Ende.
func TestKonferenz(t *testing.T) {
	adresse := os.Getenv("MEETR_TEST_DB")
	if adresse == "" {
		t.Skip("MEETR_TEST_DB ist nicht gesetzt")
	}
	c, err := pgxpool.ParseConfig(adresse)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.ConnConfig.Database, "pruef") {
		t.Fatal("MEETR_TEST_DB muss auf eine Datenbank pruef* zeigen")
	}
	ctx, ende := context.WithTimeout(context.Background(), time.Minute)
	defer ende()
	db := datenbank(ctx, adresse)
	defer db.Close()

	a := &App{db: db, v: ladeVorlagen(), hub: neuerHub(),
		ice: json.RawMessage(`[{"urls":"turn:turn.example","username":"u","credential":"NUR-FUER-TEILNEHMER"}]`),
		sso: NeuesSSO("Meetr", filepath.Join(t.TempDir(), "sso.json"), "MEETR_TEST_SSO_AUS")}
	a.sso.Frei = []string{"/statisch/", "/healthz", "/r/", "/ws/"}
	kekse := map[string]string{}
	for _, name := range []string{"wirtin", "kollege"} {
		k := ssoKonto{Name: name, Hash: ssoPasswortHash("nur-fuer-den-test")}
		a.sso.konten = append(a.sso.konten, k)
		rec := httptest.NewRecorder()
		a.sso.lokalAnmelden(rec, httptest.NewRequest("POST", "/", nil), k)
		var l []string
		for _, c := range rec.Result().Cookies() {
			l = append(l, c.Name+"="+c.Value)
		}
		kekse[name] = strings.Join(l, "; ")
	}
	k := neueKennung()
	if _, err := db.Exec(ctx, `INSERT INTO raeume (kennung, name, gastgeber, kennwort, beginn) VALUES ($1, 'Runde, klein', 'konto:wirtin', $2, now())`,
		k, ssoPasswortHash("raumwort")); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.Exec(context.Background(), `DELETE FROM raeume WHERE kennung=$1`, k); err != nil {
			t.Error(err)
		}
	}()
	s := httptest.NewServer(a.wege())
	defer s.Close()

	res, err := http.Get(s.URL + "/r/" + k)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	puffer := make([]byte, 4096)
	for {
		n, err := res.Body.Read(puffer)
		b.Write(puffer[:n])
		if err != nil {
			break
		}
	}
	res.Body.Close()
	if res.StatusCode != 200 || strings.Contains(b.String(), "NUR-FUER-TEILNEHMER") {
		t.Errorf("Seite verrät die TURN-Daten oder fehlt: %d", res.StatusCode)
	}

	rein := func(wer, kennwort string) *websocket.Conn {
		t.Helper()
		h := http.Header{}
		if kekse[wer] != "" {
			h.Set("Cookie", kekse[wer])
		}
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(s.URL, "http")+"/ws/"+k, &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Write(ctx, c, Nachricht{Typ: "hallo", Name: wer, Text: kennwort}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	sag := func(c *websocket.Conn, n Nachricht) {
		t.Helper()
		if err := wsjson.Write(ctx, c, n); err != nil {
			t.Fatal(err)
		}
	}
	// bis liest bis zum Typ und meldet, was nicht davor kommen durfte.
	bis := func(c *websocket.Conn, typ string, nie ...string) Nachricht {
		t.Helper()
		for {
			var n Nachricht
			if err := wsjson.Read(ctx, c, &n); err != nil {
				t.Fatalf("warte auf %s: %v", typ, err)
			}
			for _, x := range nie {
				if n.Typ == x {
					t.Errorf("%s vor %s", x, typ)
				}
			}
			if n.Typ == typ {
				return n
			}
		}
	}

	falsch := rein("gast", "falsch")
	bis(falsch, "kennwort", "willkommen", "warten")
	falsch.CloseNow()

	wirtin := rein("wirtin", "")
	defer wirtin.CloseNow()
	wID := bis(wirtin, "willkommen").Von

	gast := rein("gast", "raumwort")
	defer gast.CloseNow()
	bis(gast, "warten", "willkommen")
	gID := bis(wirtin, "anfrage").Von
	sag(gast, Nachricht{Typ: "chat", Text: "aus dem Wartebereich"})
	sag(gast, Nachricht{Typ: "zulassen", An: gID})
	sag(wirtin, Nachricht{Typ: "zulassen", An: gID})
	if w := bis(gast, "willkommen", "chat"); !strings.Contains(string(w.ICE), "NUR-FUER-TEILNEHMER") {
		t.Error("Zugelassener bekommt keine ICE-Server")
	}
	bis(wirtin, "neu", "chat")

	kollege := rein("kollege", "raumwort")
	defer kollege.CloseNow()
	kID := bis(kollege, "willkommen").Von
	sag(kollege, Nachricht{Typ: "stumm", An: wID})
	sag(kollege, Nachricht{Typ: "entfernen", An: wID})
	sag(kollege, Nachricht{Typ: "abschliessen", Ja: true})
	sag(gast, Nachricht{Typ: "zustand", Daten: json.RawMessage(`{"hand":true,"name":"Chef"}`)})
	if z := bis(wirtin, "zustand", "stumm", "entfernt", "abschliessen"); z.Wer == nil || !z.Wer.Zustand.Hand || z.Wer.Name == "Chef" {
		t.Errorf("Zustand: %+v", z.Wer)
	}
	sag(kollege, Nachricht{Typ: "chat", Text: "fertig"})
	bis(wirtin, "chat", "stumm", "entfernt", "abschliessen")

	sag(wirtin, Nachricht{Typ: "abschliessen", Ja: true})
	bis(wirtin, "abschliessen")
	spaet := rein("spaet", "raumwort")
	bis(spaet, "gesperrt", "warten", "willkommen")
	spaet.CloseNow()

	sag(wirtin, Nachricht{Typ: "entfernen", An: gID})
	bis(gast, "entfernt")

	a.hub.kontoWeg("konto:kollege")
	if n := bis(wirtin, "weg"); n.Von != gID && n.Von != kID {
		t.Errorf("weg: %+v", n)
	}
	for {
		var n Nachricht
		if err := wsjson.Read(ctx, kollege, &n); err != nil {
			break
		}
	}
	if ctx.Err() != nil {
		t.Error("entzogenes Konto bleibt verbunden")
	}

	res, err = http.Get(s.URL + "/r/" + k + "/termin.ics")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/calendar; charset=utf-8" {
		t.Errorf("Termin: %d", res.StatusCode)
	}
}

func TestTermin(t *testing.T) {
	if b, ok := beginnLesen(""); b != nil || !ok {
		t.Error("leer ist kein Termin")
	}
	if _, ok := beginnLesen("morgen"); ok {
		t.Error("Unsinn angenommen")
	}
	if s := icsText("a,b;c\r\nEND:VEVENT"); strings.ContainsAny(s, "\r\n") || !strings.HasPrefix(s, `a\,b\;c`) {
		t.Errorf("%q", s)
	}
}
