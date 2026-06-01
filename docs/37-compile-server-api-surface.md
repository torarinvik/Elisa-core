# Compile server API surface

This note documents implemented `-emit serve` behavior and the current HTTP API
contract.

## Start server

```sh
go run ./src -emit serve
go run ./src -emit serve -addr 127.0.0.1:9000
```

Default address is `127.0.0.1:8080` when `-addr` is omitted.

## Endpoints

- `GET /healthz` -> plain text `ok` plus newline
- `POST /api/v1/compile` -> JSON request and JSON response

Non-`POST` methods on `/api/v1/compile` return method-not-allowed response.

## Request JSON

```json
{
  "mode": "semantic",
  "filter": "function=contains:parse_expr",
  "filename": "request.elisa",
  "source": "def main() -> int:\n    return 0\n",
  "ir": ""
}
```

Current request rules:

- request must include exactly one of `source` or `ir`
- `ir` payload uses base64-encoded frontend IR bundle bytes
- if `filename` is omitted for source mode, server uses `request.elisa`
- if `filename` is omitted for IR mode, server uses bundle `SourceFilename`; if that is blank, `request.elisair`
- if `mode` is empty, server defaults to `ast`

## Response JSON

```json
{
  "ok": true,
  "output": "...",
  "ir": "",
  "value": "",
  "stderr": "",
  "error": "",
  "error_code": ""
}
```

Fields used by current modes:

- `output`: text output for report and codegen modes
- `output`: for `interpret`, this field carries interpreter stdout text
- `ir`: base64 frontend IR for `mode: "ir"`
- `value`: interpreter return value (string form) when non-void
- `stderr`: collected warnings or diagnostics text
- `error` and `error_code`: failure metadata

## Supported modes

Current compile server modes:

- `ast`
- `lowered`
- `semantic`
- `facts` (supports `filter`)
- `unsafe`
- `progress`
- `fmt`
- `doc`
- `ir`
- `llvm`
- `interpret`

`mode` normalization follows the same alias mapping as CLI `-emit` modes.

## Error behavior

- malformed request JSON -> HTTP 400
- invalid source or analysis failures -> HTTP 400 with `ok: false`
- unsupported mode -> HTTP 400 with `error`
- non-`POST` compile request -> HTTP 405 with `error: "only POST is supported"`
- invalid fact filter shape -> HTTP 400 with `error_code: "fact_trace_filter"`
- backend encode or generation failures return non-OK responses with error text

## IR round-trip pattern

Current server supports:

1. `mode: "ir"` with source -> receive base64 `ir`
2. reuse `ir` with `mode: "interpret"` or `mode: "llvm"`

This enables stateless frontend build once, then multiple backend or runtime
queries.
