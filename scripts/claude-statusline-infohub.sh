#!/usr/bin/env bash
set -euo pipefail

target="${CLAUDE_INFOHUB_RATE_LIMIT_PATH:-${HOME}/.claude/infohub-rate-limits.json}"
payload="$(cat)"

mkdir -p "$(dirname "${target}")"
tmp="${target}.$$"
printf '%s\n' "${payload}" > "${tmp}"
mv "${tmp}" "${target}"

printf 'Claude Code'
