# Open bugs

One `BUG-NNN` entry per bug; a fixed entry moves to [BUG_HISTORY.md](BUG_HISTORY.md) with the fixing
commit, which says `Fixes BUG-NNN`. The repository's GitHub issues are disabled.

## BUG-003: TestValkeyRefusedCredentialReconnectsOnce fails now and then with "redis: client is closed"

- **Reported**: 2026-11-16, while adding the OpenBao provider. Seen in 2 of 5 runs of the whole credentials
  package after the OpenBao and PostgreSQL tests: `redis_connection_test.go:131: call after the rotation: ping on
  Valkey x at 127.0.0.1:26379/0: redis: client is closed`. On the tree before that change it passed 10 of 10 runs
  alone (`tools/test-valkey.sh -run TestValkeyRefusedCredentialReconnectsOnce -count=10`).
- **Likely cause** (not confirmed): the test sets `CloseGrace` to 10 ms. After `CLIENT KILL`, a call that took the
  old client gets a network error (not `WRONGPASS`), and go-redis retries it after a back-off of 8 ms or more.
  Meanwhile another call reconnects, and the old client is closed 10 ms later, so the retry finds it closed. That
  error is not an authentication error, so `RedisConnection.do` does not reconnect. The default `CloseGrace` of
  30 s does not hit this; a longer grace in the test, or treating `redis: client is closed` as a reason to take the
  new client, would.
- **Meanwhile**: `tools/test-credentials.sh` runs only the OpenBao and PostgreSQL tests; the Valkey tests run
  under `tools/test-valkey.sh`.
