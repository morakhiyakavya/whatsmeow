#!/usr/bin/env bash
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
umask 077

[[ $# == 2 && $1 =~ ^[a-f0-9]{64}$ && $2 =~ ^[a-f0-9]{40}$ ]] || {
  echo 'Expected binary SHA-256 and Git commit SHA' >&2
  exit 1
}
[[ $EUID == 0 ]] || { echo 'Deployment helper must run as root' >&2; exit 1; }
exec 9>/run/lock/venweb-deploy.lock
flock -n 9 || { echo 'Another deployment is running' >&2; exit 1; }

stage=$(mktemp /opt/venweb/.ipo-api.XXXXXX)
trap 'rm -f "$stage"' EXIT
head -c 67108865 > "$stage"
[[ $(stat -c %s "$stage") -le 67108864 ]] || { echo 'Binary exceeds 64 MiB' >&2; exit 1; }
printf '%s  %s\n' "$1" "$stage" | sha256sum --check --status || {
  echo 'Binary checksum mismatch; running service was not changed' >&2
  exit 1
}
chmod 0755 "$stage"
cp -p /opt/venweb/ipo-api /opt/venweb/ipo-api.rollback
had_release=false
if [[ -f /etc/venweb/release.env ]]; then
  cp -p /etc/venweb/release.env /etc/venweb/release.rollback.env
  had_release=true
fi

rollback() {
  trap - ERR HUP INT TERM
  echo 'Deployment failed; restoring the previous binary and release' >&2
  systemctl stop venweb-api
  mv -f /opt/venweb/ipo-api.rollback /opt/venweb/ipo-api
  if $had_release; then
    mv -f /etc/venweb/release.rollback.env /etc/venweb/release.env
  else
    rm -f /etc/venweb/release.env
  fi
  systemctl start venweb-api
  exit 1
}
trap rollback ERR HUP INT TERM
systemctl stop venweb-api
mv -f "$stage" /opt/venweb/ipo-api
printf 'SENTRY_RELEASE=%s\n' "$2" > /etc/venweb/release.env
systemctl start venweb-api
for ((attempt=0; attempt<30; attempt++)); do
  if systemctl is-active --quiet venweb-api && curl --fail --silent --max-time 2 http://127.0.0.1:8180/health > /dev/null; then
    printf '%s\n' "$2" > /opt/venweb/REVISION
    trap - ERR HUP INT TERM
    echo "Azure is healthy at $2"
    exit 0
  fi
  sleep 2
done
rollback
