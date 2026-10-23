# Upstream CIMD (OAuth Client ID Metadata Document)

[日本語](upstream-cimd.ja.md)

When Manifold connects to an upstream MCP server as an OAuth client, it can identify itself with a Client ID Metadata Document (CIMD) instead of Dynamic Client Registration (DCR). Manifold does not serve the document. You host it at a public HTTPS URL (S3, a CDN, and so on) and pass that URL in the configuration.

## Configuration

Set `cimdClientID` directly on the server entry. Do not put it under `oauth2`: configuring `oauth2` turns off OAuth auto-discovery for the server, and `cimdClientID` relies on it.

```yaml
mcpServers:
  github:
    description: GitHub
    transport: http
    url: https://mcp.example.com/mcp
    cimdClientID: https://cdn.example.com/manifold/github.json
```

- The value must be an `https://` URL. It is valid only for `transport: http` servers and cannot be combined with `oauth2`.
- If the upstream authorization server advertises `client_id_metadata_document_supported: true`, the URL becomes the `client_id` and no client secret is used. This takes priority over DCR, and the upstream needs no registration endpoint.
- Otherwise (the option is unset, or the upstream does not support CIMD) Manifold falls back to DCR, and logs a warning in the second case.
- The discovery log line carries `cimd=true` when the document URL was used, and `client_registration=cimd|dcr` for the method actually used.

## Choosing the registration method (`clientRegistration`)

`clientRegistration` is a server-level enum, placed next to `cimdClientID` (not under `oauth2`).

| Value | Behavior |
| --- | --- |
| unset | Tries in order: CIMD if `cimdClientID` is set and the upstream supports it, otherwise DCR. The fallback warning is logged only in this mode. |
| `cimd` | CIMD only. `cimdClientID` is required. Only authorization servers with `client_id_metadata_document_supported: true` are used; if none exists, discovery fails and does not fall back to DCR. |
| `dcr` | DCR only. Only authorization servers with a `registration_endpoint` are used, even if the upstream supports CIMD. Cannot be combined with `cimdClientID`. |

Like `cimdClientID`, it is valid only for `transport: http` MCP backends and cannot be combined with `oauth2`.

## Document template

```json
{
  "client_id": "https://cdn.example.com/manifold/github.json",
  "client_name": "manifold",
  "redirect_uris": ["https://manifold.example.com/github/auth/callback"],
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "none"
}
```

`redirect_uris` must contain `<baseURL>/<server>/auth/callback`, where `<baseURL>` is the externally visible base URL of Manifold and `<server>` is the key under `mcpServers`.

## Hosting requirements

The upstream authorization server fetches the document, so it has to be:

- Publicly reachable with an unauthenticated, unsigned `GET`.
- Served directly, with no redirects.
- Served with `Content-Type: application/json`.
- Self-referencing: `client_id` in the document must be a string-for-string match with the URL the document is hosted at (and with `cimdClientID`).
- Updated whenever the Manifold base URL or server name changes, so that `redirect_uris` stays correct.
