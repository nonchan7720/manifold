# OAuth 2.0 backend example — Google Calendar

Exposes the Google Calendar REST API as MCP tools, with Manifold handling the OAuth 2.0 flow against Google. MCP clients authenticate to Manifold via its built-in OAuth 2.1 server (PKCE), and Manifold exchanges that for a Google access token behind the scenes.

## Prerequisites

1. Create an OAuth client in the [Google Cloud Console](https://console.cloud.google.com/apis/credentials) (type: Web application).
2. Add `http://localhost:9999/google-calendar/auth/callback` as an authorized redirect URI.
3. Enable the Google Calendar API for the project.

## Run

```bash
cd examples/oauth2-backend
export ENCRYPT_KEY=$(openssl rand -base64 32)
export GOOGLE_CLIENT_ID=your-client-id
export GOOGLE_CLIENT_SECRET=your-client-secret
manifold gateway
```

## Try it

Connect from Claude Code:

```bash
claude mcp add --transport http google-calendar http://localhost:9999/mcp/google-calendar
```

On the first tool call, the client is redirected through Manifold's OAuth 2.1 flow, which in turn drives Google's consent screen. Tokens are encrypted with `encryptKey` and stored in SQLite.

## Adapting to your own OAuth API

Swap `spec`, `baseURL`, and the `oauth2` block for your provider. The redirect URI to register with the provider is always:

```
http(s)://<manifold-host>/<server_name>/auth/callback
```
