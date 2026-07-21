# compliary AWS deploy — co-location on banhmi's ECS

compliary deploys as **one additional ECS service** on the **existing banhmi EC2
host**, reusing banhmi's embedder, RDS instance, origin IP, and TLS cert. Only
the slim MCP server (`cmd/server`) is deployed — no embedder, no pipeline.

All commands use `ap-southeast-1`. Fill in `YOUR_*` placeholders first.

## Why co-location (not a separate box)

banhmi's embedder binds **`127.0.0.1:8089`** (loopback only) under ECS **host
networking**. A task on the *same* instance reaches it over loopback; a task
elsewhere cannot. So compliary joins the same instance and calls
`COMPLIARY_EMBED_ENDPOINT=http://127.0.0.1:8089`. Its image therefore packages
no ONNX model or runtime — query embedding is an in-network HTTP call. The
corpus was bulk-embedded (Kaggle) with the same Qwen3-Embedding-0.6B / last-token
/ L2 / 1024-d that banhmi's embedder serves, so query and document vectors share
one space.

## Shared vs new

| Reused from banhmi (no change) | New for compliary |
|---|---|
| EC2 host + ECS cluster `banhmi` | ECS service `compliary-mcp`, task family `compliary-mcp` |
| Embedder service (`127.0.0.1:8089`) + token `/banhmi/embed-token` | — (called, not redeployed) |
| RDS **instance** + security group | RDS **database** `compliary` + role `compliary` |
| Elastic IP + EC2 public-DNS origin | port **8084** ingress (widen the origin SG's CloudFront rule) |
| — (banhmi uses per-domain certs, no shared wildcard) | **own** ACM cert `compliary.danny.vn` (us-east-1) + CloudFront dist → `:8084` |
| `ecsTaskExecutionRole`, `ecsInstanceRole` | ECR repo `compliary-mcp`; SSM/secret entries below |

## Prerequisites

- banhmi already deployed and healthy (cluster, host, embedder, RDS, origin EIP, cert).
- AWS CLI v2 configured; ARM64 build host (or CodeBuild) for the image.
- The operator's corpus built locally (licensed docs in `data/`).

## 1. RDS — compliary database + role

Connect to the shared instance as the master user and provision an isolated DB:

```sql
CREATE ROLE compliary LOGIN PASSWORD 'CHOOSE_A_STRONG_PASSWORD';
CREATE DATABASE compliary OWNER compliary;
\c compliary
CREATE EXTENSION IF NOT EXISTS vector;
```

Load the corpus by pointing the local pipeline at RDS (env overrides added for
exactly this; **bulk embedding stays on Kaggle — never CPU/CI**):

```bash
export COMPLIARY_DATABASE_HOST=YOUR_RDS_ENDPOINT
export COMPLIARY_DATABASE_PORT=5432
export COMPLIARY_DATABASE_USER=compliary
export COMPLIARY_DATABASE_NAME=compliary
export COMPLIARY_DATABASE_SSLMODE=require
export COMPLIARY_DATABASE_PASSWORD=...        # the role password
export KAGGLE_API_TOKEN=...                    # Kaggle T4 for bulk embed

make migrate && make seed
go run ./cmd/pipeline                           # manifest → extract → normalize (default)
go run ./cmd/pipeline -stage mapedges           # cross-framework mapping edges
go run ./cmd/pipeline -stage index              # dense embeddings (Kaggle bulk)
go run ./cmd/pipeline -stage lexindex           # BM25 sparse vectors
```

(Alternative: build locally, then `pg_dump` → `pg_restore` into RDS. The
env-pointed run avoids dump/restore drift.)

## 2. Secrets (SSM SecureString + Secrets Manager)

```bash
# DB password
aws ssm put-parameter --name /compliary/db-password --type SecureString \
  --value 'THE_ROLE_PASSWORD'

# OAuth operator secret — bcrypt hash (the server also auto-hashes a plain value):
#   htpasswd -nbBC 10 "" 'your-console-password' | cut -d: -f2
aws ssm put-parameter --name /compliary/oauth-operator-secret --type SecureString \
  --value '$2y$10$...'

# CloudFront origin secret — SAME value goes in create-distribution.sh
# (ORIGIN_VERIFY_SECRET). Comma-separate two values during rotation. Use the FULL
# ARN (with random suffix) in the task definition.
aws secretsmanager create-secret --name compliary-origin-verify \
  --secret-string "$(openssl rand -hex 32)"
```

The embedder token is **not** re-created — compliary reads banhmi's existing
`/banhmi/embed-token` (same value the embedder validates).

## 3. IAM — extend the execution role

`ecsTaskExecutionRole` must read compliary's secrets **and** banhmi's shared
embed token:

```bash
aws iam put-role-policy \
  --role-name ecsTaskExecutionRole \
  --policy-name compliary-secrets \
  --policy-document '{
    "Version": "2012-10-17",
    "Statement": [
      {
        "Effect": "Allow",
        "Action": ["ssm:GetParameters"],
        "Resource": [
          "arn:aws:ssm:ap-southeast-1:YOUR_ACCOUNT_ID:parameter/compliary/*",
          "arn:aws:ssm:ap-southeast-1:YOUR_ACCOUNT_ID:parameter/banhmi/embed-token"
        ]
      },
      {
        "Effect": "Allow",
        "Action": ["secretsmanager:GetSecretValue"],
        "Resource": "arn:aws:secretsmanager:ap-southeast-1:YOUR_ACCOUNT_ID:secret:compliary-origin-verify-*"
      }
    ]
  }'
```

## 4. Security group — open port 8084 from CloudFront

```bash
CF_PL=$(aws ec2 describe-managed-prefix-lists \
  --filters "Name=prefix-list-name,Values=com.amazonaws.global.cloudfront.origin-facing" \
  --query 'PrefixLists[0].PrefixListId' --output text)

aws ec2 authorize-security-group-ingress \
  --group-id YOUR_BANHMI_SG_ID \
  --ip-permissions \
    "IpProtocol=tcp,FromPort=8084,ToPort=8084,PrefixListIds=[{PrefixListId=$CF_PL,Description=CloudFront}]"
```

Origin verification (`COMPLIARY_ORIGIN_VERIFY_SECRET`) is the app-layer backstop:
even from a CloudFront IP, a request lacking our `X-Origin-Verify` header is 403'd.

## 5. ECR — build and push (ARM64)

> Steps 5-6 are automated by `deploy/aws/release.sh <x.y.z>` once the repo,
> service, and secrets exist; the commands below are the first-time/manual path.

```bash
aws ecr create-repository --repository-name compliary-mcp --region ap-southeast-1

# Any host, from repo root (builder cross-compiles; runtime stage is arm64):
podman build --platform linux/arm64 -t compliary-server \
  --build-arg VERSION=<x.y.z>-$(date +%Y%m%d) \
  -f deploy/containerfiles/Containerfile.ecs.server .
# tag + push to YOUR_ACCOUNT_ID.dkr.ecr.ap-southeast-1.amazonaws.com/compliary-mcp:<tag>
```

## 6. ECS task + service

Pin the image by tag or `@sha256` digest in `ecs-task-definition.json` (never
`:latest`), then:

```bash
aws logs create-log-group --log-group-name /ecs/compliary-mcp --region ap-southeast-1

aws ecs register-task-definition --cli-input-json file://deploy/aws/ecs-task-definition.json

aws ecs create-service \
  --cluster banhmi \
  --service-name compliary-mcp \
  --task-definition compliary-mcp \
  --desired-count 1 \
  --launch-type EC2 \
  --deployment-configuration "minimumHealthyPercent=0,maximumPercent=100"
```

The `t4g.medium` (~3,829 MB registered) already holds banhmi-mcp (300) +
embedder (2400); compliary-mcp reserves 300 more, leaving headroom.

## 7. ACM cert + CloudFront + DNS

banhmi uses **per-domain** certs (no shared wildcard), so request compliary's own
in **us-east-1** (CloudFront requires it there):

```bash
aws acm request-certificate --region us-east-1 \
  --domain-name compliary.danny.vn --validation-method DNS \
  --query 'CertificateArn' --output text
# Add the returned CNAME validation record to danny.vn DNS; wait for ISSUED.
```

Then create the distribution and point DNS at it:

```bash
# edit DOMAIN / ACM_CERT_ARN (the cert above) / ORIGIN_VERIFY_SECRET first
bash deploy/aws/create-distribution.sh
```

Finally create a CNAME `compliary.danny.vn` → the distribution domain.

## 8. Smoke tests

The origin SG admits only CloudFront, so test the origin directly only from the
host itself (e.g. via SSM run-command), and everything else through CloudFront:

```bash
# On the host: origin health (origin-verify-exempt) + origin-verify enforced on /mcp
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8084/healthz          # 200
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8084/mcp              # 403 (no X-Origin-Verify)

# Through CloudFront: health is public, unauthenticated /mcp is 401 (OAuth required)
curl -s -o /dev/null -w "%{http_code}\n" https://compliary.danny.vn/healthz     # 200
curl -s -o /dev/null -w "%{http_code}\n" https://compliary.danny.vn/mcp         # 401

# OAuth discovery is public
curl -s https://compliary.danny.vn/.well-known/oauth-protected-resource | head
```

Then add `https://compliary.danny.vn/mcp` as a custom connector in claude.ai /
chatgpt.com and complete the OAuth login (see `docs/OPERATIONS.md`).

## Cost delta

Negligible over banhmi: one CloudFront distribution (~$1-2/mo), two SSM params +
one secret (~$0.40/mo), a little ECR storage. No new EC2, EIP, or RDS instance.

## Rollback

`aws ecs update-service --cluster banhmi --service compliary-mcp --desired-count 0`
stops compliary without touching banhmi. Then optionally delete the service,
CloudFront distribution (disable → delete), SG rule, secrets, and ECR repo.
banhmi is unaffected throughout.
