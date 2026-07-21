#!/usr/bin/env bash
# release.sh — build, push, and roll the maintainer instance in one command.
#
#   deploy/aws/release.sh 0.1.12
#
# Encodes the manual runbook steps (setup-checklist.md §5-6): cross-compile the
# ARM64 image, push version + latest tags to ECR, register a task-definition
# revision pinned to the pushed digest, roll the ECS service, and verify the
# live instance reports the new version. Idempotent: re-running the same
# version rebuilds and re-rolls.
#
# Requires: podman, aws CLI v2 (credentials in the environment or .env),
# python3. No secrets are read besides AWS credentials; the account ID is
# resolved at runtime and never committed.
set -euo pipefail

VERSION="${1:?usage: release.sh <x.y.z>}"
case "$VERSION" in
  *[!0-9.]*) echo "version must be bare semver (got: $VERSION)" >&2; exit 1 ;;
esac

REGION="${COMPLIARY_AWS_REGION:-ap-southeast-1}"
CLUSTER="${COMPLIARY_ECS_CLUSTER:-banhmi}"
SERVICE="${COMPLIARY_ECS_SERVICE:-compliary-mcp}"
ECR_REPO="${COMPLIARY_ECR_REPO:-compliary-mcp}"
PUBLIC_URL="${COMPLIARY_PUBLIC_URL:-https://compliary.danny.vn}"
STAMP="${VERSION}-$(date +%Y%m%d)"

say() { printf '\n== %s\n' "$*"; }

say "pre-flight: cmd/server must stay CGO-free (no go-fitz/mupdf)"
if go list -deps ./cmd/server | grep -qi 'fitz\|mupdf'; then
  echo "FAIL: cmd/server pulls a CGO PDF dependency" >&2; exit 1
fi

ACCOUNT="$(aws sts get-caller-identity --query Account --output text)"
REGISTRY="${ACCOUNT}.dkr.ecr.${REGION}.amazonaws.com"
IMAGE="${REGISTRY}/${ECR_REPO}"

say "build ${STAMP} (arm64; builder cross-compiles on this host)"
podman build --platform linux/arm64 -t "compliary-server:${STAMP}" \
  --build-arg "VERSION=${STAMP}" \
  -f deploy/containerfiles/Containerfile.ecs.server .
ARCH="$(podman inspect "compliary-server:${STAMP}" --format '{{.Architecture}}')"
[ "$ARCH" = "arm64" ] || { echo "FAIL: image arch is ${ARCH}, want arm64" >&2; exit 1; }

say "push to ECR (${STAMP} + latest)"
aws ecr get-login-password --region "$REGION" |
  podman login --username AWS --password-stdin "$REGISTRY"
podman tag "compliary-server:${STAMP}" "${IMAGE}:${STAMP}"
podman tag "compliary-server:${STAMP}" "${IMAGE}:latest"
podman push "${IMAGE}:${STAMP}"
podman push "${IMAGE}:latest"

DIGEST="$(aws ecr describe-images --repository-name "$ECR_REPO" --region "$REGION" \
  --image-ids "imageTag=${STAMP}" --query 'imageDetails[0].imageDigest' --output text)"
say "pushed digest ${DIGEST}"

say "register task-definition revision pinned to the digest"
TD_FILE="$(mktemp)"
trap 'rm -f "$TD_FILE"' EXIT
aws ecs describe-task-definition --task-definition "$SERVICE" --region "$REGION" \
  --query 'taskDefinition' >"$TD_FILE"
python3 - "$TD_FILE" "${IMAGE}@${DIGEST}" <<'PY'
import json, sys
path, image = sys.argv[1], sys.argv[2]
td = json.load(open(path))
for k in ("taskDefinitionArn", "revision", "status", "requiresAttributes",
          "compatibilities", "registeredAt", "registeredBy"):
    td.pop(k, None)
td["containerDefinitions"][0]["image"] = image
json.dump(td, open(path, "w"))
PY
REV="$(aws ecs register-task-definition --cli-input-json "file://${TD_FILE}" \
  --region "$REGION" --query 'taskDefinition.revision' --output text)"

say "roll ${SERVICE} to revision ${REV}"
aws ecs update-service --cluster "$CLUSTER" --service "$SERVICE" \
  --task-definition "${SERVICE}:${REV}" --region "$REGION" \
  --query 'service.taskDefinition' --output text
aws ecs wait services-stable --cluster "$CLUSTER" --services "$SERVICE" --region "$REGION"

say "verify live instance"
HEALTH="$(curl -fsS "${PUBLIC_URL}/healthz")"
[ "$HEALTH" = "ok" ] || { echo "FAIL: healthz returned: ${HEALTH}" >&2; exit 1; }
LIVE="$(curl -fsS "$PUBLIC_URL/" | grep -o 'version <b>[^<]*' | cut -d'>' -f2)"
[ "$LIVE" = "$STAMP" ] || { echo "FAIL: live version is '${LIVE}', want '${STAMP}'" >&2; exit 1; }
CODE="$(curl -s -o /dev/null -w '%{http_code}' -X POST "${PUBLIC_URL}/mcp" \
  -H 'Content-Type: application/json' -d '{}')"
[ "$CODE" = "401" ] || { echo "FAIL: unauthenticated /mcp returned ${CODE}, want 401" >&2; exit 1; }

say "released ${STAMP} — live version verified, /mcp auth enforced"
