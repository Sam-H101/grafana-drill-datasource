# Drill configurations for development

| Directory | Compose service | Auth | Port on 127.0.0.1 |
| --- | --- | --- | --- |
| (image default) | `drill` | none | 8047 |
| `auth/` | `drill-auth` | htpasswd users, FORM and BASIC on HTTP, impersonation on | 8048 |
| `tls/` | `drill-tls` | none; HTTPS with a throwaway CA | 8049 |

All start with `docker compose up -d`.

`drill-tls` generates its CA and server certificate on first start, into the
`drill-tls` Docker volume (never into the repo). The toolbox mounts that volume
read-only at `/tls`; tests read `/tls/ca.crt` (override with
`DRILL_TLS_CA_FILE`). Outside the toolbox: `docker compose cp drill-tls:/tls/ca.crt .`

The users in `auth/htpasswd` exist only for local development and tests. Their
passwords are public on purpose:

| User | Password |
| --- | --- |
| `grafana` | `grafana-dev-password` |
| `analyst` | `analyst-dev-password` |

Never reuse these settings or credentials outside this dev stack. Regenerate a
hash with `openssl passwd -apr1 '<password>'`.

`impersonation/` (authentication off, impersonation on) was used to probe
Drill's behaviour; see `docs/drill-rest-notes.md`.
