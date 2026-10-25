#!/usr/bin/env bash
set -euo pipefail
[[ $EUID == 0 ]] || { echo 'Run this isolated helper test with sudo' >&2; exit 1; }
sandbox=$(mktemp -d)
trap 'rm -rf "$sandbox"' EXIT
mkdir -p "$sandbox/app" "$sandbox/config" "$sandbox/bin" "$sandbox/lock"

# Exercise the real helper in a disposable filesystem with simulated service/health commands.
sed -e "s|/opt/venweb|$sandbox/app|g" \
  -e "s|/etc/venweb|$sandbox/config|g" \
  -e "s|/run/lock|$sandbox/lock|g" \
  -e "s|^export PATH=.*|export PATH=$sandbox/bin:/usr/sbin:/usr/bin:/sbin:/bin|" \
  "$(dirname "$0")/deploy-venweb.sh" > "$sandbox/helper"
export VENWEB_TEST_SANDBOX="$sandbox"
cat > "$sandbox/bin/systemctl" <<'MOCK'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$1" >> "$VENWEB_TEST_SANDBOX/service-calls"
if [[ $1 == start ]] && grep -q bad-start "$VENWEB_TEST_SANDBOX/app/ipo-api"; then exit 1; fi
MOCK
cat > "$sandbox/bin/curl" <<'MOCK'
#!/usr/bin/env bash
if grep -q bad-signal "$VENWEB_TEST_SANDBOX/app/ipo-api"; then
  kill -TERM "$PPID"
  exit 1
fi
! grep -q bad-health "$VENWEB_TEST_SANDBOX/app/ipo-api"
MOCK
printf '#!/bin/sh\nexit 0\n' > "$sandbox/bin/sleep"
chmod +x "$sandbox/bin/"*
commit=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
printf 'old binary\n' > "$sandbox/app/ipo-api"
printf 'SENTRY_RELEASE=old\n' > "$sandbox/config/release.env"
printf 'private runtime config\n' > "$sandbox/config/api.env"
cp "$sandbox/config/api.env" "$sandbox/expected-config"
printf 'new binary\n' > "$sandbox/input"
digest=$(sha256sum "$sandbox/input" | cut -d ' ' -f 1)

if bash "$sandbox/helper" invalid "$commit" < "$sandbox/input"; then exit 1; fi
if bash "$sandbox/helper" "${digest/a/b}" invalid < "$sandbox/input"; then exit 1; fi
if bash "$sandbox/helper" "$(printf '0%.0s' {1..64})" "$commit" < "$sandbox/input"; then exit 1; fi
[[ ! -f $sandbox/service-calls ]]
grep -qx 'old binary' "$sandbox/app/ipo-api"

bash "$sandbox/helper" "$digest" "$commit" < "$sandbox/input"
cmp "$sandbox/input" "$sandbox/app/ipo-api"
grep -qx "SENTRY_RELEASE=$commit" "$sandbox/config/release.env"
grep -qx "$commit" "$sandbox/app/REVISION"

for failure in bad-health bad-start bad-signal; do
  printf '%s\n' "$failure" > "$sandbox/input"
  digest=$(sha256sum "$sandbox/input" | cut -d ' ' -f 1)
  if bash "$sandbox/helper" "$digest" bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb < "$sandbox/input"; then exit 1; fi
  grep -qx 'new binary' "$sandbox/app/ipo-api"
  grep -qx "SENTRY_RELEASE=$commit" "$sandbox/config/release.env"
  grep -qx "$commit" "$sandbox/app/REVISION"
done
rm "$sandbox/config/release.env"
printf 'bad-health\n' > "$sandbox/input"
digest=$(sha256sum "$sandbox/input" | cut -d ' ' -f 1)
if bash "$sandbox/helper" "$digest" "$commit" < "$sandbox/input"; then exit 1; fi
[[ ! -f $sandbox/config/release.env ]]
grep -qx 'new binary' "$sandbox/app/ipo-api"
cmp "$sandbox/expected-config" "$sandbox/config/api.env"
[[ -z $(find "$sandbox/app" -name '.ipo-api.*' -print -quit) ]]
echo 'Checksum rejection, deployment, health/start/signal rollback and configuration preservation passed'
