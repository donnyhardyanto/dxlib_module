# dxlib_module

Released under the MIT License; see [LICENSE](LICENSE).

## Dependencies

Every new or upgraded dependency is scanned and its licence checked before the change is committed.
From the repo root, after `go get` and `go mod tidy`:

    syft dir:. -q -o cyclonedx-json=/tmp/dxlib_module-sbom.cdx.json
    grype sbom:/tmp/dxlib_module-sbom.cdx.json
    osv-scanner scan source -r .
    govulncheck ./...

The databases disagree at times, so all four run and the union counts. Every dependency must be open
source under an OSI-approved licence; syft lists the licences from the module cache after
`go mod download`:

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
