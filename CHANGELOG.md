# Änderungen

## Unveröffentlicht

- Sicherheit: TURN-Zugangsdaten nur für Zugelassene, Tempogrenze je Verbindung, Moderation nur nach Rolle, Verbindungen enden mit dem Konto.
- Kennwort und Termin je Raum, Raum abschließen, Sprecheransicht, Reaktionen, Kamera aus.
- Bildschirm teilen geht ohne Kamera und mehrfach, die Kamera geht beim Umschalten wirklich aus.
- Sicherheit: Die SSO-Einstellung hat einen Leser für Prüfung und Speichern und lehnt Unlesbares ab.
- Doppelte id im Seitengerüst behoben.
- Sicherheit: Die Sperre der Verwaltung wartet weder auf den Browser noch auf den Anbieter und hat eine Zeitgrenze.
- Sicherheit: Änderungen an Konten, Rollen und Anmeldung laufen nacheinander, Prüfung und Schreiben unter einer Sperre.
- Sicherheit: Mit einem Konto enden seine Rechte und Räume; geprüft wird erst, wenn die Anfrage vollständig da ist.
- Sicherheit: Admin wird man nur mit dem Einrichtungscode oder über `MEETR_ADMIN`, nicht durch die erste Anmeldung. Kennungen von eigenen Konten und SSO sind getrennt.
- Oberfläche überarbeitet: Kopfzeile, Tabellen und Felder am Handy, Anmeldeseite in den Farben der App.
- Logo auf der Anmeldeseite wird wieder ausgeliefert, der Hinweis zur Ersteinrichtung stimmt.
- Sicherheit: Anmeldung, Konten und Verwaltung nur noch für Admins.
- Räume per Link (Kennung wie kq7m-x2ra-9fhd), Liste der eigenen Räume, Löschen.
- Video und Ton per WebRTC direkt zwischen den Teilnehmern (Mesh, bis 12), Signalisierung über WebSocket.
- Wartebereich für Gäste ohne Konto, Zulassen und Ablehnen durch Angemeldete.
- Chat, Hand heben, Bildschirm teilen, Stummschalten und Entfernen.
- Anmeldung über Keycloak (OIDC), eigene STUN/TURN-Server per MEETR_ICE.
- Tests für Seiten, Kennungen und Namen.
