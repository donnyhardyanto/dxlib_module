# Fixed bugs

Entries move here from [BUG_OUTSTANDING.md](BUG_OUTSTANDING.md) when fixed, newest first.

## BUG-002: the reset template still asked for Go 1.27.2 after the move to Go 1.27.3

- **Reported**: 2026-10-28, by the after-the-fact review of 9fc5720 and 63c668e (the Go 1.27.3
  move and the x/net and go-jose bumps, pushed without a review).
- **Cause**: 9fc5720 moved go.mod and the template's `golang:` base image to 1.27.3 but missed the
  Prerequisites line of `tools/reset-databases/templates/HOW_TO_USE.template.md`, which 57f98d3 had
  set to "Go 1.27.2 or higher" in the previous Go move. Generated projects told users to install a Go
  older than the one this module's go.mod requires.
- **Fixed**: 2026-10-28. The line asks for Go 1.27.3 or higher. The rest of the range checked out:
  go.mod says go 1.27.3 with golang.org/x/net v0.62.0 and go-jose/v4 v4.1.6, go.sum matches, build,
  vet and `go test -race` pass, and govulncheck, grype, osv-scanner and trivy find nothing but the
  accepted GO-2026-5932.

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
