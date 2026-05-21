#!/usr/bin/env bash
# .claude/agent-chains.sh — per-role fallback chains.
# Sourced by bin/agent-dispatch.sh at dispatch time.
#
# Each role maps to a function that emits a pipe-delimited tier table on stdout:
#
#   backend | model | opts | wait_max | slots
#
# Fields:
#   backend   ollama-local | codex-exec | claude-native
#             (ollama-cloud removed — no verified cloud model tag in user's plan)
#   model     model tag (qwen2.5-coder:7b, gpt-5.5, opus, haiku, sonnet)
#   opts      backend-specific flags (codex: --sandbox / -c effort=...) or empty
#   wait_max  seconds to wait acquiring a lock slot (Ollama tier only)
#   slots     max concurrent slots for the Ollama tier (4 default; bounded by NUM_PARALLEL)
#
# Lock policy: ONLY the ollama-local tier uses flock-style slot acquisition.
# codex-exec and claude-native dispatch directly — the external API returns
# 429/401 on overload or auth, and any non-zero exit advances the dispatcher
# to the next tier.
#
# Architectural rule (thermal-safe + cascade-compliant):
#
#   ONLY ONE local Ollama model in the chains: qwen2.5-coder:7b.
#
#   The cascade methodology (docs/cascade-methodology.md) requires builder
#   droplets to touch 1-4 small blocks of code. A 7B coder handles that
#   atomic-scope workload fine. Larger local models (20B+, 30B+) demand
#   tens of GB of KV cache, melt the fan on sustained load, and would only
#   be reached if a planner under-decomposed a droplet — which is the wrong
#   fix anyway (fix the planner, not the model).
#
#   Builder fallback when the 7B slot is unavailable: codex (cheap effort),
#   then claude haiku. NOT a larger local model.
#
#   Planning + QA roles never use local Ollama at all — they need stronger
#   reasoning, and the cost of routing to cloud APIs is justified by the
#   smaller volume per cascade.

emit_chain_for_role() {
  case "$1" in
    ta-go-builder|ta-fe-builder)                     chain_builder ;;
    ta-go-planning|ta-fe-planning)                   chain_planning ;;
    ta-go-qa-falsification|ta-fe-qa-falsification)   chain_qa_falsification ;;
    ta-go-qa-proof|ta-fe-qa-proof)                   chain_qa_proof ;;
    ta-closeout)                                     chain_closeout ;;
    *)  echo "" ;;
  esac
}

# --- Builder ---------------------------------------------------------------
# Atomic 1-4 small blocks per cascade methodology. The 7B handles it.
# Primary is local; fallback is codex (cheap), then claude haiku.
chain_builder() {
  cat <<'EOF'
ollama-local|qwen2.5-coder:7b||20|4
codex-exec|gpt-5.5|--sandbox workspace-write -c model_reasoning_effort=low||
claude-native|haiku||||
EOF
}

# --- Planning --------------------------------------------------------------
# Decomposition needs strong reasoning. Cloud APIs throughout.
chain_planning() {
  cat <<'EOF'
codex-exec|gpt-5.5|--sandbox read-only -c model_reasoning_effort=low||
codex-exec|gpt-5.5|--sandbox read-only -c model_reasoning_effort=medium||
claude-native|sonnet||||
claude-native|opus||||
EOF
}

# --- QA Falsification ------------------------------------------------------
# Adversarial reasoning — deepest available.
# Tier 1 uses effort=xhigh (the highest your codex tier exposes; verified
# working with current ChatGPT-tier auth as of 2026-05-19). If you switch
# to a business / API-key tier that exposes gpt-5-codex, swap the tier-1
# model to gpt-5-codex (which is reasoning-specialized and outperforms
# gpt-5.5 at xhigh effort for adversarial tasks).
#
# Sandbox: workspace-write so the QA agent can run `mage check` / `mage testPkg`
# (which writes to go build cache + pnpm node_modules + /tmp). The persona's
# tools: line is the actual write-discipline (no Edit/Write/Bash(go *)/etc.);
# codex's sandbox is defense-in-depth, not the primary gate.
chain_qa_falsification() {
  cat <<'EOF'
codex-exec|gpt-5.5|--sandbox workspace-write -c model_reasoning_effort=xhigh||
codex-exec|gpt-5.5|--sandbox workspace-write -c model_reasoning_effort=high||
codex-exec|gpt-5.5|--sandbox workspace-write -c model_reasoning_effort=medium||
claude-native|opus||||
claude-native|sonnet||||
EOF
}

# --- QA Proof --------------------------------------------------------------
# Evidence verification. Claude Opus primary; cloud-API fallback only.
# Sandbox: workspace-write so the QA agent can run `mage check`. Persona
# tools: line gates source-write discipline (no Edit/Write tools).
chain_qa_proof() {
  cat <<'EOF'
claude-native|opus||||
codex-exec|gpt-5.5|--sandbox workspace-write -c model_reasoning_effort=medium||
claude-native|sonnet||||
EOF
}

# --- Closeout -------------------------------------------------------------
# Final coordinator before commit. Claude Opus primary; cloud-API only.
# Sandbox: workspace-write so closeout can run `mage check`. Persona
# tools: line gates source-write discipline (no Edit/Write tools).
chain_closeout() {
  cat <<'EOF'
claude-native|opus||||
codex-exec|gpt-5.5|--sandbox workspace-write -c model_reasoning_effort=medium||
claude-native|sonnet||||
EOF
}
