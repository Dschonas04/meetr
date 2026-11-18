-- Datenbankschema von Meetr (Entwurf).
CREATE TABLE IF NOT EXISTS raeume (
	id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	kennung    text NOT NULL UNIQUE,              -- der Teil im Link
	name       text NOT NULL,
	gastgeber  text NOT NULL,                     -- Keycloak-Subjekt
	kennwort   text,                              -- bcrypt, leer = ohne
	wartebereich boolean NOT NULL DEFAULT true,
	erstellt   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS termine (
	id      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	raum    uuid NOT NULL REFERENCES raeume(id) ON DELETE CASCADE,
	beginn  timestamptz NOT NULL,
	ende    timestamptz NOT NULL,
	titel   text NOT NULL DEFAULT ''
);
