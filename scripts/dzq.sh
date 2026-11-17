#!/bin/zsh
# Calls Dzinga's local services with the internal token, which stays in .env.local
# and is never printed or passed on the command line.
#   scripts/dzq.sh GET  /internal/v1/research-requests
#   scripts/dzq.sh GET  quiz/internal/v1/pending       (the quiz service)
#   scripts/dzq.sh PUT  quiz/internal/v1/quizzes quiz.json
#   scripts/dzq.sh POST sync                  (starts a mailbox sync)
#   scripts/dzq.sh GET  sync                  (sync status)
set -eu
root="$(cd "$(dirname "$0")/.." && pwd)"
method="$1"; target="$2"; body="${3:-}"
case "$method" in GET|PUT|POST|PATCH) ;; *) echo "method must be GET, PUT, POST or PATCH" >&2; exit 2 ;; esac
if [[ "$target" == sync ]]; then
  url="http://127.0.0.1:8083/v1/sync"
  [[ "$method" == POST && -z "$body" ]] && body=/dev/null
elif [[ "$target" == quiz/api/v1/* || "$target" == quiz/internal/v1/* ]]; then
  url="http://127.0.0.1:8084${target#quiz}"
elif [[ "$target" == /api/v1/* || "$target" == /internal/v1/* ]]; then
  url="http://127.0.0.1:8091$target"
else
  echo "path must start with /api/v1/, /internal/v1/, quiz/api/v1/ or quiz/internal/v1/, or be 'sync'" >&2; exit 2
fi
token="$(grep '^DZ_INTERNAL_TOKEN=' "$root/.env.local" | cut -d= -f2-)"
args=(-sS -m 60 -X "$method" -H "Content-Type: application/json" -K -)
[[ -n "$body" ]] && args+=(--data-binary "@$body")
[[ "$target" == sync && "$method" == POST && "$body" == /dev/null ]] && args=(-sS -m 60 -X POST -H "Content-Type: application/json" -K - --data '{"mailbox_ids":[]}')
# The header goes in via a curl config on stdin, so it never appears in ps output.
printf 'header = "Authorization: Bearer %s"\n' "$token" | curl "${args[@]}" -w '\nHTTP %{http_code}\n' "$url"
