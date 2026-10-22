#!/usr/bin/env bash
# make-dummy-sessions.sh — create N dummy opencode sessions (default 20) that
# show up in `opencode session list` from the directory you run it in.
#
# Uses the sanctioned `opencode import` path (offline, no API calls). Sessions
# created this way survive opencode's project reconciliation, unlike raw
# `opencode db` inserts which get pruned by the running server.
#
# Gotcha baked in: imported sessions are bound to the project of the CWD where
# import runs (directory is taken from CWD, not the JSON).
#
# Usage:
#   ./make-dummy-sessions.sh            # 20 sessions, bound to CWD's project
#   ./make-dummy-sessions.sh 5          # 5 sessions
#   N=5 ./make-dummy-sessions.sh        # 5 sessions

set -euo pipefail

N=${1:-${N:-20}}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

cwd=$(pwd)
now=$(( $(date +%s) * 1000 ))

hex() { od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'; }

mins=(2 5 30 90 240 720 1440 2880 4320)

fails=0
for ((i = 1; i <= N; i++)); do
	sid="ses_dummy_$(hex 11)"
	mid="msg_$(hex 13)"
	prt="prt_$(hex 13)"
	pid=$(hex 16)
	created=$(( now - mins[RANDOM % 9] * 60000 ))
	updated=$(( created + 60000 + RANDOM * 1740 ))

	printf '{"info":{"id":"%s","slug":"dummy-%d","projectID":"%s","directory":"%s","path":"%s","title":"dummy session %d","agent":"plan","model":{"id":"deepseek-v4-flash","providerID":"deepseek","variant":"high"},"version":"1.18.14","time":{"created":%s,"updated":%s}},"messages":[{"info":{"role":"user","time":{"created":%s},"agent":"plan","model":{"providerID":"deepseek","modelID":"deepseek-v4-flash","variant":"high"},"id":"%s","sessionID":"%s"},"parts":[{"type":"text","text":"dummy","id":"%s","messageID":"%s","sessionID":"%s"}]}]}' \
		"$sid" "$i" "$pid" "$cwd" "${cwd#/}" "$i" "$created" "$updated" "$created" "$mid" "$sid" "$prt" "$mid" "$sid" >"$TMP/dummy$i.json"

	if ! opencode import "$TMP/dummy$i.json" >/dev/null 2>&1; then
		fails=$((fails + 1))
		echo "failed: $sid"
	fi
done

echo "inserted $((N - fails)), failed $fails"
echo "--- sessions now visible ---"
opencode session list | tail -n +3 | head -"$N"
