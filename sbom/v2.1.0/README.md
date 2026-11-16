# SBOM of dxlib-module v2.1.0

- Tag: `v2.1.0`
- Commit scanned: `b1d75d7c2a7042ac66b64fe10d4fda3c4e367b55`
- Date: 2026-11-16 16:16 UTC
- Result: clean

SBOM: dxlib-module-2.1.0.cdx.json (CycloneDX, 125 components). Scan (syft 1.57.1, grype 0.121.3, osv-scanner 2.7.1, govulncheck v1.9.0, `trivy fs --scanners vuln --exit-code 1 .`): clean.

## Files
- `dxlib-module-2.1.0.cdx.json`: CycloneDX JSON, from syft
- `dxlib-module-2.1.0.spdx.json`: SPDX JSON, from the same syft run
- `reports/grype.json`: grype sbom:dxlib-module-2.1.0.cdx.json -o json
- `reports/osv-scanner.json`: osv-scanner scan source -r --allow-no-lockfiles --no-call-analysis=go --format json .
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
- `sbom` (the records of the earlier scans)

## Reused from the scan cache

- govulncheck-0 reused (key b4d27489f494, made 23:07 for dxlib-module 2.1.0 (b1d75d7c2))

## Run it again

`sbom-record v2.1.0` from the repository's root scans the tag's tree again and replaces this folder (the same day, a step whose inputs, tools and database are unchanged is taken from the scan cache; `AGENTQ_SCAN_CACHE=off` runs every step).
