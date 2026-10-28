# SBOM of dxlib-module v1.75.0

- Tag: `v1.75.0`
- Commit scanned: `5aa809974dc4f278fb01d308a07af3717f51db14`
- Date: 2026-10-28 12:17 UTC
- Result: clean

SBOM: dxlib-module-1.75.0.cdx.json (CycloneDX, 118 components). Scan (syft 1.55.0, grype 0.121.0, osv-scanner 2.7.0, govulncheck v1.8.0, `trivy fs --scanners vuln --exit-code 1 .`): clean. Accepted: GO-2026-5932 in golang.org/x/crypto 0.58.0 (./go.mod) (.dependency-allowlist.json).

## Files
- `dxlib-module-1.75.0.cdx.json`: CycloneDX JSON, from syft
- `dxlib-module-1.75.0.spdx.json`: SPDX JSON, from the same syft run
- `reports/grype.json`: grype sbom:dxlib-module-1.75.0.cdx.json -o json
- `reports/osv-scanner.json`: osv-scanner scan source -r --allow-no-lockfiles --format json .
- `reports/osv-scanner.stderr.txt`: what osv-scanner wrote to stderr
- `reports/govulncheck-0.json`: govulncheck -format json ./... in .
- `reports/scanner-0.txt`: trivy fs --scanners vuln --exit-code 1 .
- `reports/scanner-0.stderr.txt`: what scanner-0 wrote to stderr
- `tools.json`: the tools and their versions, each step's command and exit status
- `allowlist-applied.json`: the .dependency-allowlist.json entries that applied, with their reasons

## Left out of the scan

- `.build`
- `.worktrees`
- `references`
- `sbom` (the records of the earlier scans, #346)

## Run it again

`sbom-record v1.75.0` from the repository's root scans the tag's tree again and replaces this folder.
