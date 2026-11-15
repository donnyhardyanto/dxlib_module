# dxlib_module

Released under the MIT License; see [LICENSE](LICENSE).

## Permission table

`module/permission_table` prints the grants the module's permission check reads, as the permission table
SpecArch's `extract permissions` reads (format version 1). A service calls it once its databases are open,
for example behind a flag that prints and exits:

    err := permission_table.Print(ctx, &log.Log, os.Stdout)

It prints one JSON object, its first line `{` alone, with `grants` (each role and privilege it grants, by
name id, read from `user_management.role_privilege` as `self.DxmSelf.RegenerateSessionObject` reads it) and
`gates` (each check that runs only when a setting is present, and so lets every request through while the
setting is empty). Both lists are sorted, each entry listed once. `EVERYTHING` is printed as stored, not
expanded. The module's own middleware has no gate (`ModuleGates` says why); a service passes the gates of its
own middleware after the writer, as `permission_table.Gate{Check: ..., Setting: ...}`. SpecArch's
`tools/permissions/dump-permissions.sh` runs the service's printing command in a committed folder and adds the
format version, the folder's path and the commit.
dxlib's log writes to standard output and the dump script wants `{` alone on the first line, so keep every log
line off standard output while printing: write the table to a file and print that file afterwards.

## Names a service sets for itself

- `module/account_lockout` starts every Redis key with `core.redis_key_prefix` from the `account_lockout`
  configuration (`AccountLockoutConfig.RedisKeyPrefix`); unset, it is `dxlib:account_lockout`. A service that
  already has lockout keys in Redis sets its old prefix before it upgrades, or its stored lockouts and counters
  are no longer read.
- `module/push_notification` names the tracer of its FCM spans `push_notification.TracerName`, by default the
  package's import path. A service whose dashboards read another tracer name sets it at start-up.
- `tools/reset-databases` reads `VAULT_PATH` with the default `kv/data/dev`.

## Sessions in Valkey (`module/session`, `module/credentials`, `module/auth_audit`)

The base of a login framework: a session store in Valkey that carries the user's profile, with every change
to the user written into all of that user's sessions at once.

- **Cookie**: a 256-bit random token, base64url (43 characters), in a `__Host-` cookie (default
  `__Host-session`) with `Path=/`, `Secure`, `HttpOnly`, `SameSite=Strict` and no `Domain`
  (`Config.Cookie`, `Config.ClearCookie`, `Config.TokenFromRequest`). A value of any other form is refused
  before Valkey is asked.
- **Keys**: the session is a hash at `<KeyPrefix>s:<hex SHA-256 of the token>`, never the token itself; it
  holds the user uuid, auth method, CSRF token, creation time, absolute expiry and the profile (username,
  display name, status, photo reference and version, roles, permissions) with its version. Every session of
  a user is listed in the set `<KeyPrefix>u:<user uuid>`, and `<KeyPrefix>v:<user uuid>` keeps the highest
  profile version propagated for the user. The service sets `KeyPrefix`.
- **Expiry**: a sliding idle TTL (`IdleTTL`, default 15 minutes) that never runs past the absolute expiry
  (`AbsoluteTTL`, default 12 hours), both as Valkey TTLs. Times come from Valkey's own clock.
- **Rotation**: `Rotate` gives a session a new token and CSRF token at a privilege change, keeping its creation
  time and absolute expiry; `Logout`, `RevokeSession` (one of the caller's own sessions, by id; another user's
  id answers `ErrNotFound`, as a missing one does) and `RevokeAll` (every session of a user, for disable and
  delete) end sessions.
- **Propagation**: after the database commit of a change to a user (display name, status, photo, roles), the
  service calls `PropagateProfile`; one Lua script writes the profile into every session of the user. A copy
  with a lower `Profile.Version` never replaces a higher one, also for a login, rotation or rebuild that read the
  profile just before the change: such a login takes the current profile from the `ProfileLoader`, or is refused
  when there is none. When the write fails, every session of the user
  is deleted (fail closed), the cause logged and audited, and a `*PropagationError` returned; when even the
  delete fails (Valkey down), the error says so and carries both causes, and the service should retry
  `RevokeAll` once Valkey is back. The database stays the authority: `InvalidateProfiles` drops the cached
  copies and the next `Get` of each session rebuilds it through the service's `ProfileLoader`.
- **Audit**: every login, refused login, logout, rotation, revocation and propagation goes to an
  `auth_audit.Sink` the service points at its own append-only store. A login whose audit record cannot be
  written is refused and its session deleted; so is a rotation (its old token is already gone). `auth_audit.Memory` is for tests.
- **Credentials**: the store connects through `credentials.RedisConnection`, a dxlib redis connection made
  with the credential a `credentials.Provider` returns, never with configuration values or the environment.
  It reconnects with the provider's newer credential when the current one is within `RenewBefore` of its
  expiry, when Valkey refuses it (`WRONGPASS`, `NOAUTH`), or on `Refresh`; the sessions stay in Valkey across
  it. Calls refused at the same moment reconnect once between them, and a credential the provider still returns
  after Valkey refused it is not tried again. Each new credential is first tried on one plain connection that is
  closed afterwards, and only an accepted one goes to dxlib's `Connect` (a failed `Connect` leaves its client
  running); after a failed attempt the next waits `RetryMin` (1 s), doubling up to `RetryMax` (30 s), and calls
  in between get the last error at once. Its methods (`Eval`, `HGet`, `Command`) and the store's `Connection`
  interface take and return plain Go values, so no Redis client type is part of the API.
  `credentials.Fixed` is for tests only.

Valkey:

- dxlib's redis package pins `github.com/go-redis/redis/v8` v8.11.5, which speaks RESP2 and authenticates with
  `AUTH <user> <password>`; Valkey accepts both. The tests run against `valkey/valkey:9.1.3`.
- One node or a Sentinel-managed primary. Valkey Cluster is not supported: the scripts reach sessions through the
  per-user set.
- dxlib's redis package has no TLS option yet, so the connection is plain TCP: keep Valkey on a private network
  until it has one.
- Run Valkey with `maxmemory-policy noeviction`, so sessions are never dropped silently.
- An ACL user limited to the prefix is enough:

      ACL SETUSER <user> on ><password> resetkeys ~<KeyPrefix>* resetchannels -@all +ping +select +eval +evalsha
        +script|load +time +hset +hget +hmget +hdel +del +exists +pexpire +pttl +sadd +srem +smembers

The tests that need Valkey start a throwaway container and make such an ACL user for each test:

    tools/test-valkey.sh -v             # PODMAN=... names the container command (default podman)

Without `DXLIB_MODULE_TEST_VALKEY` set, `go test ./...` skips them.

## Dependencies

Every new or upgraded dependency is scanned and its licence checked before the change is committed.
From the repo root, after `go get` and `go mod tidy`:

    syft dir:. -q -o cyclonedx-json=/tmp/dxlib_module-sbom.cdx.json
    grype sbom:/tmp/dxlib_module-sbom.cdx.json
    osv-scanner scan source -r .
    govulncheck ./...
    trivy fs --scanners vuln .

The databases disagree at times, so all five run and the union counts. Trivy cannot tell whether code
calls a vulnerable package, so `.trivyignore` at the repo root repeats the accepted GO-2026-5932 with an
expiry date.

Every dependency must be open source under an OSI-approved licence; syft lists the licences from the
module cache after `go mod download`:

    SYFT_GOLANG_SEARCH_LOCAL_MOD_CACHE_LICENSES=true syft dir:. -q -o syft-json \
      | jq -r '.artifacts[] | [.name, .version, ([.licenses[]?.value] | join(" | "))] | @tsv' | sort

An empty licence column or a `sha256:` value means syft could not name it: read the package's licence
file in `$(go env GOMODCACHE)`. The commit message records the scan result and the licences.

### Accepted findings: `.dependency-allowlist.json`

A finding that no version change can clear is recorded in `.dependency-allowlist.json` at the repo
root. The pre-commit dependency scan reads it, so an entry can arrive in the same commit as the
dependency it covers:

    {"licences":           {"<name>[@<version glob>]": "<SPDX licence, checked by hand>"},
     "licence_exceptions": {"<name>[@<version glob>]": "<why the owner accepted it>"},
     "vulnerabilities":    {"<GO-, GHSA- or CVE- id>": "<why the owner accepted it>"}}

A vulnerability is accepted only on the owner's word, and only with proof from govulncheck that no
dxlib_module code calls the vulnerable package: its report must say the code is affected by 0
vulnerabilities, with the finding listed under modules that are required but not called. The entry
says why, when it was accepted and when to look at it again. It is removed once a fixed release
exists.

Current entries:

- `GO-2026-5932` in `golang.org/x/crypto`, accepted 2026-10-05, the same acceptance as the entry in
  dxlib's `.dependency-allowlist.json`. The advisory declares `golang.org/x/crypto/openpgp`
  unmaintained and unsafe by design, and it covers every release of the module (introduced at v0, no
  fixed version), so no upgrade clears it. dxlib_module imports nothing from the module; it is
  required only through dxlib, which uses `argon2` and `bcrypt`. Nothing imports openpgp, and
  govulncheck reports the package is not called. Review by 2027-04-05, or sooner if a fix appears.

- `github.com/donnyhardyanto/dxlib`: MIT, read from its `LICENSE` file. Recorded because the licence
  lookup the pre-commit scan makes (deps.dev, through osv-scanner) has nothing for a release tagged the
  same day, which refuses the commit that moves the pin.

A project that depends on dxlib_module inherits this finding through its `go.mod`. It can accept it
the same way, citing this entry or dxlib's, provided its own govulncheck run shows the same: openpgp
not called.

## Compliance and Security

The software bill of materials of every release from v1.73.0 on is committed in `sbom/<tag>/` at the root of
this repository (releases before v1.73.0 have none: the records began with v1.73.0 and were not made for earlier
tags), in two formats from the same syft run: CycloneDX JSON (`<name>-<version>.cdx.json`) and SPDX JSON
(`<name>-<version>.spdx.json`). Beside them are the report of each scanner that ran on the release's tree (grype
on the SBOM, osv-scanner on the source, govulncheck for each Go module, and the repository's own scanners when
it names any), `tools.json` with the scanners' versions and exit statuses, `allowlist-applied.json` with the
accepted findings that applied, and a README.md naming the tag, the commit scanned, the date and the result.
Where the repository's releases allow it, both SBOMs are also attached to the GitHub release.

To run the scan again for a tag: `sbom-record <tag>` from the repository's root. It scans the tag's tree,
writes `sbom/<tag>/` and commits it.

Accepted findings and licences, each with its reason, are in `.dependency-allowlist.json` (`vulnerabilities`,
`licences`, `licence_exceptions`); a scanner's own ignore file (`osv-scanner.toml`, `.grype.yaml`,
`.trivyignore`) holds its entries where one exists.
