# Meetr

Videokonferenzen im Browser, auf dem eigenen Server: Räume per Link, Bildschirmfreigabe, Chat.

**Stand: Grundgerüst.** Der Server startet und zeigt eine Startseite, die
Funktionen unten sind geplant.

> Video läuft über UDP. Für Teilnehmer von außen braucht es eine Portfreigabe für die Medienports oder einen TURN-Server; das wird erst entschieden, wenn es so weit ist.

## Geplant

* **Räume per Link**, fest oder für einen Termin, mit Wartebereich und Kennwort nach Wahl
* **Video und Ton über WebRTC** mit eigenem SFU (Pion): jeder schickt einmal, der Server verteilt
* **Bildschirmfreigabe**, Chat im Raum, Hand heben, Reaktionen
* **Moderation**: stummschalten, Kamera aus, entfernen, Raum abschließen
* **Gastgeber über Keycloak**, Gäste ohne Konto über den Link
* **TURN** (coturn) für Teilnehmer hinter strengen Firewalls
* **Sprecheransicht und Galerie**, Simulcast für schwache Verbindungen
* Später: Aufzeichnung, Untertitel mit lokalem Whisper

## Schritte

1. Signalisierung über WebSocket, zwei Teilnehmer direkt (P2P)
2. SFU mit Pion für viele Teilnehmer
3. Räume, Wartebereich, Gastgeberrechte
4. Bildschirmfreigabe und Chat
5. TURN und Simulcast
6. Keycloak, Termine, Aufzeichnung

## Starten

```bash
cp .env.beispiel .env            # Kennwort für die Datenbank eintragen
docker compose up -d --build
```

Danach unter `http://<host>:8370`. Im Homelab kommt TLS über Caddy mit
der Homelab-CA davor, der Dienst selbst lauscht dann auf `127.0.0.1:28370`.

## Entwickeln

```bash
go run .
go test ./...
```

Go 1.27.4, Datenbank PostgreSQL. Das Schema steht in `schema.sql`.

## Lizenz

MIT, siehe [LICENSE](LICENSE).
