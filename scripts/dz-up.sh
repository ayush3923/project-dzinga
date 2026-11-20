#!/bin/zsh
# Starts core-api, the sync, quiz, organizations and startoff services if they aren't running (for Claude's
# scheduled run). Services listen on loopback only. Logs: ~/Library/Logs/dzinga.
set -eu
cd "$(dirname "$0")/.."
logs="$HOME/Library/Logs/dzinga"
mkdir -p "$logs"
up() { curl -sf -m 2 "$1" >/dev/null 2>&1; }
wait_up() { for _ in {1..60}; do up "$1" && return 0; sleep 2; done; echo "$2 did not start; see $logs" >&2; return 1; }

if ! up http://127.0.0.1:8091/healthz; then
  nohup make core-api >>"$logs/core-api.log" 2>&1 &
  wait_up http://127.0.0.1:8091/healthz core-api
fi
if ! up http://127.0.0.1:8083/healthz; then
  nohup make ingest-service >>"$logs/ingest.log" 2>&1 &
  wait_up http://127.0.0.1:8083/healthz "sync service"
fi
if ! up http://127.0.0.1:8084/healthz; then
  nohup make quiz >>"$logs/quiz.log" 2>&1 &
  wait_up http://127.0.0.1:8084/healthz "quiz service"
fi
if ! up http://127.0.0.1:8085/healthz; then
  nohup make orgs >>"$logs/orgs.log" 2>&1 &
  wait_up http://127.0.0.1:8085/healthz "organizations service"
fi
if ! up http://127.0.0.1:8086/healthz; then
  nohup make startoff >>"$logs/startoff.log" 2>&1 &
  wait_up http://127.0.0.1:8086/healthz "startoff service"
fi
echo "core-api, sync, quiz, organizations and startoff services are up"
