# Provisioning API

The dedicated listener exposes the App Covenant `0.1.0-draft.1` manifest and
five complete PUT operations. It shares transactional stores with the public
server but has its own TLS configuration. The public OpenAI-compatible `/v1/`
proxy is unaffected.

This is lot 2: unit reads, ETags, preconditions, lists, synchronization and
webhooks are still pending. The manifest identifies the targeted contract;
it is not a claim of complete conformance. Do not use conditional writes yet.

## Configuration and authority

| Variable | Default | Meaning |
| --- | --- | --- |
| `XOLO_PROVISIONNING_API_ENABLED` | `false` | Enable the separate listener |
| `XOLO_PROVISIONNING_API_ADDRESS` | `:3003` | Listen address |
| `XOLO_PROVISIONNING_API_TLS_CERT_FILE` | — | Server certificate PEM |
| `XOLO_PROVISIONNING_API_TLS_KEY_FILE` | — | Server private key PEM |
| `XOLO_PROVISIONNING_API_TLS_CLIENT_CA_FILE` | — | Trusted client CA bundle PEM |
| `XOLO_PROVISIONNING_API_AUTHORIZED_URIS` | — | Required comma-separated absolute URI allowlist |
| `XOLO_PROVISIONNING_API_RATE_LIMIT` | `10` | Requests per second per authorized URI, positive |
| `XOLO_PROVISIONNING_API_RATE_BURST` | `20` | Burst per URI, positive integer |
| `XOLO_PROVISIONNING_API_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown timeout |

TLS 1.3 is required. Client certificates must chain to the configured CA and
contain exactly one URI SAN matching the allowlist exactly. CN, DNS SAN,
bearer tokens, cookies and forwarded certificate headers grant no authority.
An authorized URI controls the whole instance, including suspended resources.
Certificate failures fail the TLS handshake; a request-time rejection returns
`403 client_certificate_rejected`.

Rate budgets are local to each server replica and reset on restart. A rejection
returns `429 rate_limited`, a positive integer `Retry-After` in seconds, and
performs no mutation. Use retry backoff with jitter.

`X-Request-ID` accepts exactly one 32-character lowercase hexadecimal value.
Otherwise the server generates one, discarding the invalid input. Every HTTP
response echoes the selected ID; access logs and mutation audits use it.
It is a correlation identifier, not an idempotency key.

## Common routes

`GET /v1/manifest` returns only `name`, `version` (the Xolo build release) and
`contract_version` (`0.1.0-draft.1`). Read it before writes and after upgrades.

| PUT path | Mutable fields |
| --- | --- |
| `/v1/tenants/{tenantID}` | `slug`, `name`, `status` |
| `/v1/tenants/{tenantID}/domains/{hostname}` | `status` |
| `/v1/tenants/{tenantID}/organizations/{organizationID}` | `slug`, `name`, `status` |
| `/v1/tenants/{tenantID}/members/{memberID}` | `email`, optional `display_name`, `tenant_role`, `status` |
| `/v1/tenants/{tenantID}/organizations/{organizationID}/members/{memberID}` | `role`, `status` |

UUID keys use canonical lowercase syntax. Tenant roles are `owner`/`member`;
membership roles are `owner`/`admin`/`member`. Status is `active`/`suspended`.
Tenant creation remains disabled in single-tenant mode; existing tenants can
still be updated. A tenant account PUT creates no identity, invitation or
membership. Existing platform privileges and provider identities are preserved,
and provisioning cannot assign configured default-admin emails to other accounts.

PUT requires `application/json` (valid media-type parameters are accepted), one
object, declared string fields and no nulls. All fields except `display_name`
are required. Omitting `display_name` clears it; an empty result is omitted.
The entire body, including trailing whitespace, is limited to 1,048,576 bytes.
Invalid types/JSON, unknown fields, invalid UTF-8 and excess bytes return
`400 invalid_json`; absent fields and invalid values return
`400 invalid_representation`. Unsupported media types return `415`.

Normalization follows Go Unicode trimming and lowercase mapping: slug, email
and hostname are trimmed and lowercased; names are trimmed. Roles and statuses
are case-sensitive and trimmed, **except member status which is not trimmed**.
Limits after normalization are 63 bytes for slug, 200 for name/display name,
320 for email and 253 for hostname. Email requires only `@` and no ASCII control
characters. Names also reject ASCII controls. Hostnames are ASCII DNS labels,
without IP literals, ports, trailing dots or the shared application hostname.
There is no NFC, IDNA or provider-specific email rewriting.

All successful common PUTs return `200` and only the normalized representation,
including creation. Identical normalized writes preserve rows, timestamps,
audit and publication records. Changes commit atomically. Tenant/organization
owners are protected against concurrent demotion or suspension:
`409 last_owner`. Parent suspension is allowed and does not rewrite children.
Custom membership roles survive a common role update.

Errors use `{"error":{"code":"...","message":"..."}}`. Missing parents return
`404 parent_not_found`; UUID reassignment and uniqueness conflicts return a
non-disclosing `409 conflict`. Invalid/reserved domains return
`400 invalid_hostname`. Unsupported routes and methods return `404 not_found`.
Technical failures return `500 internal_error` without database diagnostics.

## Persistent host routing

Public routing resolves an active domain record to an active tenant. The shared
hostname from `XOLO_HTTP_BASE_URL` is reserved and continues to serve the default
tenant in single-tenant mode. That binding survives a tenant slug rename.
Domain and tenant status are read from the database, without a stale local cache.
Base URLs and OIDC callbacks use the validated domain, retaining the configured
scheme, port and base path. The identity provider must allow these callback URLs.

`XOLO_MULTITENANCY_HOST_PATTERN` is optional and retained only for automatic
upgrade: at the first startup, existing tenant hosts from the old pattern are
saved as explicit domains. This happens once, with no creation events for
historical domains. Later tenant creation, slug changes or pattern edits never
create routes implicitly. Provision domains with PUT. Existing explicit domain
records and suspensions are preserved; collisions stop startup.

## Xolo extensions

Extensions live under `/v1/xolo` and retain Xolo-specific representations:

- `GET /healthz` and `GET /permissions`.
- `GET`/`PATCH /tenants/{tenantID}` for Xolo tenant metadata.
- `GET`/`PATCH /tenants/{tenantID}/organizations/{orgID}` for settings including
  description, currency and quota sharing.
- Role CRUD under `/tenants/{tenantID}/organizations/{orgID}/roles`.
- `PUT /tenants/{tenantID}/organizations/{orgID}/members/{membershipID}/roles`
  for Xolo role assignments using the internal membership identifier.
- `GET`/`PUT /tenants/{tenantID}/users` for provider/subject identity lookup and
  provisioning. This extension is separate from the common member declaration.

These extension payloads may include IDs, timestamps and Xolo settings. Their
creation/deletion responses retain their existing `201`/`204` semantics.
They do not enlarge the minimal manifest or the common contract.

## Upgrade

Database conversion is automatic at startup. Back up the database, preserve
`XOLO_SECRET_KEY`, stop older replicas, then start the new release. Existing
keys and encrypted secrets remain valid. Sessions with old tenant IDs require
sign-in again; external clients holding old IDs must refresh them.

For provisioning clients, this release intentionally replaces the old routes:
there are no aliases and no `/v2`. Change clients to complete PUT payloads and
client-generated UUIDs, and move Xolo-specific calls to `/v1/xolo`. Reissue any
client certificate without exactly one authorized URI SAN and configure
`XOLO_PROVISIONNING_API_AUTHORIZED_URIS`. For example, the certificate extension
`subjectAltName=URI:urn:example:console` matches that same configured URI.

Tests use ephemeral CAs and real TLS handshakes. Database tests exercise SQLite
and PostgreSQL. App Covenant tooling remains external; no conformance runner
or permanent CI workflow is installed in Xolo.
