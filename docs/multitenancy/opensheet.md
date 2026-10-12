# opensheet request scope

The template page is [`request scope`](request-scope.md). This page is what opensheet's data plane
adds to its S3 path.

## Sheets resolve under the project scope

`internal/dataplane/scope.go` resolves the org slug through the definer wrapper and the project
under the org scope, as the template does, then runs the tenanted `sheet.BySlug` under that same
project scope. The `/spreadsheets/{id}/tabs` routes name a spreadsheet id, not a slug, so they
resolve org and project only. Every failure, the sheet lookup included, is the one masked
`NotFoundError`.

A key's `ResourceIDs` name sheets. A key restricted to some sheets is refused on the
`/spreadsheets/{id}/tabs` routes, which authorize project-wide (`AuthorizeProject`).

A `public` sheet is read with no credential at all, and only while `sheets.publicEnabled` is on.
Writes always need a key.

## Routes

The enforced list is the probe table in `internal/boot/route_scope_test.go`:

```
GET    /api/v1/orgs/{org}/projects/{project}/sheets/{slug}
POST   /api/v1/orgs/{org}/projects/{project}/sheets/{slug}
GET    /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/capabilities
GET    /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows/{id}
POST   /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows
POST   /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows/batch
PUT    /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows/{id}
PATCH  /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows/{id}
DELETE /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows/{id}
DELETE /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/cache
GET    /api/v1/orgs/{org}/projects/{project}/spreadsheets/{id}/tabs
POST   /api/v1/orgs/{org}/projects/{project}/spreadsheets/{id}/tabs
```
