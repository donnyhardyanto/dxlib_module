# CLAUDE.md

Go library, module `github.com/donnyhardyanto/dxlib_module`, built on `github.com/donnyhardyanto/dxlib`.
The umbrella `../../CLAUDE.md` and `~/.claude/CLAUDE.md` apply as well; this file adds only what is
specific to this repo.

## Rules

- Consumers pin it by tag: dx-service-push-notification and dx-erp's tool-system0-reset pin it in their
  `go.mod`. A change reaches them only after a tag is cut and their pin moves.
- Do not change public API signatures without asking.
- Bugs follow the umbrella's rule for public repos (this repo is public; GitHub issues are disabled):
  one `BUG-NNN` entry per bug in `BUG_OUTSTANDING.md` at the repo root, moved to `BUG_HISTORY.md` when
  fixed, with `Fixes BUG-NNN` in the fixing commit.
- Pushing follows the repo's Push switch (`agentq policy <repo>`, read right before): on = push without
  asking once committed (and approved where Review is on), off = ask; never force-push.
- No Docker. There are no images or compose files; if containers are ever needed, use Podman (`podman`,
  `podman compose`).

## Checks

    go build ./... && go vet ./... && go test ./...

## Dependencies

Every new or upgraded dependency gets an SBOM scan and a licence check before it is committed. The only
manifest is `go.mod` (with `go.sum`).

Tools (Homebrew, except govulncheck):

    brew install syft grype osv-scanner trivy
    go install golang.org/x/vuln/cmd/govulncheck@latest

SBOM scan, from the repo root after `go get ...` and `go mod tidy`:

    syft dir:. -q -o cyclonedx-json=/tmp/dxlib_module-sbom.cdx.json
    grype sbom:/tmp/dxlib_module-sbom.cdx.json
    osv-scanner scan source -r .
    govulncheck ./...
    trivy fs --scanners vuln .

Licences (run `go mod download` first, since syft reads them from the module cache):

    SYFT_GOLANG_SEARCH_LOCAL_MOD_CACHE_LICENSES=true syft dir:. -q -o syft-json \
      | jq -r '.artifacts[] | [.name, .version, ([.licenses[]?.value] | join(" | "))] | @tsv' | sort

Rules:

- Run all the scanners and act on the union: the databases disagree at times (grype has flagged
  `golang.org/x/crypto` releases that osv-scanner passed).
- A new library is adopted only with a clean scan: no known vulnerability in it or anything it pulls in,
  at the version chosen. Otherwise pick a fixed version or another library, or ask the owner.
- An upgrade must not add a finding. An existing finding with no fix yet may stay, but name it.
- A finding no version change can clear goes in `.dependency-allowlist.json` (read by the pre-commit
  scan), only on the owner's word and with govulncheck proof that the code does not call it. README's
  Dependencies section has the file's shape and current entries (GO-2026-5932 in `golang.org/x/crypto`,
  the openpgp advisory, accepted 2026-10-05).
- Trivy has no reachability filter, so `.trivyignore` carries GO-2026-5932 with an expiry date. Do not use
  `--ignore-unfixed`; it hides too much.
- Licence policy is the umbrella's (OSI-approved; ask before GPL, AGPL, LGPL; MPL-2.0 allowed; never
  source-available, community, commercial or key-gated). Here it covers every Go module, every GitHub
  Action in `.github/workflows` and any tool copied into the repo, including what each pulls in. Read the
  licence again on every major upgrade.
- A blank licence or a `sha256:` value means syft did not recognise it: read the module's `LICENSE` in
  `$(go env GOMODCACHE)` by hand.
- Accepted by the owner after the 2026-10-04 scan: `github.com/golang/freetype`, pulled in through
  `dxlib/captcha`, under its FreeType License option (dxlib records the credit in its NOTICE).
- The commit message carries the scan result next to the licences: "SBOM scan clean (syft, grype,
  osv-scanner, govulncheck)", or what was found and how it was resolved.
