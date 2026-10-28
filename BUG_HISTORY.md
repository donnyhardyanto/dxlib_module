# Fixed bugs

Entries move here from [BUG_OUTSTANDING.md](BUG_OUTSTANDING.md) when fixed, newest first.

## BUG-001: v1.73.0 claimed an SBOM it did not carry; v1.73.0 and v1.74.0 had no `sbom/<tag>/` records

- **Reported**: 2026-10-28, by a consumer whose audit needs every release's SBOM. The notes of
  v1.73.0 said "SBOM: dxlib-module-1.73.0.cdx.json (CycloneDX, 117 components)", but the release had
  no asset. v1.74.0 carries its CycloneDX file, but neither release had `sbom/<tag>/` records in the
  repository, and there was no SPDX SBOM or scanner report for either.
- **Cause**: v1.73.0's GitHub release was published (and so made immutable) before the SBOM upload
  ran; GitHub refused the upload with HTTP 422, and the scan summary still went into the notes,
  claiming the file. The `sbom/<tag>/` records were introduced on 2026-10-28, after both releases.
- **Fixed**: 2026-10-28. `sbom/v1.73.0/` and `sbom/v1.74.0/` were scanned from their tags and
  committed (CycloneDX and SPDX SBOMs, the reports of grype, osv-scanner and govulncheck, the tools'
  versions and the accepted findings that applied); the README's Compliance and Security section says
  where the SBOMs are and how to run the scan again. v1.73.0's notes now say the SBOM is not attached
  and point to the records. v1.73.0 stays without an asset: an immutable release takes none. Both
  trees carry the three findings in golang.org/x/net 0.61.0 and go-jose/v4 4.1.5 that main fixed
  afterwards (63c668e), so the records say `findings`; a release from main after that commit is clean.
