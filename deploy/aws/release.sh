#!/usr/bin/env bash
# release.sh — migrate, build, push, roll, and verify the maintainer instance.
#
#   deploy/aws/release.sh --preflight 0.2.2  # read-only AWS checks + local build
#   deploy/aws/release.sh 0.2.2              # release 0.2.2-YYYYMMDD
#
# The release is ordered so a failed migration cannot take down the live
# service: build and push the digest-pinned image, run its /migrate binary as a
# one-off no-port ECS task, require exit code 0, then roll the server service.
# The previous task definition is captured before any change and restored if
# rollout or smoke verification fails. The auth migration is expand-only, so
# the previous server remains compatible after a rollback.
#
# Requires: podman, AWS CLI v2, python3, curl, Go, and Git. AWS credentials must
# be supplied explicitly through the environment or an AWS profile. This script
# deliberately does not source .env files.
set -euo pipefail

PREFLIGHT=false
if [ "${1:-}" = "--preflight" ]; then
  PREFLIGHT=true
  shift
fi

VERSION="${1:-}"
if [ "$#" -ne 1 ] || ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  LATEST="$(git describe --tags --abbrev=0 2>/dev/null || echo none)"
  echo "usage: release.sh [--preflight] <x.y.z>   (latest tag: ${LATEST})" >&2
  exit 1
fi

REGION="${COMPLIARY_AWS_REGION:-${AWS_DEFAULT_REGION:-ap-southeast-1}}"
CLUSTER="${COMPLIARY_ECS_CLUSTER:-banhmi}"
SERVICE="${COMPLIARY_ECS_SERVICE:-compliary-mcp}"
MIGRATE_FAMILY="${SERVICE}-migrate"
ECR_REPO="${COMPLIARY_ECR_REPO:-compliary-mcp}"
PUBLIC_URL="${COMPLIARY_PUBLIC_URL:-https://compliary.danny.vn}"
PUBLIC_URL="${PUBLIC_URL%/}"
STAMP="${VERSION}-$(date +%Y%m%d)"
RELEASE_TAG="v${VERSION}"

say() { printf '\n== %s\n' "$*"; }
fail() {
  echo "FAIL: $*" >&2
  exit 1
}

for command in aws curl git go podman python3; do
  command -v "$command" >/dev/null || fail "required command not found: ${command}"
done

# An untracked local CLAUDE.md is excluded by the container allowlist and cannot
# affect the image. Other source changes must be committed so the image is
# reproducible from the signed release tag.
if [ -n "$(git status --porcelain -- ':!data' ':!CLAUDE.md' 2>/dev/null)" ]; then
  fail "working tree has uncommitted changes that enter the release"
fi

say "verify signed release tag and published commit"
git fetch --quiet origin master --tags
HEAD_COMMIT="$(git rev-parse HEAD)"
SHORT_COMMIT="${HEAD_COMMIT:0:12}"
CANDIDATE_TAG="${STAMP}-g${SHORT_COMMIT}"
ORIGIN_COMMIT="$(git rev-parse origin/master)"
TAG_COMMIT="$(git rev-parse "${RELEASE_TAG}^{commit}" 2>/dev/null || true)"
[ "$HEAD_COMMIT" = "$ORIGIN_COMMIT" ] ||
  fail "HEAD is not origin/master; push the release commit and wait for CI"
[ "$HEAD_COMMIT" = "$TAG_COMMIT" ] ||
  fail "${RELEASE_TAG} does not exist or does not point to HEAD"
git verify-tag "$RELEASE_TAG" >/dev/null 2>&1 ||
  fail "${RELEASE_TAG} is not a valid signed tag"
REMOTE_TAG_COMMIT="$(
  git ls-remote origin "refs/tags/${RELEASE_TAG}^{}" |
    awk 'NR == 1 { print $1 }'
)"
[ "$REMOTE_TAG_COMMIT" = "$HEAD_COMMIT" ] ||
  fail "${RELEASE_TAG} is not pushed to origin"

say "local quality gates"
[ -z "$(gofmt -l .)" ] || fail "gofmt found unformatted Go files"
git diff --check
python3 -m unittest discover -s deploy/aws -p 'test_*.py'
go run ./cmd/migrate -check
go build ./...
go vet ./...
go test ./...
go test -race ./pkg/oauth ./cmd/server

say "pre-flight: cmd/server must stay CGO-free (no go-fitz/mupdf)"
if go list -deps ./cmd/server | grep -qi 'fitz\|mupdf'; then
  fail "cmd/server pulls a CGO PDF dependency"
fi

CALLER_ARN="$(aws sts get-caller-identity --query Arn --output text)"
ACCOUNT="$(aws sts get-caller-identity --query Account --output text)"
REGISTRY="${ACCOUNT}.dkr.ecr.${REGION}.amazonaws.com"
IMAGE="${REGISTRY}/${ECR_REPO}"
CLUSTER_ARN="arn:aws:ecs:${REGION}:${ACCOUNT}:cluster/${CLUSTER}"
MIGRATE_TD_ARN="arn:aws:ecs:${REGION}:${ACCOUNT}:task-definition/${MIGRATE_FAMILY}:*"

INITIAL_SERVICE_JSON="$(
  aws ecs describe-services \
    --cluster "$CLUSTER" \
    --services "$SERVICE" \
    --region "$REGION" \
    --output json
)"
SERVICE_STATUS="$(
  python3 -c 'import json,sys; print(json.load(sys.stdin)["services"][0]["status"])' \
    <<<"$INITIAL_SERVICE_JSON"
)"
[ "$SERVICE_STATUS" = "ACTIVE" ] || fail "ECS service ${SERVICE} is not ACTIVE"

say "require a stable live service before capturing the rollback revision"
aws ecs wait services-stable \
  --cluster "$CLUSTER" \
  --services "$SERVICE" \
  --region "$REGION"
SERVICE_JSON="$(
  aws ecs describe-services \
    --cluster "$CLUSTER" \
    --services "$SERVICE" \
    --region "$REGION" \
    --output json
)"
SERVICE_READY="$(
  python3 -c '
import json, sys
s = json.load(sys.stdin)["services"][0]
deployments = s.get("deployments", [])
ready = (
    s.get("status") == "ACTIVE"
    and s.get("desiredCount", 0) > 0
    and s.get("runningCount") == s.get("desiredCount")
    and s.get("pendingCount") == 0
    and len(deployments) == 1
    and deployments[0].get("rolloutState") in (None, "COMPLETED")
)
print("true" if ready else "false")
' <<<"$SERVICE_JSON"
)"
[ "$SERVICE_READY" = "true" ] ||
  fail "ECS service ${SERVICE} is not in a single completed deployment"
CURRENT_TD_ARN="$(
  python3 -c 'import json,sys; print(json.load(sys.stdin)["services"][0]["taskDefinition"])' \
    <<<"$SERVICE_JSON"
)"
DEPLOY_MIN="$(
  python3 -c 'import json,sys; print(json.load(sys.stdin)["services"][0]["deploymentConfiguration"]["minimumHealthyPercent"])' \
    <<<"$SERVICE_JSON"
)"
DEPLOY_MAX="$(
  python3 -c 'import json,sys; print(json.load(sys.stdin)["services"][0]["deploymentConfiguration"]["maximumPercent"])' \
    <<<"$SERVICE_JSON"
)"

aws ecr describe-repositories \
  --repository-names "$ECR_REPO" \
  --region "$REGION" \
  >/dev/null

say "check one-off migration permission"
if [[ "$CALLER_ARN" == arn:aws:iam::*:user/* ]]; then
  CALLER_USERNAME="${CALLER_ARN##*/}"
  RUN_DECISION="$(
    aws iam simulate-principal-policy \
      --policy-source-arn "$CALLER_ARN" \
      --action-names ecs:RunTask \
      --resource-arns "$MIGRATE_TD_ARN" \
      --context-entries \
        "ContextKeyName=ecs:cluster,ContextKeyValues=${CLUSTER_ARN},ContextKeyType=string" \
        "ContextKeyName=aws:username,ContextKeyValues=${CALLER_USERNAME},ContextKeyType=string" \
      --query 'EvaluationResults[0].EvalDecision' \
      --output text 2>/dev/null || echo unknown
  )"
  case "$RUN_DECISION" in
    allowed) ;;
    explicitDeny | implicitDeny)
      fail "caller cannot ecs:RunTask for ${MIGRATE_FAMILY} (${RUN_DECISION}); apply the scoped deploy policy in setup-checklist.md"
      ;;
    *)
      say "IAM simulation unavailable; ecs:RunTask will be checked at release"
      ;;
  esac
else
  say "IAM simulation skipped for non-user caller ${CALLER_ARN}; ecs:RunTask will be checked at release"
fi

say "build ${STAMP} (arm64; builder cross-compiles on this host)"
podman build --platform linux/arm64 -t "compliary-server:${STAMP}" \
  --build-arg "VERSION=${STAMP}" \
  --build-arg "REVISION=${HEAD_COMMIT}" \
  -f deploy/containerfiles/Containerfile.ecs.server .
ARCH="$(podman inspect "compliary-server:${STAMP}" --format '{{.Architecture}}')"
[ "$ARCH" = "arm64" ] || fail "image arch is ${ARCH}, want arm64"
IMAGE_REVISION="$(
  podman inspect "compliary-server:${STAMP}" \
    --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}'
)"
[ "$IMAGE_REVISION" = "$HEAD_COMMIT" ] ||
  fail "image revision label is ${IMAGE_REVISION}, want ${HEAD_COMMIT}"

if [ "$PREFLIGHT" = true ]; then
  say "preflight passed — no AWS resources changed"
  exit 0
fi

TMP_DIR="$(mktemp -d)"
cleanup() { rm -rf -- "$TMP_DIR"; }
trap cleanup EXIT

PUSH_DIGEST_FILE="${TMP_DIR}/pushed-digest"
BASE_TD_FILE="${TMP_DIR}/base-task-definition.json"
SERVER_TD_FILE="${TMP_DIR}/server-task-definition.json"
MIGRATE_TD_FILE="${TMP_DIR}/migrate-task-definition.json"
RUN_FILE="${TMP_DIR}/migration-run.json"

# The ECR repository permits mutable `latest`, so the source commit is part of
# the candidate tag and the exact digest returned by this push is compared with
# ECR before it is used. Retries of the same signed commit safely replace only
# that commit-scoped candidate; ECS always receives the verified digest.
say "push commit-scoped candidate ${CANDIDATE_TAG}"
aws ecr get-login-password --region "$REGION" |
  podman login --username AWS --password-stdin "$REGISTRY"
podman tag "compliary-server:${STAMP}" "${IMAGE}:${CANDIDATE_TAG}"
podman push --digestfile "$PUSH_DIGEST_FILE" "${IMAGE}:${CANDIDATE_TAG}"
PUSHED_DIGEST="$(tr -d '[:space:]' <"$PUSH_DIGEST_FILE")"

DIGEST="$(
  aws ecr describe-images \
    --repository-name "$ECR_REPO" \
    --region "$REGION" \
    --image-ids "imageTag=${CANDIDATE_TAG}" \
    --query 'imageDetails[0].imageDigest' \
    --output text
)"
[ -n "$DIGEST" ] && [ "$DIGEST" != "None" ] ||
  fail "could not resolve digest for ${IMAGE}:${CANDIDATE_TAG}"
[ "$DIGEST" = "$PUSHED_DIGEST" ] ||
  fail "ECR candidate digest ${DIGEST} does not match pushed digest ${PUSHED_DIGEST}"
PINNED_IMAGE="${IMAGE}@${DIGEST}"
say "pushed digest ${DIGEST}"

# Surface scan-on-push findings (best-effort: the scan may still be running,
# and the deploy credential may lack ecr:DescribeImageScanFindings).
SCAN="$(
  aws ecr describe-image-scan-findings \
    --repository-name "$ECR_REPO" \
    --region "$REGION" \
    --image-ids "imageTag=${CANDIDATE_TAG}" \
    --query 'imageScanFindings.findingSeverityCounts' \
    --output json 2>/dev/null || true
)"
if [ -n "$SCAN" ] && [ "$SCAN" != "null" ]; then
  say "image scan findings: ${SCAN}"
  case "$SCAN" in
    *CRITICAL*) echo "CRITICAL vulnerabilities found — investigate before relying on this image" >&2 ;;
  esac
else
  say "image scan findings not available yet (check the ECR console)"
fi

# Clone the exact revision used by the service, not the latest family revision:
# a failed or migration-only registration must never become an accidental base.
aws ecs describe-task-definition \
  --task-definition "$CURRENT_TD_ARN" \
  --region "$REGION" \
  --query taskDefinition \
  >"$BASE_TD_FILE"

python3 deploy/aws/render_task_definitions.py \
  --base "$BASE_TD_FILE" \
  --server-out "$SERVER_TD_FILE" \
  --migrate-out "$MIGRATE_TD_FILE" \
  --image "$PINNED_IMAGE" \
  --migrate-family "$MIGRATE_FAMILY"

say "apply database migrations in one-off ECS task"
MIGRATE_REV="$(
  aws ecs register-task-definition \
    --cli-input-json "file://${MIGRATE_TD_FILE}" \
    --region "$REGION" \
    --query 'taskDefinition.revision' \
    --output text
)"
aws ecs run-task \
  --cluster "$CLUSTER" \
  --task-definition "${MIGRATE_FAMILY}:${MIGRATE_REV}" \
  --launch-type EC2 \
  --started-by "release-${STAMP}" \
  --region "$REGION" \
  --output json \
  >"$RUN_FILE"
TASK_ARN="$(
  python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("tasks", [{}])[0].get("taskArn", ""))' \
    <"$RUN_FILE"
)"
if [ -z "$TASK_ARN" ]; then
  FAILURE="$(
    python3 -c 'import json,sys; data=json.load(sys.stdin); print("; ".join(item.get("reason", "unknown") for item in data.get("failures", [])))' \
      <"$RUN_FILE"
  )"
  fail "migration task was not started: ${FAILURE:-unknown ECS failure}"
fi

stop_migration_task() {
  aws ecs stop-task \
    --cluster "$CLUSTER" \
    --task "$TASK_ARN" \
    --reason "release aborted before migration completion" \
    --region "$REGION" \
    >/dev/null 2>&1 || true
  aws ecs wait tasks-stopped \
    --cluster "$CLUSTER" \
    --tasks "$TASK_ARN" \
    --region "$REGION" \
    >/dev/null 2>&1 || true
}

interrupt_migration() {
  trap - INT TERM
  echo "release interrupted; stopping migration task ${TASK_ARN}" >&2
  stop_migration_task
  exit 130
}
trap interrupt_migration INT TERM

if ! aws ecs wait tasks-stopped \
  --cluster "$CLUSTER" \
  --tasks "$TASK_ARN" \
  --region "$REGION"; then
  TASK_STATE="$(
    aws ecs describe-tasks \
      --cluster "$CLUSTER" \
      --tasks "$TASK_ARN" \
      --region "$REGION" \
      --query 'tasks[0].{lastStatus:lastStatus,desiredStatus:desiredStatus,stoppedReason:stoppedReason}' \
      --output json 2>/dev/null || echo '{"status":"unavailable"}'
  )"
  stop_migration_task
  trap - INT TERM
  fail "migration waiter failed for ${TASK_ARN}: ${TASK_STATE}"
fi
trap - INT TERM

TASK_RESULT="$(
  aws ecs describe-tasks \
    --cluster "$CLUSTER" \
    --tasks "$TASK_ARN" \
    --region "$REGION" \
    --query 'tasks[0].{exitCode:containers[0].exitCode,containerReason:containers[0].reason,stoppedReason:stoppedReason}' \
    --output json
)"
MIGRATE_EXIT="$(
  python3 -c 'import json,sys; value=json.load(sys.stdin).get("exitCode"); print("" if value is None else value)' \
    <<<"$TASK_RESULT"
)"
if [ "$MIGRATE_EXIT" != "0" ]; then
  fail "migration task failed: ${TASK_RESULT}"
fi
say "database migrations completed"

say "confirm the live service did not change during migration"
if ! aws ecs wait services-stable \
  --cluster "$CLUSTER" \
  --services "$SERVICE" \
  --region "$REGION"; then
  fail "live service changed or became unstable during migration; not rolling the candidate"
fi
POST_MIGRATION_TD="$(
  aws ecs describe-services \
    --cluster "$CLUSTER" \
    --services "$SERVICE" \
    --region "$REGION" \
    --query 'services[0].taskDefinition' \
    --output text
)"
[ "$POST_MIGRATION_TD" = "$CURRENT_TD_ARN" ] ||
  fail "live task definition changed from ${CURRENT_TD_ARN} to ${POST_MIGRATION_TD}; not rolling the candidate"

say "register server task definition pinned to the migrated image"
SERVER_REV="$(
  aws ecs register-task-definition \
    --cli-input-json "file://${SERVER_TD_FILE}" \
    --region "$REGION" \
    --query 'taskDefinition.revision' \
    --output text
)"

rollback() {
  say "restore previous task definition ${CURRENT_TD_ARN}"
  aws ecs update-service \
    --cluster "$CLUSTER" \
    --service "$SERVICE" \
    --task-definition "$CURRENT_TD_ARN" \
    --region "$REGION" \
    >/dev/null
  aws ecs wait services-stable \
    --cluster "$CLUSTER" \
    --services "$SERVICE" \
    --region "$REGION"
}

interrupt_rollout() {
  trap - INT TERM
  echo "release interrupted after rollout began; restoring ${CURRENT_TD_ARN}" >&2
  rollback || true
  exit 130
}
trap interrupt_rollout INT TERM

PRE_ROLLOUT_TD="$(
  aws ecs describe-services \
    --cluster "$CLUSTER" \
    --services "$SERVICE" \
    --region "$REGION" \
    --query 'services[0].taskDefinition' \
    --output text
)"
[ "$PRE_ROLLOUT_TD" = "$CURRENT_TD_ARN" ] ||
  fail "live task definition changed to ${PRE_ROLLOUT_TD} before update; not rolling the candidate"

say "roll ${SERVICE} to revision ${SERVER_REV} with circuit-breaker rollback"
if ! aws ecs update-service \
  --cluster "$CLUSTER" \
  --service "$SERVICE" \
  --task-definition "${SERVICE}:${SERVER_REV}" \
  --deployment-configuration \
    "minimumHealthyPercent=${DEPLOY_MIN},maximumPercent=${DEPLOY_MAX},deploymentCircuitBreaker={enable=true,rollback=true}" \
  --region "$REGION" \
  --query 'service.taskDefinition' \
  --output text; then
  echo "update-service failed or its result was lost; restoring the captured task definition" >&2
  rollback
  exit 1
fi
if ! aws ecs wait services-stable \
  --cluster "$CLUSTER" \
  --services "$SERVICE" \
  --region "$REGION"; then
  echo "rollout did not stabilize; restoring the captured task definition" >&2
  rollback
  exit 1
fi

say "verify live instance"
if ! HEALTH="$(
  curl -fsS \
    --connect-timeout 10 \
    --max-time 30 \
    --retry 2 \
    --retry-delay 1 \
    --retry-all-errors \
    "${PUBLIC_URL}/healthz"
)" || [ "$HEALTH" != "ok" ]; then
  echo "healthz verification failed; restoring the captured task definition" >&2
  rollback
  exit 1
fi
if ! LIVE="$(
  curl -fsS \
    --connect-timeout 10 \
    --max-time 30 \
    --retry 2 \
    --retry-delay 1 \
    --retry-all-errors \
    "$PUBLIC_URL/" |
    grep -o 'version <b>[^<]*' |
    cut -d'>' -f2
)"; then
  echo "live version could not be read; restoring the captured task definition" >&2
  rollback
  exit 1
fi
if [ "$LIVE" != "$STAMP" ]; then
  echo "live version is '${LIVE}', want '${STAMP}'; restoring the captured task definition" >&2
  rollback
  exit 1
fi
if ! OAUTH_METADATA="$(
  curl -fsS \
    --connect-timeout 10 \
    --max-time 30 \
    --retry 2 \
    --retry-delay 1 \
    --retry-all-errors \
    "${PUBLIC_URL}/.well-known/oauth-authorization-server"
)"; then
  echo "OAuth discovery could not be reached; restoring the captured task definition" >&2
  rollback
  exit 1
fi
if ! python3 -c '
import json, sys
metadata = json.load(sys.stdin)
issuer = sys.argv[1]
valid = (
    metadata.get("issuer") == issuer
    and metadata.get("token_endpoint") == issuer + "/oauth/token"
    and "authorization_code" in metadata.get("grant_types_supported", [])
    and "refresh_token" in metadata.get("grant_types_supported", [])
)
raise SystemExit(0 if valid else 1)
' "$PUBLIC_URL" <<<"$OAUTH_METADATA"; then
  echo "OAuth discovery metadata is invalid; restoring the captured task definition" >&2
  rollback
  exit 1
fi
if ! CODE="$(
  curl -sS \
    --connect-timeout 10 \
    --max-time 30 \
    -o /dev/null \
    -w '%{http_code}' \
    -X POST "${PUBLIC_URL}/mcp" \
    -H 'Content-Type: application/json' \
    -d '{}'
)"; then
  echo "unauthenticated /mcp could not be reached; restoring the captured task definition" >&2
  rollback
  exit 1
fi
if [ "$CODE" != "401" ]; then
  echo "unauthenticated /mcp returned ${CODE}, want 401; restoring the captured task definition" >&2
  rollback
  exit 1
fi
trap - INT TERM

# latest means live: move it only after the digest-pinned rollout and smoke
# checks have succeeded.
say "promote verified image to latest"
LATEST_DIGEST_FILE="${TMP_DIR}/latest-digest"
podman tag "compliary-server:${STAMP}" "${IMAGE}:latest"
podman push --digestfile "$LATEST_DIGEST_FILE" "${IMAGE}:latest"
LATEST_DIGEST="$(tr -d '[:space:]' <"$LATEST_DIGEST_FILE")"
[ "$LATEST_DIGEST" = "$DIGEST" ] ||
  fail "latest digest ${LATEST_DIGEST} does not match deployed digest ${DIGEST}"

say "released ${STAMP} (${CANDIDATE_TAG}) — migration completed, live version verified, /mcp auth enforced"
