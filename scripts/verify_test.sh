#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/verify.sh"

test -x "$script"
grep -Fq 'go test ./...' "$script"
grep -Fq 'go vet ./...' "$script"
grep -Fq 'govulncheck' "$script"
test "$(grep -Fc 'go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...' "$script")" -eq 3
grep -Fq 'npm ci' "$script"
grep -Fq 'npm run lint:ci' "$script" || grep -Fq 'npm run check' "$script"
grep -Fq 'git diff --exit-code' "$script"
grep -Fq 'SYNC_WEB_DIST' "$script"
grep -Fq 'sync-web-dist.sh' "$script"

echo "verify.sh contract OK"
