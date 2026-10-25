#!/usr/bin/env bash
set -euo pipefail
sandbox=$(mktemp -d)
trap 'rm -rf "$sandbox"' EXIT
mkdir "$sandbox/bin"
export VENWEB_TEST_SANDBOX="$sandbox"
export GITHUB_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export RENDER_API_KEY=fixture-only
export RENDER_SERVICE_ID=srv-fixture
export RENDER_HEALTH_URL=https://fixture.invalid/health
export PATH="$sandbox/bin:$PATH"
cat > "$sandbox/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$VENWEB_TEST_SANDBOX/requests"
case "${!#}" in
  */deploys) printf '{"id":"dep-fixture"}\n' ;;
  */deploys/dep-fixture)
    case "$VENWEB_TEST_SCENARIO" in
      failed) printf '{"status":"build_failed"}\n' ;;
      mismatch) printf '{"status":"live","commit":{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}\n' ;;
      healthy|unhealthy) printf '{"status":"live","commit":{"id":"%s"}}\n' "$GITHUB_SHA" ;;
      pending) printf '{"status":"update_in_progress"}\n' ;;
    esac
    ;;
  */health) [[ $VENWEB_TEST_SCENARIO != unhealthy ]] ;;
  *) exit 1 ;;
esac
MOCK
printf '#!/bin/sh\nexit 0\n' > "$sandbox/bin/sleep"
chmod +x "$sandbox/bin/"*
for VENWEB_TEST_SCENARIO in healthy unhealthy failed mismatch pending; do
  export VENWEB_TEST_SCENARIO
  rm -f "$sandbox/requests"
  if bash "$(dirname "$0")/deploy.sh"; then
    [[ $VENWEB_TEST_SCENARIO == healthy ]]
  else
    [[ $VENWEB_TEST_SCENARIO != healthy ]]
  fi
  [[ $(grep -c -- '--data ' "$sandbox/requests") == 1 ]]
  grep -q -- "commitId.*$GITHUB_SHA" "$sandbox/requests"
  if [[ $VENWEB_TEST_SCENARIO != healthy && $VENWEB_TEST_SCENARIO != unhealthy ]]; then
    ! grep -q /health "$sandbox/requests"
  fi
done
echo 'Render live-commit verification, failure, mismatch and bounded polling passed'
