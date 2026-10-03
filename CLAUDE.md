# CLAUDE.md

Go library, module `github.com/donnyhardyanto/dxlib_module`, built on `github.com/donnyhardyanto/dxlib`.
Consumers pin it by tag through their own `go.mod` (dx-service-push-notification pins an older version;
dx-erp carries a nested copy), so a change here reaches them only after a tag is cut and their pin moves.
Do not change public API signatures without asking.

## Checks

    go build ./... && go vet ./... && go test ./...

Bugs are tracked in GitHub issues only, one per bug, with `Fixes #N` in the fixing commit. There are no
`BUG_OUTSTANDING.md` or `BUG_HISTORY.md` files (in any spelling); do not create them. Ask before pushing;
never force-push.

Docker is not used. The library has no images or compose files; if containers are ever needed, they use
Podman (`podman`, `podman compose`).

## Dependencies: SBOM scan

Every dependency upgrade or new dependency gets an SBOM scan before it is committed. The only manifest is
`go.mod` (with `go.sum`); there are no images, npm or other package files.

Tools (all via Homebrew except govulncheck):

    brew install syft grype osv-scanner
    go install golang.org/x/vuln/cmd/govulncheck@latest

Run from the repo root after changing the dependency (`go get ...` and `go mod tidy`):

    syft dir:. -q -o cyclonedx-json=/tmp/dxlib_module-sbom.cdx.json
    grype sbom:/tmp/dxlib_module-sbom.cdx.json
    osv-scanner scan source -r .
    govulncheck ./...

The databases disagree at times (grype has flagged `golang.org/x/crypto` releases that osv-scanner passed), so
run all of them and act on the union.

Rules:

- A new library is adopted only when the scan is clean: no known vulnerability in it or in anything it pulls
  in, at the version chosen. If it is not clean, choose a fixed version or a different library, or ask the
  owner.
- An upgrade must not add a finding. A finding that was already there and has no fix yet may stay, but name it.
- Put the result in the commit message: "SBOM scan clean (syft, grype, osv-scanner, govulncheck)", or what was
  found and how it was resolved.

## Dependencies: licence check

Every dependency must be open source. Check the licence of each new or upgraded dependency (Go module, GitHub
Action in `.github/workflows`, or any tool copied into the repo) at the same time as the SBOM scan, including
what it pulls in.

- Allowed: OSI-approved licences such as MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0, ISC, and MPL-2.0
  (weak copyleft that applies per file).
- Ask the owner first: GPL, AGPL and LGPL.
- Never: source-available, "community", commercial or key-gated licences.
- A new major version can change the licence (PrimeVue 5 moved to the proprietary PrimeUI License, so dx-erp
  stays on PrimeVue 4.x), so read it again on every major upgrade.

List the licences from the repo root (syft reads them from the Go module cache, so run `go mod download` first):

    SYFT_GOLANG_SEARCH_LOCAL_MOD_CACHE_LICENSES=true syft dir:. -q -o syft-json \
      | jq -r '.artifacts[] | [.name, .version, ([.licenses[]?.value] | join(" | "))] | @tsv' | sort

A blank licence or a `sha256:` value means syft did not recognise the text; read the module's `LICENSE` in
`$(go env GOMODCACHE)` by hand. Put the result next to the SBOM result in the commit message.

Accepted by the owner after the scan of 2026-10-04: `github.com/golang/freetype`, pulled in through
`dxlib/captcha`, taken under its FreeType License option (dxlib records the credit in its NOTICE).
