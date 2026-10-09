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
