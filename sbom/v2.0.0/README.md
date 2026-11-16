# SBOM of dxlib-module v2.0.0

- Tag: `v2.0.0`
- Commit scanned: `ab6c0b1d00fe9fcbb445120edd334c9a47baeea8`
- Date: 2026-11-16 01:42 UTC
- Result: findings

SBOM: dxlib-module-2.0.0.cdx.json (CycloneDX, 125 components). Scan (syft 1.57.1, grype 0.121.3, osv-scanner 2.7.1, govulncheck v1.9.0, `trivy fs --scanners vuln --exit-code 1 .`): 1 finding not allowlisted: CVE-2026-116591/GHSA-fpmh-frq8-7rj8/GO-2026-6865 in github.com/klauspost/compress 1.20.1 (go.mod) (fixed in 1.20.2). Accepted: GO-2026-5932 in golang.org/x/crypto 0.58.0 (go.mod) (.dependency-allowlist.json).

## Files
- `dxlib-module-2.0.0.cdx.json`: CycloneDX JSON, from syft
- `dxlib-module-2.0.0.spdx.json`: SPDX JSON, from the same syft run
- `reports/grype.json`: grype sbom:dxlib-module-2.0.0.cdx.json -o json
- `reports/osv-scanner.json`: osv-scanner scan source -r --allow-no-lockfiles --no-call-analysis=go --format json .
- `reports/osv-scanner.stderr.txt`: what osv-scanner wrote to stderr
- `reports/govulncheck-0.json`: govulncheck -format json ./... in .
- `reports/scanner-0.txt`: trivy fs --scanners vuln --exit-code 1 .
- `reports/scanner-0.stderr.txt`: what scanner-0 wrote to stderr
- `tools.json`: the tools and their versions, each step's command and exit status
- `allowlist-applied.json`: the .dependency-allowlist.json entries that applied, with their reasons
- `osv-scanner.toml`: osv-scanner's ignores for this folder: the root's entries and the findings of v2.0.0 recorded here, so a whole-tree osv-scanner run does not count this release's findings as the current tree's

## Left out of the scan

- `.build`
- `.worktrees`
- `references`
- `sbom` (the records of the earlier scans)

## Reused from the scan cache

- govulncheck-0 reused (key ce24eb343612, made 08:10 for dep-scan of dxlib_module at 0bca345)

## Run it again

`sbom-record v2.0.0` from the repository's root scans the tag's tree again and replaces this folder (the same day, a step whose inputs, tools and database are unchanged is taken from the scan cache; `AGENTQ_SCAN_CACHE=off` runs every step).
