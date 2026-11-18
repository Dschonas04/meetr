-- Datenbankschema von Meetr. Idempotent: wird bei jedem Start eingespielt.
CREATE TABLE IF NOT EXISTS raeume (
	id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	kennung    text NOT NULL UNIQUE,              -- der Teil im Link, zufällig
	name       text NOT NULL,
	gastgeber  text NOT NULL,                     -- Keycloak-sub oder lokaler Name
	gastgeber_name text NOT NULL DEFAULT '',
	gaeste     boolean NOT NULL DEFAULT true,     -- Gäste ohne Konto über den Link
	erstellt   timestamptz NOT NULL DEFAULT now(),
	zuletzt    timestamptz
);
