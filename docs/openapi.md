# OpenAPI specification

The Gamera HTTP API is described by an OpenAPI 3 document that is **generated
from the Go request/response types**, so it stays in sync with the code.

## Where to get it

- **Live, from a running operator:** `GET /api/openapi.json` returns the spec as
  JSON. For example, with the UI/API port-forwarded to `localhost:8082`:

  ```bash
  curl -s http://localhost:8082/api/openapi.json | jq .
  ```

- **Checked in:** [`docs/openapi.yaml`](openapi.yaml) is a committed copy for
  browsing, diffing in review, and feeding client generators / docs viewers
  (Swagger UI, Redoc, `openapi-generator`, etc.).

## How it is generated

The spec is built by reflecting the handler request/response types with
[`swaggest/openapi-go`](https://github.com/swaggest/openapi-go). The route table
lives in `internal/api/openapi.go` (`apiEndpoints`) and mirrors the routes
registered in `Server.Handler`; the DTOs in `internal/api/dto.go` become the
component schemas.

Regenerate the checked-in file after changing any endpoint or DTO:

```bash
make openapi        # writes docs/openapi.yaml
# or:
go generate ./internal/api/...
```

A unit test (`TestOpenAPIYAMLInSync`) fails if `docs/openapi.yaml` is stale, so
`make test` catches a forgotten regeneration.

## Adding or changing an endpoint

1. Add/adjust the route in `Server.Handler` and its handler.
2. Add/adjust the matching entry in `apiEndpoints` (method, path, request and
   response types, status codes) in `internal/api/openapi.go`. Use small
   path/query request structs (with `path:"…"` / `query:"…"` tags) for
   parameters, and the DTO types for bodies.
3. Run `make openapi` and commit the updated `docs/openapi.yaml`.
