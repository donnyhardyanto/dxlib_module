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

## Passkeys (`module/passkey`)

WebAuthn registration and login as a relying party, on `github.com/go-webauthn/webauthn` (BSD-3-Clause).

- **Passkeys only**: every credential is discoverable (`residentKey: required`) and made and used with user
  verification (`userVerification: required`); an answer without the UV flag is refused. A credential the
  client reports as not discoverable (`credProps.rk: false`) is refused. A user may hold several
  (`Config.MaxPerUser`, default 20); a new registration excludes the user's own.
- **Flow**: `BeginRegistration(ctx, User)` and `BeginLogin(ctx)` return the options for
  `navigator.credentials.create()` / `.get()` as JSON (`{"publicKey": ...}`) and a handle the page sends back
  with the browser's answer; `FinishRegistration(ctx, userUUID, handle, name, body, Client)` stores the passkey
  under a name (`CheckName` tells the user what is wrong with one), and `FinishLogin(ctx, handle, body, Client)`
  starts the session through `session.Store.Create` with the auth method `passkey` and returns its token. Bodies
  are read up to 64 KiB. No go-webauthn type is part of the API.
- **Rate-limit `BeginLogin`**: anyone may call it, before any login, and each call keeps a challenge in Valkey
  for `ChallengeTTL`. The service must rate-limit the endpoint that calls it (per client address), or a client
  that loops on it fills Valkey's memory.
- **Challenges** live in Valkey under `<KeyPrefix>c:<hex SHA-256 of the handle>` for `ChallengeTTL` (default and
  longest 5 minutes), not in a table, through `credentials.RedisConnection`. Each is taken (read and deleted in one
  step) before the answer is checked, so it works once, also when the answer is refused. A registration challenge
  is bound to the user it was started for and cannot be used to log in, nor a login challenge to register.
- **Login**: the authenticator names the user (the user handle is the user uuid's 16 bytes, so the service's
  uuids should be random, v4 or v7, never v1); the passkey must belong to that user. Then the service's
  `Accounts.LoginProfile` gives the profile, or an error that refuses the login (disabled, deleted, locked). The
  new signature counter and flags are stored before the session starts; a counter that goes back (a copied
  key) or a backup-eligible flag that changes refuses the login. The backup state is stored too, so `List`
  shows a device-bound passkey the platform later synced as synced from its next login.
- **Never the last way in**: `Remove` refuses the user's last passkey (`ErrLastWayIn`) unless
  `Accounts.HasPassword` says the user has a password; an error from it refuses too. The store counts and
  deletes in one transaction with the user's rows locked, so two removals at once cannot take both of the last
  two. Another user's passkey answers `ErrNotFound`, as a missing one does.
- **Audit**: registrations and removals (`credential_registered`, `credential_removed`), refused ones with
  their real reason (a refused `BeginRegistration` and a malformed user uuid included), and failed logins
  (`login_failed`) go to the `auth_audit.Sink`; `BeginLogin`'s errors are not, since no user or answer exists
  yet and it fails only when the challenge cannot be stored; a successful login is
  audited once, by the session store, with the passkey's id in its detail (`session.LoginInfo.AuditDetail`). A
  registration whose audit record cannot be written is refused and the passkey deleted.
- **Errors**: a refused ceremony returns an error wrapping `ErrRefused` whose text holds the real reason; answer
  the browser with 400 or 401 and log the text. It wraps the cause too, so `errors.Is` finds `ErrDuplicate` or
  `ErrTooMany` when the store refused the passkey. Its cause may also be `ErrNotFound` or the service's own
  error, so test for `ErrRefused` first.

The Valkey user of the challenges needs, under its own prefix:

    ACL SETUSER <user> on ><password> resetkeys ~<KeyPrefix>* resetchannels -@all +ping +select +eval +evalsha
      +script|load +set +get +del

The passkeys are kept by a `CredentialStore`. `DBStore` keeps them in a PostgreSQL table the service names
(`table` or `schema.table`), through dxlib's databases package, with fixed column names and every value bound as
a parameter. It takes any `Database` with dxlib's `Tx` (a `*databases.DXDatabase`):

    CREATE TABLE <schema>.<table> (
        id              text PRIMARY KEY,           -- credential id, base64url
        user_uuid       uuid NOT NULL,
        name            text NOT NULL,
        credential      text NOT NULL,              -- the WebAuthn credential record, JSON
        backup_eligible boolean NOT NULL,
        backup_state    boolean NOT NULL,
        created_at      timestamptz NOT NULL,
        last_used_at    timestamptz
    );
    CREATE INDEX ON <schema>.<table> (user_uuid);
    GRANT SELECT, INSERT, UPDATE, DELETE ON <schema>.<table> TO <service role>;

`MemoryStore` is for tests. The tests that need PostgreSQL start a throwaway `postgres:18.7` container, make a
schema, that table and a role with only those grants for each test:

    tools/test-postgres.sh -v           # PODMAN=... names the container command (default podman)

`tools/test-valkey.sh` runs the passkey tests that need Valkey. Without `DXLIB_MODULE_TEST_POSTGRES` or
`DXLIB_MODULE_TEST_VALKEY` set, `go test ./...` skips them.

## Passwords (`module/password`)

Password login, change and reset. A password is hashed with Argon2id (`golang.org/x/crypto/argon2`), and the hash
is then encrypted by a transit key in the secret store, so a copy of the table alone gives nothing to crack.

- **Hash**: Argon2id with 64 MiB of memory, 3 passes and 4 lanes (RFC 9106, section 4, the second recommended
  option), a 16-byte random salt and a 32-byte tag, kept as a PHC string (`$argon2id$v=19$m=65536,t=3,p=4$...`).
  The password is put in Unicode NFKC first, so the same password typed on two keyboards hashes the same. At
  most `Config.MaxConcurrentHashes` (default 4) hashes run at once, 64 MiB each; a login waits for a slot until
  its context ends. A hash stored with other parameters is replaced at the user's next login.
- **Transit**: the PHC string is encrypted through the `Transit` interface. `OpenBaoTransit` speaks the OpenBao
  (or Vault) transit HTTP API; its token comes from the `credentials.Provider` (the credential's `Password`)
  and is asked for again on HTTP 403, so a rotated token is picked up. `Check` at start-up refuses a key that is
  not `aes256-gcm96`, or is exportable, allows a plaintext backup or is derived. `InsecureTestTransit` is an
  in-memory key ring for tests only: its keys die with the process, and `New` refuses it outside `go test`. Each
  row stores the key version that encrypted it.
- **Bound to its user**: the encrypted value holds the user's uuid beside the hash, and a value read from
  another user's row is refused, so copying your own row over the owner's does not log you in as the owner.
- **Rotation**: after the key is rotated, `RewrapAll(ctx, batch)` re-encrypts every row of an older key version
  with the newest one (transit `rewrap`: the hashes never leave the secret store). A row changed meanwhile is
  left alone and counted. Once it returns no error, older versions may be trimmed (`min_decryption_version`).
- **Policy** (`CheckPolicy` lets a page ask first): 15 to 128 characters (`Config.MinLength`, never below 15;
  `Config.MaxLength`, at most 1024), counted as code points after NFKC; none of the user's context words
  (`Accounts.ContextWords`: username, e-mail, name; words under four characters are ignored); not in the
  `BreachedList`, checked as typed and after NFKC. `HashList` reads SHA-1 lists in the Pwned Passwords download
  format; `RangeAPI` asks a k-anonymity range service (`PwnedPasswordsURL`, or a copy the service runs), sending
  only the first five hex digits of the SHA-1 and asking for padded answers. A list that cannot answer refuses the
  change with its real error, which does not wrap `ErrPolicy`.
- **Login**: `Login(ctx, login, password, Client)` turns the login name into a user uuid through
  `Accounts.FindUser`, asks the `Lockout` first (a locked account, or a lockout that cannot answer, is refused:
  fail closed), checks the password, asks `Accounts.LoginProfile`, and starts the session through
  `session.Store.Create` with the auth method `password`. An unknown user, one without a password, a locked one
  and a password over the 1024-character limit each cost one Argon2id hash too (none when the password is over
  4096 bytes, for any user), so a single refusal does not tell whether the name exists; the lockout, store and
  transit round trips of a known user still add a few milliseconds. Every refusal is written to the
  `auth_audit.Sink` as `login_failed` with its real reason, and a wrong password is also fed to the `Lockout`
  (`Failed`); a success resets it (`Succeeded`).
- **Forced change**: a password set by someone else (`Set(..., mustChange=true)`, the reset-owner command) gives
  `ErrMustChange` at login and no session. `ChangeAndLogin(ctx, login, current, next, Client)` checks the current
  password as a login does, stores the new one, clears `must_change_password`, and starts the session.
  `Change(ctx, userUUID, current, next, Client)` is the same for a logged-in user. Every change ends all of the
  user's sessions (`RevokeAll`) and starts one new session for the caller; a change never overwrites a password
  someone else set meanwhile (`ErrConflict`).
- **Never the last way in**: `Remove` refuses (`ErrLastWayIn`) unless `Accounts.HasPasskey` says the user has a
  passkey. Wire `Manager.Has` to `passkey.Accounts.HasPassword` for the other direction.
- **Lockout**: the `Lockout` interface; `module/password/accountlockout` adapts `module/account_lockout` to it
  with a resolver from user uuid to the numeric user id and login id. Run account_lockout with
  `REDIS_FAIL_THEN_LOCK`: with `REDIS_FAIL_KEEP_UNLOCK` it answers "not locked" while Redis is down.
- **Reset-owner host command**: `RunResetOwner(ctx, m, owner, args, stdout, stderr)` is run by the service from
  its own binary on the server (`myservice reset-owner --confirm [--user <uuid>]`), after it has built the
  Manager from its secret store. Without `--confirm` it changes nothing. It ends the account's lockout, sets a
  random 24-character temporary password with `must_change_password`, ends the account's sessions, audits a
  `credential_changed` record whose detail names the host command, and prints the temporary password alone on
  stdout (everything else goes to stderr). The temporary password skips the breached list, so a reset works
  while the list cannot be reached.
- **Audit**: `credential_changed` (`Set`, changes, reset-owner; the actor's uuid when an administrator set it)
  and `credential_removed`, refused ones with their real reason; failed logins as `login_failed`; a successful
  login once, by the session store. No record holds a password.
- **Errors**: a refused login or change wraps `ErrRefused` (and `ErrLocked` for a locked account), its text
  holding the real reason; answer 401 without the detail and log the text. A refused new password wraps
  `ErrPolicy` and one of `ErrTooShort`, `ErrTooLong`, `ErrBreached`, `ErrContextWord`, `ErrSameAsCurrent`,
  `ErrNotUTF8`; that text is safe to show the user.

The transit key and the token's policy (the service's token comes from its `credentials.Provider`):

    bao secrets enable transit
    bao write -f transit/keys/<key> type=aes256-gcm96 exportable=false
    path "transit/encrypt/<key>" { capabilities = ["update"] }
    path "transit/decrypt/<key>" { capabilities = ["update"] }
    path "transit/rewrap/<key>"  { capabilities = ["update"] }
    path "transit/keys/<key>"    { capabilities = ["read"] }

Rotate with `bao write -f transit/keys/<key>/rotate`, run `RewrapAll`, then
`bao write transit/keys/<key>/config min_decryption_version=<newest>`.

`DBStore` keeps the passwords in a PostgreSQL table the service names, through dxlib, with fixed column names and
every value bound as a parameter; it takes any `Database` with dxlib's `Tx`:

    CREATE TABLE <schema>.<table> (
        user_uuid            uuid PRIMARY KEY,
        ciphertext           text NOT NULL,       -- transit ciphertext of the uuid and the Argon2id hash
        key_version          integer NOT NULL,    -- transit key version of ciphertext
        must_change_password boolean NOT NULL,
        changed_at           timestamptz NOT NULL
    );
    CREATE INDEX ON <schema>.<table> (key_version);
    GRANT SELECT, INSERT, UPDATE, DELETE ON <schema>.<table> TO <service role>;

`MemoryStore` is for tests. `tools/test-postgres.sh` runs the `DBStore` tests against `postgres:18.7`, and
`tools/test-openbao.sh` runs `OpenBaoTransit` against an `openbao/openbao:2.6.5` dev server (each test mounts its
own transit engine, key, policy and token). Without `DXLIB_MODULE_TEST_POSTGRES` or `DXLIB_MODULE_TEST_OPENBAO`
set, `go test ./...` skips them.

## User photos (`module/user_photo`)

One profile photo per user, checked, re-encoded and kept in a private bucket. Only the photo route reads it
back, after the caller's right is checked.

- **Upload**: `Set(ctx, caller, userUUID, body)` reads at most `Config.MaxBytes` (2 MiB by default, at most
  8 MiB), counted while reading, so a chunked upload cannot pass the limit. The file is accepted only when its
  own bytes are PNG, JPEG or WebP; the request's Content-Type and file name are never read, so a GIF, BMP, SVG
  or HTML page sent as `image/png` is refused. The service's route checks the caller's CSRF token
  (`session.Session.CheckCSRF`) before calling `Set` or `Remove`.
- **Pixel-bomb limit**: the dimensions are read from the header with that format's own decoder (no other
  registered decoder is ever reached) and refused over `Config.MaxPixels` (16 777 216, 4096x4096) or
  `Config.MaxSide` (8192) before any pixel is decoded. For WebP every frame header is checked as well as the
  canvas, since the decoder allocates by the frame; an animated WebP is refused. A JPEG with more than 64 scan
  markers is refused too: each progressive scan is a pass over every block, and a scan that adds nothing costs
  the file a dozen bytes, so 2 MiB of them kept Go's decoder busy for about six minutes (measured at 4000x4000).
  At most `Config.MaxConcurrentDecodes` (default 1) decodes run at once, each holding its slot until the image is
  scaled and needing up to about `MaxPixels` x 20 bytes meanwhile (320 MiB at the default; a progressive
  four-component JPEG is the worst case); an upload waits for a slot until its context ends. A decoded image
  whose size differs from its header is refused.
- **Re-encoding**: a centred square is cut and scaled to 256x256 (Catmull-Rom), then encoded as lossless WebP
  (`github.com/HugoSmits86/nativewebp`) from the pixels alone: the stored file is RIFF, WEBP and one VP8L
  chunk, so no EXIF, GPS, ICC, XMP or comment survives. Transparency is kept. EXIF orientation is not applied,
  so a camera photo stored sideways with an orientation tag comes out sideways.
- **Storage**: each photo goes under a new random key (`Config.KeyPrefix` + 64 hex digits + `.webp`, nothing
  of the user in it) through the `ObjectStore` interface. `S3Store` speaks the S3 API (tested against
  SeaweedFS; other S3 servers are untested) through `github.com/minio/minio-go/v7`, path-style; its access key
  and secret key are the
  credential's `Username` and `Password` from the `credentials.Provider` (`S3Config.CredentialName`), asked
  for again 30 seconds before `ExpiresAt` and after any answer refusing the credential (`AccessDenied`,
  `InvalidAccessKeyId`, `SignatureDoesNotMatch`, `ExpiredToken`), which is then tried once more. `Check(ctx)`
  at start-up refuses a bucket that does not exist (it never creates one) and one that answers an unsigned
  listing or read with anything but 401 or 403. `MemoryObjectStore` is for tests.
- **The row**: `Store` keeps, per user, the object key and a version that rises by one with every change or
  removal (a removal keeps the row with an empty key, so the version keeps rising). The previous object is
  deleted once the new row is committed; when that fails the change still stands, and `Change.OldObjectErr`,
  the log and the audit record (`old_object_left`) name the key to remove by hand. When the row cannot be
  written, the new object is deleted again.
- **Sessions**: after the commit the profile is loaded through `Profiles` and written into every session of
  the user with `session.Store.PropagateProfile`. `NewProfiles(serviceLoader, store, cfg)` wraps the
  service's own `session.ProfileLoader` and fills `PhotoRef` (`Config.PhotoPath` with the uuid and
  `?v=<version>`, never the object key) and `PhotoVersion` from the `Store`. Give it to
  `session.NewValkeyStore` as the loader, and build every login's profile with `Profiles.LoadProfile` or
  `Profiles.Fill` (the `LoginProfile` of password's and passkey's `Accounts`), so every session carries the
  photo. The profile's `Version` stays the service's own: a photo change never raises it, so the session
  store's version marker stays on the service's scale and a later login or name change is never taken for an
  older copy. When the profile cannot be loaded, the user's cached profiles are dropped (rebuilt on the next
  request), and when even that fails their sessions are ended; `Change.SessionsErr` says which. A
  `*session.PropagationError` (the session store has deleted the sessions) is handed back the same way.
- **The route**: `Handler(sessions, sessionConfig)` serves `GET` and `HEAD`; mount it on a pattern with
  `{uuid}` that matches `Config.PhotoPath`:

      mux.Handle("GET /users/{uuid}/photo", photos.Handler(sessions, sessionConfig))

  It reads the session from the cookie (401 without one), asks `Rights.MayView`, and only then reads the row
  and the object. Another user's photo without the right, a user without a photo, a removed photo and
  anything that is not a uuid all answer the same 404. A photo is served with `Content-Type: image/webp`,
  `X-Content-Type-Options: nosniff`, `Cache-Control: private, max-age=86400` (`Config.CacheMaxAge`),
  `Vary: Cookie`, `ETag: "<version>"` (`If-None-Match` gives 304), `Content-Security-Policy: default-src
  'none'; sandbox` and `Cross-Origin-Resource-Policy: same-origin`; refusals are `private, no-store`. A
  session store or bucket that fails answers 503, the real error in the log.
- **Rights**: `SelfOrPermission{ViewAny, ChangeAny}` (the default with both empty) lets each user see and
  change their own photo, and a session holding the named permission anyone's. A service with other rules
  implements `Rights`. A caller who may not change a photo gets `ErrNotFound`, and the attempt is audited.
- **Deleted with the user**: `DeleteUser(ctx, userUUID, actorUUID)` deletes the row and the object as part of
  deleting the user (the service ends the sessions with `RevokeAll`). When the object cannot be deleted, the
  error and the audit record name its key.
- **Audit**: `photo_changed` and `photo_removed` with the actor's uuid when it is not the user, the version,
  the sizes, and on a failure the real reason (a refused image, a refused right, a store error).
- **Errors**: a refused image wraps `ErrRejected` and one of `ErrTooLarge` (answer 413), `ErrFormat`,
  `ErrTooManyPixels`, `ErrDecode` (422); that text is safe to show the user. `ErrNotFound` is 404.

`DBStore` keeps the rows in a PostgreSQL table the service names, through dxlib, with fixed column names and
every value bound as a parameter:

    CREATE TABLE <schema>.<table> (
        user_uuid  uuid PRIMARY KEY,
        object_key text NOT NULL,        -- '' once the photo was removed
        version    bigint NOT NULL,
        changed_at timestamptz NOT NULL
    );
    GRANT SELECT, INSERT, UPDATE, DELETE ON <schema>.<table> TO <service role>;

The bucket is created by the operator, with no anonymous access, and the service's identity limited to it.
In SeaweedFS (`-s3.config`), an identity with only `Read:<bucket>`, `Write:<bucket>` and `List:<bucket>`, and no
`anonymous` identity that names the bucket; `Check` refuses the bucket whenever an unsigned request gets in.

`tools/test-seaweedfs.sh` runs the `S3Store` tests against `chrislusf/seaweedfs:4.53` with an admin, a photo
identity limited to the test bucket, another limited to another bucket, and an anonymous identity that may
read only a `public-test` bucket (for `Check`'s refusal). `tools/test-postgres.sh` and `tools/test-valkey.sh`
include the package's `DBStore` tests and a test against the real session store. Without
`DXLIB_MODULE_TEST_S3`, `DXLIB_MODULE_TEST_POSTGRES` or `DXLIB_MODULE_TEST_VALKEY`, `go test ./...` skips them.
`FuzzReencode` feeds the image checks arbitrary bytes (`go test -fuzz FuzzReencode ./module/user_photo/`).

## Credentials from OpenBao (`module/credentials`)

`credentials.OpenBao` is the `credentials.Provider` a service ships: every Valkey, PostgreSQL and object-storage
credential comes from OpenBao (or Vault), through dxlib's vault client (`vault.NewHashiCorpVault`). The service
names each credential once:

    v := vault.NewHashiCorpVault("https://bao.example.test:8200", token, "", "")
    p, err := credentials.NewOpenBao(v, map[string]credentials.OpenBaoSource{
        "valkey/sessions": credentials.KV("secret", "login/valkey"),        // fields username, password
        "objects/photos":  {Kind: credentials.SourceKV, Mount: "secret", Path: "login/objects",
                            UsernameKey: "access_key", PasswordKey: "secret_key"},
        "postgres/app":    credentials.DatabaseRole("database", "login-app"),
    }, credentials.OpenBaoOptions{TokenSource: login})
    err = p.Check(ctx)                    // at start-up: reads every source once
    defer p.Close(ctx)                    // revokes the database leases it issued

- **KV** (version 2): read again on every call, so a holder always gets the newest version. `Version` is the
  KV version, `ExpiresAt` is zero. A holder sees a rotation when its server refuses the old password.
- **Database** (dynamic credentials): one lease per name serves every call. `Version` is the lease id and
  `ExpiresAt` its end. Near the end (`RenewBefore`, one minute, or half the lease when shorter) it is renewed;
  once it cannot be renewed past that (the role's `max_ttl`, a lease revoked out of band) a new lease is issued
  and the old one is left to run out, so transactions still on it finish. A holder whose server refused the
  credential calls `Invalidate` (the optional `credentials.Invalidator` interface) and gets a new lease.
- **The provider's own token**: every lease is a child of it, and OpenBao revokes them all when it ends. The
  provider renews it (`auth/token/renew-self`) near its end, caps each `ExpiresAt` at its end, and asks the
  optional `TokenSource` for a new token when OpenBao refuses the current one (HTTP 403) or it cannot be renewed.
  Give the service a periodic token (or an auth method that issues one). The token handed to the vault client is
  the service's bootstrap secret; it is the one value not read through the provider.
- **TLS**: an http:// address is refused (`AllowHTTP` is for a local test server only), and so is a client that
  does not verify the certificate (`VAULT_SKIP_VERIFY`, which dxlib's client reads from the environment). Give
  the CA with `VAULT_CACERT`. The namespace is the one in `OpenBaoOptions.Namespace`, never `VAULT_NAMESPACE`.
- **Errors** name the OpenBao address, the credential's name, the path and OpenBao's own status and text.

The token's policy, for the sources above:

    path "secret/data/login/*"          { capabilities = ["read"] }
    path "database/creds/login-app"     { capabilities = ["read"] }
    path "sys/leases/renew"             { capabilities = ["update"] }
    path "sys/leases/revoke"            { capabilities = ["update"] }
    path "auth/token/renew-self"        { capabilities = ["update"] }
    path "auth/token/lookup-self"       { capabilities = ["read"] }

A database role whose revocation ends the user's sessions before dropping it, so a revoked or expired lease
leaves no session behind and the role can be dropped:

    creation_statements   = CREATE ROLE "{{name}}" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';
                            GRANT USAGE ON SCHEMA <schema> TO "{{name}}"; GRANT ... ON <schema>.<table> TO "{{name}}";
    revocation_statements = SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = '{{name}}';
                            REVOKE ALL ON ALL TABLES IN SCHEMA <schema> FROM "{{name}}";
                            REVOKE ALL ON ALL SEQUENCES IN SCHEMA <schema> FROM "{{name}}";
                            REVOKE USAGE ON SCHEMA <schema> FROM "{{name}}"; DROP ROLE IF EXISTS "{{name}}";

### PostgreSQL connections (`credentials.PostgresConnection`)

The PostgreSQL counterpart of `RedisConnection`: a dxlib `databases.DXDatabase` pool opened with the provider's
credential. Its `Tx` is the one the `DBStore`s of `module/password`, `module/passkey` and `module/user_photo` take:

    db := credentials.NewPostgresConnection("login", "db.example.test:5432", "app", "postgres/app", p)
    db.ConnectionOptions = "sslmode=verify-full sslrootcert=/etc/ssl/db-ca.pem"
    store, err := password.NewDBStore(db, "login.passwords")

- It asks the provider again near `ExpiresAt` (`RenewBefore`, one minute, or half the credential's lifetime),
  on `Refresh` (a rotation notice), and when PostgreSQL refuses the credential as a transaction begins (SQLSTATE
  28P01 or 28000). A renewed lease only moves the expiry; a new credential opens a new pool and new transactions
  go to it. The old pool is closed when its last running transaction ends, so a rotation loses no work, and the
  sessions (in Valkey, or any table) are untouched.
- A callback runs at most once. Before it runs, a refused credential is retried on a new pool and a connection
  the server had ended (SQLSTATE 57P01, as a revoked lease ends its user's sessions) on the same pool.
- The connection string is built here with every value quoted, so a password cannot add keywords;
  `ConnectionOptions` may not set the user, password, host, port or database. A failed attempt leaves no pool
  running; the next waits `RetryMin` (1 s), doubling up to `RetryMax` (30 s).

The tests start OpenBao (`quay.io/openbao/openbao:2.7.2`), PostgreSQL (`postgres:18.7`) and Valkey in one pod,
so OpenBao's database engine reaches PostgreSQL; each test mounts its own engines, schema, role and policy:

    tools/test-credentials.sh -v        # PODMAN=... names the container command (default podman)

They cover KV versions, one lease per name, renewal past the first TTL (a login still works), a new lease at
`max_ttl`, a transaction open across a rotation, a lease that ran out while idle, a revoked lease replaced once for
eight transactions refused together, token renewal, a refused token replaced from the `TokenSource`, and a Valkey
password rotated in KV with the data kept. Without `DXLIB_MODULE_TEST_OPENBAO` they are skipped.

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
