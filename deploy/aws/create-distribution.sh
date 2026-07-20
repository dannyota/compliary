#!/usr/bin/env bash
set -euo pipefail

# Create the CloudFront distribution for compliary.danny.vn → the shared banhmi
# EC2 host on :8084. One distribution; compliary is a single corpus. The origin
# is the EC2 instance's PUBLIC DNS name (same host banhmi uses — there is no
# origin.danny.vn DNS record). The ACM cert is compliary's OWN (banhmi uses
# per-domain certs — request compliary.danny.vn in us-east-1).

# ── Variables (edit these) ───────────────────────────────────────────────────
DOMAIN="compliary.danny.vn"
ORIGIN_HOST="ec2-XX-XX-XX-XX.ap-southeast-1.compute.amazonaws.com"  # the EC2 host's public DNS (aws ec2 describe-instances ... PublicDnsName)
ACM_CERT_ARN="arn:aws:acm:us-east-1:YOUR_ACCOUNT_ID:certificate/YOUR_CERT_ID"  # compliary.danny.vn cert (us-east-1)
ORIGIN_VERIFY_SECRET="YOUR_SECRET_HERE"  # same value stored in the compliary-origin-verify secret
TEMPLATE="$(dirname "$0")/cloudfront-config.json"
# ─────────────────────────────────────────────────────────────────────────────

CALLER_REF="${DOMAIN}-$(date +%Y%m%d%H%M%S)"

echo "Creating distribution: ${DOMAIN} -> ${ORIGIN_HOST}:8084"

# Order matters: substitute CALLER_REFERENCE (no DOMAIN substring) before the
# generic DOMAIN replacement, so the caller reference keeps its timestamp.
CONFIG=$(sed \
  -e "s|CALLER_REFERENCE|${CALLER_REF}|g" \
  -e "s|ORIGIN_HOST|${ORIGIN_HOST}|g" \
  -e "s|DOMAIN|${DOMAIN}|g" \
  -e "s|ORIGIN_VERIFY_SECRET|${ORIGIN_VERIFY_SECRET}|g" \
  -e "s|ACM_CERT_ARN|${ACM_CERT_ARN}|g" \
  "$TEMPLATE")

# Remove the _comment field (not valid in CloudFront API input).
CONFIG=$(echo "$CONFIG" | python3 -c "
import json, sys
d = json.load(sys.stdin)
d.pop('_comment', None)
json.dump(d, sys.stdout, indent=2)
")

aws cloudfront create-distribution \
  --distribution-config "$CONFIG" \
  --query 'Distribution.{Id:Id,Domain:DomainName,Status:Status}' \
  --output table

echo ""
echo "Done. Next steps:"
echo "  1. Wait for the distribution to reach 'Deployed' status."
echo "  2. Create a CNAME: ${DOMAIN} -> the CloudFront distribution domain."
echo "  3. Verify: curl -I https://${DOMAIN}/healthz"
