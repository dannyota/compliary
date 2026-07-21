# Operations

Deployment and connector setup for compliary's maintainer instance at `compliary.danny.vn`.
Infra mirrors banhmi's AWS shape: CloudFront (TLS termination) -> ECS -> RDS.

## Deployment topology — co-located on banhmi's ECS

The maintainer instance runs as **one extra ECS service on banhmi's existing EC2
host**, not a separate stack. Full runbook: [`deploy/aws/setup-checklist.md`](../deploy/aws/setup-checklist.md).

**Releases are one command** once the stack exists:
[`deploy/aws/release.sh <x.y.z>`](../deploy/aws/release.sh) — builds the arm64
image (cross-compiled, any host), pushes version+latest to ECR, registers a
digest-pinned task-definition revision, rolls the service, and verifies the
live version chip, `/healthz`, and the 401 on unauthenticated `/mcp`.

```
                 CloudFront (TLS, X-Origin-Verify)
   banhmi.danny.vn ─┐   laksa/… ─┐   compliary.danny.vn ─┐
                    ▼            ▼                        ▼
     ec2-…compute.amazonaws.com:8081  :8082 …     (same EC2 host):8084
   ┌──────────────────── one t4g.medium (ECS cluster banhmi) ───────────────────────┐
   │  banhmi-mcp        banhmi-embedder            compliary-mcp                     │
   │  cmd/server        cmd/embedder  ◀─ HTTP ───  cmd/server                        │
   │  :8081             127.0.0.1:8089  /embeddings (COMPLIARY_EMBED_ENDPOINT)       │
   └──────────────────────────────────────┬───────────────────────────────────────┘
                                           ▼
                       RDS Postgres (shared instance; DB `compliary`)
```

- **Only `cmd/server` is deployed** (slim, CGO-free, no ONNX):
  [`deploy/containerfiles/Containerfile.ecs.server`](../deploy/containerfiles/Containerfile.ecs.server).
- **Query embedding is delegated** to banhmi's embedder over loopback
  (`COMPLIARY_EMBED_ENDPOINT=http://127.0.0.1:8089`). That embedder binds
  `127.0.0.1` only, so co-location on the same instance is required. Same
  Qwen3-Embedding-0.6B / 1024-d as the Kaggle-embedded corpus, so vectors align.
- **Shared with banhmi:** EC2 host, ECS cluster `banhmi`, embedder + its
  `/banhmi/embed-token`, RDS instance + security group, Elastic IP / origin.
  **New for compliary:** RDS database `compliary`, SG rule for port 8084, its
  **own** `compliary.danny.vn` ACM cert (banhmi uses per-domain certs), a
  CloudFront distribution, and the secrets below.
- **`COMPLIARY_ORIGIN_VERIFY_SECRET`** enforces CloudFront-only ingress: a request
  hitting the origin directly (or via another distribution) lacks `X-Origin-Verify`
  and is 403'd — which is what makes the `COMPLIARY_TRUST_PROXY` XFF handling sound.

## Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `COMPLIARY_PUBLIC_URL` | for OAuth | Public URL of the instance (e.g. `https://compliary.danny.vn`). Determines issuer, metadata endpoints, redirect targets. |
| `COMPLIARY_OAUTH_OPERATOR_SECRET` | for OAuth | bcrypt hash of the operator's password. Generate: `htpasswd -nbBC 10 "" 'your-password' \| cut -d: -f2` |
| `COMPLIARY_MCP_TOKEN` | for bearer / fallback | Static bearer token for CLI/script access. If set alongside OAuth, both mechanisms are accepted. |
| `COMPLIARY_MCP_PUBLIC` | no | `true` to serve reduced projection anonymously when no auth is configured. Default: `false` (401 on `/mcp`). |
| `COMPLIARY_EMBED_ENDPOINT` | for HTTP embed | OpenAI-compatible embeddings base URL (e.g. `http://127.0.0.1:8089`, banhmi's embedder). When set, the server calls it for query embedding and packages no ONNX. Unset → in-process ONNX (local, needs `-tags onnx`). |
| `COMPLIARY_EMBED_TOKEN` | no | Bearer token sent to `COMPLIARY_EMBED_ENDPOINT`. For banhmi's embedder this is the value of `/banhmi/embed-token`. |
| `COMPLIARY_ORIGIN_VERIFY_SECRET` | for CloudFront | Secret(s) that must arrive in the `X-Origin-Verify` header (injected by the CloudFront distribution). Non-`/healthz` requests without a match get 403. Comma-separated for zero-downtime rotation. Empty → disabled. |
| `COMPLIARY_MCP_ALLOWED_ORIGINS` | no | Comma-separated origins for MCP cross-origin protection. |
| `COMPLIARY_TRUST_PROXY` | no | `true` when behind a reverse proxy (CloudFront). The client IP for rate limiting is the entry the trusted edge **appended** to `X-Forwarded-For` (position `len − hops`), not the client-controllable leftmost entry. |
| `COMPLIARY_DATABASE_HOST` | no | Postgres host (default `localhost`). Set to the RDS endpoint in deployment. |
| `COMPLIARY_DATABASE_PORT` | no | Postgres port (default `10011` for the local podman stack; `5432` for RDS). |
| `COMPLIARY_DATABASE_USER` | no | Postgres user (default `compliary`). |
| `COMPLIARY_DATABASE_NAME` | no | Postgres database (default `compliary`). |
| `COMPLIARY_DATABASE_SSLMODE` | no | libpq sslmode (default `disable` for local; use `require` for RDS). |
| `COMPLIARY_DATABASE_PASSWORD` | for DB auth | Postgres password. Secret — from SSM in deployment, never the config file. |
| `COMPLIARY_TRUSTED_PROXY_HOPS` | no | Number of appending proxies between the client and this server (default `1` for a single CloudFront edge). Set `2` behind CloudFront→ALB. Too-low over-restricts (fail-safe); too-high risks trusting a client-supplied entry. |
| `COMPLIARY_OAUTH_RATE_PER_MIN` | no | Per-IP attempt budget for `POST /oauth/authorize` and `POST /oauth/token` (brute-force gate; default 10). |
| `COMPLIARY_MCP_RATE_RPS` | no | Global per-IP request rate (default 50). |
| `COMPLIARY_MCP_RATE_BURST` | no | Global per-IP burst capacity (default 100). |
| `PORT` | no | Listen port (default 8088). |

## Monitoring (one-time, requires admin credentials)

**Done 2026-07-21:** scan-on-push enabled, `compliary-alerts` SNS topic + email subscription,
Route53 healthz check (30s interval) + `compliary-healthz-down` alarm (state OK). The commands
below are the re-creation reference. The deploy CLI credential is deliberately deploy-scoped
and cannot manage monitoring; run these from an admin profile (or a temporary least-privilege
grant, removed after):

```bash
# 1. Vulnerability scan on every image push:
aws ecr put-image-scanning-configuration --repository-name compliary-mcp \
  --image-scanning-configuration scanOnPush=true --region ap-southeast-1

# 2. Alert topic + email subscription (confirm the email AWS sends):
aws sns create-topic --name compliary-alerts --region us-east-1
aws sns subscribe --topic-arn arn:aws:sns:us-east-1:<ACCOUNT_ID>:compliary-alerts \
  --protocol email --notification-endpoint <operator email> --region us-east-1

# 3. External healthz check + alarm (Route53 health-check metrics live in us-east-1):
aws route53 create-health-check --caller-reference compliary-healthz-1 \
  --health-check-config Type=HTTPS,FullyQualifiedDomainName=compliary.danny.vn,ResourcePath=/healthz,RequestInterval=30,FailureThreshold=3
aws cloudwatch put-metric-alarm --alarm-name compliary-healthz-down \
  --namespace AWS/Route53 --metric-name HealthCheckStatus \
  --dimensions Name=HealthCheckId,Value=<health check id> \
  --statistic Minimum --period 60 --evaluation-periods 3 \
  --threshold 1 --comparison-operator LessThanThreshold \
  --alarm-actions arn:aws:sns:us-east-1:<ACCOUNT_ID>:compliary-alerts --region us-east-1
```

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
