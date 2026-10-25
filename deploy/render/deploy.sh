#!/usr/bin/env bash
set -euo pipefail

: "${RENDER_API_KEY:?Set RENDER_API_KEY in repository Actions secrets}"
: "${RENDER_SERVICE_ID:?Missing Render service ID}"
: "${RENDER_HEALTH_URL:?Missing Render health URL}"
[[ ${GITHUB_SHA:-} =~ ^[a-f0-9]{40}$ ]] || { echo 'Invalid commit SHA' >&2; exit 1; }
[[ $RENDER_SERVICE_ID =~ ^srv-[a-z0-9]+$ ]] || exit 1
api="https://api.render.com/v1/services/$RENDER_SERVICE_ID/deploys"

# Do not retry the POST: an ambiguous network failure could create two deployments.
deployment=$(curl --fail --silent --show-error --max-time 30 \
  -H "Authorization: Bearer $RENDER_API_KEY" -H 'Content-Type: application/json' \
  --data "{\"commitId\":\"$GITHUB_SHA\"}" "$api")
id=$(jq -er '.id | select(test("^dep-[a-z0-9]+$"))' <<< "$deployment")
echo "Render deployment: $id ($GITHUB_SHA)"

for ((attempt=0; attempt<80; attempt++)); do
  deployment=$(curl --fail --silent --show-error --retry 2 --max-time 20 \
    -H "Authorization: Bearer $RENDER_API_KEY" "$api/$id")
  status=$(jq -er '.status' <<< "$deployment")
  case "$status" in
    live)
      [[ $(jq -er '.commit.id' <<< "$deployment") == "$GITHUB_SHA" ]] || exit 1
      curl --fail --silent --show-error --retry 3 --retry-delay 5 --max-time 15 "$RENDER_HEALTH_URL"
      echo "Render is live at $GITHUB_SHA"
      exit 0
      ;;
    created|queued|build_in_progress|pre_deploy_in_progress|update_in_progress) sleep 15 ;;
    *) echo "Render deployment did not become live: $status" >&2; exit 1 ;;
  esac
done
echo 'Timed out waiting for Render; inspect this deployment before retrying' >&2
exit 1
