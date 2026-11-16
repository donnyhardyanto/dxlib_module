# SBOM of dxlib-module v2.1.0

- Tag: `v2.1.0`
- Commit scanned: `b1d75d7c2a7042ac66b64fe10d4fda3c4e367b55`
- Date: 2026-11-16 16:07 UTC
- Result: not scanned

SBOM: none was made. Not scanned in full: syft dir:. -o cyclonedx-json did not run (~/.local/bin/heavy-lock dxlib-module:sbom -- /bin/sh -c <the scan>: exit none, or not within 1800 s, ); osv-scanner scan source -r . did not run (~/.local/bin/heavy-lock dxlib-module:sbom -- /bin/sh -c <the scan>: exit none, or not within 1800 s, ); `trivy fs --scanners vuln --exit-code 1 .` did not run (~/.local/bin/heavy-lock dxlib-module:sbom -- /bin/sh -c <the scan>: exit none, or not within 1800 s).

## Files
- `reports/govulncheck-0.json`: govulncheck -format json ./... in .
- `tools.json`: the tools and their versions, each step's command and exit status
- `allowlist-applied.json`: the .dependency-allowlist.json entries that applied, with their reasons

## Left out of the scan

- `.build`
- `.worktrees`
- `references`
- `sbom` (the records of the earlier scans)

## Not scanned in full

- syft dir:. -o cyclonedx-json did not run (~/.local/bin/heavy-lock dxlib-module:sbom -- /bin/sh -c <the scan>: exit none, or not within 1800 s, )
- osv-scanner scan source -r . did not run (~/.local/bin/heavy-lock dxlib-module:sbom -- /bin/sh -c <the scan>: exit none, or not within 1800 s, )
- `trivy fs --scanners vuln --exit-code 1 .` did not run (~/.local/bin/heavy-lock dxlib-module:sbom -- /bin/sh -c <the scan>: exit none, or not within 1800 s)

## Run it again

`sbom-record v2.1.0` from the repository's root scans the tag's tree again and replaces this folder (the same day, a step whose inputs, tools and database are unchanged is taken from the scan cache; `AGENTQ_SCAN_CACHE=off` runs every step).
