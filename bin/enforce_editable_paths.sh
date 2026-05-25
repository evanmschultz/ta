#!/usr/bin/env bash
# PreToolUse Edit/Write gate for dispatched CLAUDE agents. Two modes, auto-selected
# from the hook input — so ONE script serves both dispatch shapes:
#
#   (A) Agent-tool SUBAGENT  (agent_id present): in-process subagents SHARE the
#       orchestrator's env, so we can't key on an env var. Gate by agent IDENTITY
#       against a gates file { "<agent_type>": ["/abs/path", ...] } that the
#       orchestrator writes before dispatch (default $HOME/.ta-edit-gates.json,
#       override TA_EDIT_GATES_FILE). Keyed by agent_type (the subagent_type name).
#       LIMIT: parallel spawns of the SAME agent_type with DIFFERENT lists can't be
#       distinguished (orchestrator can't pre-set agent_id) — use serial dispatch,
#       distinct agent_types, or Mode B for those.
#
#   (B) subprocess agent — `claude -p` (agent_id ABSENT, TA_EDITABLE_PATHS set):
#       each subprocess has an ISOLATED env, so gate against the colon-separated
#       TA_EDITABLE_PATHS the dispatcher exported for THIS process. Parallel-safe.
#
#   (C) orchestrator MAIN THREAD (no agent_id, no TA_EDITABLE_PATHS): defer (exit 0).
#       The orchestrator edits freely and is NEVER gated.
#
# Codex does NOT use this hook (apply_patch PreToolUse hooks hang on codex 0.133.0);
# codex is gated by execpolicy (git-mutation/commands) + `-C` workspace (writes).
# Fail-OPEN on internal error so a hook bug can never wedge a session.
set -u
input="$(cat 2>/dev/null)" || exit 0
command -v jq >/dev/null 2>&1 || exit 0

tool="$(printf '%s' "$input" | jq -r '.tool_name // empty' 2>/dev/null || true)"
case "${tool}" in Edit|Write|MultiEdit|NotebookEdit) ;; *) exit 0 ;; esac

fp="$(printf '%s' "$input" | jq -r '.tool_input.file_path // empty' 2>/dev/null || true)"
agent_id="$(printf '%s' "$input" | jq -r '.agent_id // empty' 2>/dev/null || true)"

deny() {
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":%s}}\n' \
    "$(printf '%s' "$1" | jq -Rs .)"
  exit 0
}

allowed="" ; who=""
if [ -n "${agent_id}" ]; then
  # Mode A — Agent-tool subagent.
  agent_type="$(printf '%s' "$input" | jq -r '.agent_type // empty' 2>/dev/null || true)"
  [ -z "${agent_type}" ] && exit 0
  GATES="${TA_EDIT_GATES_FILE:-$HOME/.ta-edit-gates.json}"
  [ -f "${GATES}" ] || exit 0
  jq -e --arg t "${agent_type}" 'has($t)' "${GATES}" >/dev/null 2>&1 || exit 0   # unconfigured -> defer
  allowed="$(jq -r --arg t "${agent_type}" '.[$t][]' "${GATES}" 2>/dev/null || true)"
  who="agent '${agent_type}'"
else
  # Mode B — subprocess (claude -p). Orchestrator main thread never sets this var.
  [ -z "${TA_EDITABLE_PATHS:-}" ] && exit 0
  allowed="$(printf '%s' "${TA_EDITABLE_PATHS}" | tr ':' '\n')"
  who="this dispatch"
fi

[ -z "${fp}" ] && deny "${who}: could not determine edit target path"
fpb="$(basename "${fp}")"
while IFS= read -r a; do
  [ -z "${a}" ] && continue
  [ "${fp}" = "${a}" ] && exit 0
  [ "${fpb}" = "$(basename "${a}")" ] && exit 0
done <<EOF
${allowed}
EOF
deny "${who} is not permitted to edit '${fp}' (not in its call-time allowed list)"
