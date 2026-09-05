# Binding DCR Clients to the MCP Server They Registered With

English | [日本語](dcr-client-server-binding.ja.md)

## Overview

A client registered through dynamic client registration (RFC 7591) at `POST /{server_name}/auth/clients` may only use the authorization endpoint of that same MCP server. Presenting its `client_id` to `GET /{other_server}/auth/login` is refused with `invalid_client` (401).

Clients resolved through a client ID metadata document (CIMD) are not affected: they carry no registering server and stay usable across every MCP server.

## Why the binding exists

Two reasons, both modest.

**A registration belongs to the authorization server that issued it.** Manifold advertises a separate authorization server per MCP server: the metadata of `{server_name}` names `{base_url}/mcp/{server_name}` as its `issuer` and `/{server_name}/auth/clients` as its `registration_endpoint`. A `client_id` minted under one issuer has no meaning under another, so there is no reason to honor it there.

**A `client_id` registered elsewhere becomes visible.** A rejection is logged with both server names, so bringing another server's `client_id` to this one shows up in the audit log instead of passing silently.

**On its own this does not prevent an attacker from obtaining another server's upstream token.** `POST /{server_name}/auth/clients` is registered on the same mux for every server, and the `middleware.MCPServerApp` in front of it (`pkg/interfaces/http/middleware/mcp_server.go`) only resolves the server from the path and puts it into the request context — it neither authenticates the caller nor restricts access. An attacker who wants `server-b` registers directly at `server-b`'s registration endpoint and passes the binding check. Closing "register at A, present at B" merely replaces it with "register at B, present at B".

What actually restricts this is `mcpServers.<name>.oauth2.clients` together with `unknownClient: reject`. `resolveUpstreamClient` (`pkg/interfaces/http/auth_handler.go`) decides solely on whether the `client_id` appears in that mapping, regardless of where it was registered.

## What is checked

`LoginEndpoint` compares the registering server recorded on the client with the server named in the request, immediately after the client is resolved and before the `redirect_uri` is matched.

| Client source | Recorded `mcp_server_name` | MCP servers it may use |
| ------------- | -------------------------- | ---------------------- |
| DCR (`source: dcr`) | the server recorded at registration time | that server only |
| DCR registered before this change (`source` empty) | the server recorded at registration time | that server only |
| CIMD (`source: cimd`) | empty by design | any server |

For `POST /{server_name}/auth/clients` the recorded server is the one in the path. `POST /register` carries no server name at all and takes it from the submitted `client_name` instead; the binding then applies to that server.

A rejection logs the downstream `client_id`, the client name, the registering server name, and the requested server name for auditing. The reason is never returned to the client; the response body is `invalid_client`.

The `/authorize` alias carries no server name in its path. There, Manifold still resolves the server from the client's own `mcp_server_name`, so the two always agree and the check never rejects that route. A CIMD client has no `mcp_server_name` and therefore cannot use `/authorize` at all — it must call `/{server_name}/auth/login`.

## Configurations affected

Under RFC 7591 a client registers with each authorization server separately and keeps the returned `client_id` keyed by issuer, so a spec-compliant DCR client never presents one server's `client_id` to another. There is normally no affected configuration. Only an implementation that reuses a single DCR-issued `client_id` across several `mcpServers` entries now gets `invalid_client` (401).

The default of `unknownClient` is unchanged: `reject` when `clients` is non-empty, `default` when it is empty.

## Related

- [Downstream client registration](../../README.md#downstream-client-registration) — DCR, CIMD, and the mapping from downstream to upstream clients
