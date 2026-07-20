# Operations

Deployment and connector setup for compliary's maintainer instance at `compliary.danny.vn`.
Infra mirrors banhmi's AWS shape: CloudFront (TLS termination) -> ECS -> RDS.

## Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `COMPLIARY_PUBLIC_URL` | for OAuth | Public URL of the instance (e.g. `https://compliary.danny.vn`). Determines issuer, metadata endpoints, redirect targets. |
| `COMPLIARY_OAUTH_OPERATOR_SECRET` | for OAuth | bcrypt hash of the operator's password. Generate: `htpasswd -nbBC 10 "" 'your-password' \| cut -d: -f2` |
| `COMPLIARY_MCP_TOKEN` | for bearer / fallback | Static bearer token for CLI/script access. If set alongside OAuth, both mechanisms are accepted. |
| `COMPLIARY_MCP_PUBLIC` | no | `true` to serve reduced projection anonymously when no auth is configured. Default: `false` (401 on `/mcp`). |
| `COMPLIARY_MCP_ALLOWED_ORIGINS` | no | Comma-separated origins for MCP cross-origin protection. |
| `COMPLIARY_TRUST_PROXY` | no | `true` when behind a reverse proxy (CloudFront). Uses the leftmost `X-Forwarded-For` entry as the client IP for rate limiting. |
| `COMPLIARY_MCP_RATE_RPS` | no | Global per-IP request rate (default 50). |
| `COMPLIARY_MCP_RATE_BURST` | no | Global per-IP burst capacity (default 100). |
| `COMPLIARY_OAUTH_RATE_PER_MIN` | no | Tight per-IP limit on `POST /oauth/authorize` and `POST /oauth/token` (brute-force gate; default 10/min). |
| `PORT` | no | Listen port (default 8088). |

## Generating the operator password hash

```bash
# Generate bcrypt hash (cost 10):
htpasswd -nbBC 10 "" 'your-password' | cut -d: -f2

# Or with Go:
# go run -e 'import "golang.org/x/crypto/bcrypt"; h,_:=bcrypt.GenerateFromPassword([]byte("your-password"),10); fmt.Println(string(h))'

# Set the env var (the hash, not the password):
export COMPLIARY_OAUTH_OPERATOR_SECRET='$2y$10$...'
```

## TLS

TLS terminates at CloudFront. The ECS task listens on plain HTTP. `COMPLIARY_TRUST_PROXY=true`
ensures rate limiting uses the real client IP from `X-Forwarded-For`.

## Connecting as a claude.ai custom connector

1. Set `COMPLIARY_PUBLIC_URL=https://compliary.danny.vn` and `COMPLIARY_OAUTH_OPERATOR_SECRET`.
2. In Claude settings -> Connected apps -> Add MCP server -> enter `https://compliary.danny.vn/mcp`.
3. Claude discovers `/.well-known/oauth-protected-resource`, finds the authorization server, fetches
   `/.well-known/oauth-authorization-server`.
4. Claude registers via CIMD (preferred) or DCR, then opens the authorization URL in a browser.
5. Enter the operator password on the consent form -> Claude receives an access token.
6. All five MCP tools (`guide`, `corpus_status`, `search`, `document`, `quality_gaps`) are available
   with full projection.

**Redirect URI:** `https://claude.ai/api/mcp/auth_callback` (Claude.ai handles this automatically).

## Connecting as a ChatGPT connector

Same flow as Claude. ChatGPT discovers the OAuth metadata, registers via CIMD or DCR, and presents
the authorization form to the operator.

**Redirect URI:** `https://chatgpt.com/connector/oauth/{callback_id}` (ChatGPT assigns this).

## Connecting via Claude Code (CLI)

Claude Code uses localhost redirect URIs with varying ports. The server matches
`http://localhost/callback` and `http://127.0.0.1/callback` port-agnostically.

## Static bearer token (CLI/scripts)

For non-interactive access (curl, scripts, CI), set `COMPLIARY_MCP_TOKEN` alongside OAuth:

```bash
export COMPLIARY_MCP_TOKEN='your-static-token'
curl -H "Authorization: Bearer $COMPLIARY_MCP_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"guide"},"id":1}' \
  https://compliary.danny.vn/mcp
```

Both OAuth tokens and the static bearer token are accepted when both env vars are set.
