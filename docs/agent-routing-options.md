# Agent Routing — Capacity & Fallback Options

This document captures the five capacity-management options considered for the multi-backend agent routing system (Ollama local + Ollama Cloud + Codex + Claude Code native), the chosen direction (Option 2 + fallback chains, shell-only), and what is explicitly OUT of scope.

ta is **not** an agent manager. ta is a JSON-style fast-edit API for traditionally-unstructured files (md, html, toml). The agent routing infrastructure described here is **project-local shell scripting**, never ta Go code. The eventual "switcher" graduation lives in a separate repo, not in ta.

---

## The Two-Axis Problem

- **Routing axis** (already solved by `<project>/.claude/agent-routing.sh`): which backend + model per role per project. Each project owns its preferences.
- **Capacity axis** (this document): when 5+ projects share one Mac's RAM, one OpenAI account, and one Anthropic plan, none of them know what the others are doing. Today the only coordination is Ollama's internal queue — Codex and Anthropic have zero local awareness.

The four overload surfaces:

| Surface          | Bound by         | Realistic ceiling                                  |
|------------------|------------------|----------------------------------------------------|
| Local Ollama     | RAM              | ~43 GB at `NUM_PARALLEL=4` + 30B + KV cache        |
| Ollama Cloud     | Plan concurrency | 3 concurrent (user's current plan)                 |
| Codex            | Cost + rate      | Per-account tier; high-effort calls add up         |
| Anthropic        | Rate limit       | Plan-tier dependent (Pro / Max)                    |
| MCP server count | Process count    | ~2-3 GB at 25 concurrent builders (not critical)   |

---

## Option 1 — Status Quo (Ollama daemon queue only)

- **What it is**: do nothing. Rely on `OLLAMA_NUM_PARALLEL=4` to serialize local builder calls. Codex and Anthropic surface rate-limit errors gracefully; dispatcher retries or fails.
- **Failure mode**: Codex/Anthropic 429s under heavy load. No priority — first project to fire wins, others wait silently.
- **Cost**: zero infrastructure.
- **Reusability**: n/a (no system to share).
- **Verdict**: fine for 1-2 simultaneous projects. Breaks at 5+ under sustained cascade.

## Option 2 — File-Lock Semaphore (SELECTED)

- **What it is**: bash-level semaphore using `flock`. Lock files at `/tmp/agent-dispatch/<backend>.<slot>.lock`. Dispatcher acquires one of N slots per backend before invoking; releases on completion.
- **Implementation sketch** (added to `bin/agent-dispatch.sh`):
  ```bash
  LOCK_DIR="/tmp/agent-dispatch"
  mkdir -p "${LOCK_DIR}"
  case "${BACKEND}" in
    ollama-local)  MAX=4 ;;
    ollama-cloud)  MAX=3 ;;
    codex-exec)    MAX=6 ;;
    claude-native) MAX=10 ;;
  esac

  acquire_slot() {
    local backend=$1 max=$2 i fd
    for ((i=1; i<=max; i++)); do
      exec {fd}>"${LOCK_DIR}/${backend}.${i}.lock"
      if flock -n "$fd"; then echo "$fd"; return 0; fi
      exec {fd}>&-
    done
    return 1
  }

  FD=$(acquire_slot "${BACKEND}" "${MAX}") || { sleep 1; ... retry ... }
  # ... dispatch ...
  exec {FD}>&-
  ```
- **Project-specific layer**: each project's `agent-routing.sh` can override `MAX_*` per backend.
- **Failure mode**: no priority (FIFO arrival). No queue-depth visibility. No starvation control. **Acceptable for 5+ project workload.**
- **Cost**: ~30 LOC addition to existing dispatcher. No new daemon. Survives reboot trivially.
- **Reusability**: low — specific to this dispatcher. But the *pattern* is copy-paste portable.
- **Verdict**: **selected as the starting point.** Solves 80% of the overload problem with 5% of the effort. Layered with fallback chains (below) for resilience.

## Option 3 — Shared Limits Manifest (skipped for now)

- **What it is**: single TOML at `~/.config/agent-dispatch/limits.toml` declares system-wide caps + per-project ceilings + budgets. Dispatchers read on each invocation.
- **Why skipped**: TOML parsing in bash is awkward (needs `taplo` / `dasel` / external dep). Counter-file races possible. Same FCFS as Option 2 — no fairness improvement. Worth revisiting only if budgets (codex daily $ cap) become a real concern.

## Option 4 — Dispatch Broker Daemon ("agentq", the open-source play)

- **What it is**: small Go binary, candidate name `agentq` or `llmq`. Long-lived daemon listening on Unix socket at `~/.local/run/agentq.sock`. Dispatchers submit jobs; broker schedules and returns results.
- **Architecture**:
  ```
  ┌──────────────────────────────────────────────┐
  │           agentq daemon (single host)        │
  │  ┌────────────┐  ┌────────────┐              │
  │  │ Job queue  │→ │ Scheduler  │              │
  │  │ (priority, │  │ (per-      │              │
  │  │  FIFO,     │  │  backend   │              │
  │  │  project)  │  │  pools)    │              │
  │  └────────────┘  └─────┬──────┘              │
  │         ┌──────────────┼──────────────┐      │
  │         ▼              ▼              ▼      │
  │   ┌─────────┐   ┌─────────┐    ┌─────────┐   │
  │   │ ollama  │   │ codex   │    │ claude  │   │
  │   │ pool(4) │   │ pool(6) │    │ pool(10)│   │
  │   └─────────┘   └─────────┘    └─────────┘   │
  └──▲─────────────────▲─────────────────▲───────┘
     │ Unix socket     │                 │
  ┌──┴──┐         ┌────┴─┐         ┌─────┴─┐
  │projA│         │projB │   ...   │projE  │
  └─────┘         └──────┘         └───────┘
  ```
- **Protocol**: JSON over Unix socket. Submit job → get job_id. Stream events → result.
- **Capacity policy**: per-backend worker pool, FIFO with priority levels, optional weighted-fairness across projects.
- **Failure mode**: daemon crash → dispatcher falls back to direct invocation (graceful degrade).
- **Cost**: ~800-1500 LOC Go. Homebrew tap. Repository: `cmd/agentq/`, `internal/queue/`, `internal/pool/`, etc.
- **Reusability**: **high** — this is the open-source-friendly form factor. Audience = anyone running multi-backend Claude+Codex+Ollama orchestration.
- **Verdict**: **build later, separate repo, NOT in ta.** Evolve to this when Option 2 + chain fallback starts hurting at scale.

## Option 5 — Per-Project Budget Tokens (layer on top)

- **What it is**: each project declares a "concurrent dispatch token bucket" in its routing config. Dispatcher consumes/returns tokens from project-local lock file.
- **Why a layer**: orthogonal to capacity caps — this is about PER-PROJECT throttling. One project is exploratory and shouldn't ever hog the machine.
- **Integration**: works alongside any of Options 1-4. ~10 LOC addition.
- **Verdict**: optional nice-to-have. Layer on top of Option 2 when one project starts dominating.

---

## Chosen Direction — Option 2 + Fallback Chains (shell-only)

Option 2 alone solves *capacity overload* but doesn't handle *backend failure* (codex auth expires, Anthropic plan rate-limit, ollama daemon down, etc.). Layered on top: each role gets an ordered **fallback chain** of tiers. The dispatcher walks the chain, trying each tier in order until one succeeds.

A tier is a tuple of `(backend, model, opts, capacity-slots, wait-policy)`. The dispatcher per tier:

1. **Preflight check**: backend reachable? Auth valid? Model pulled?
2. **Acquire slot**: `flock -w <wait_seconds>` against `/tmp/agent-dispatch/<backend>.<N>.lock`.
3. **Dispatch**: invoke backend with persona + task.
4. **On success**: write result to stdout, log `served_by=<backend>:<model>` to stderr, exit 0.
5. **On preflight fail OR slot timeout OR dispatch error**: log `tier N skipped because <reason>`, advance to next tier.
6. **All tiers exhausted**: log `CHAIN FAILED` to stderr, exit non-zero.

**Per-role chains** (proposed defaults, each project overrides as needed):

| Role            | Tier 1 (primary)                         | Tier 2                          | Tier 3                          | Tier 4                          | Tier 5 (last resort)              |
|-----------------|------------------------------------------|---------------------------------|---------------------------------|---------------------------------|-----------------------------------|
| builder         | ollama-local qwen3-coder:30b             | ollama-local qwen2.5-coder:7b   | ollama-cloud qwen3-coder:cloud  | codex-exec gpt-5-codex effort=low | claude-native sonnet            |
| planning        | codex-exec gpt-5-codex effort=medium     | codex-exec gpt-5-codex effort=low | claude-native opus            | ollama-cloud qwen3-coder:cloud  | ollama-local qwen3-coder:30b     |
| qa-falsification| codex-exec gpt-5-codex effort=high       | codex-exec gpt-5-codex effort=medium | claude-native opus         | ollama-cloud qwen3-coder:cloud  | ollama-local qwen3-coder:30b     |
| qa-proof        | claude-native opus                       | codex-exec gpt-5-codex effort=medium | ollama-cloud qwen3-coder:cloud | ollama-local qwen3-coder:30b  | (no further fallback)            |
| closeout        | claude-native opus                       | codex-exec gpt-5-codex effort=medium | ollama-cloud qwen3-coder:cloud | (no further fallback)         | —                                 |

**Wait policy per tier** (configurable):

| Role            | Tier 1 wait | Tier 2 wait | Tier 3 wait | Tier 4 wait | Tier 5 wait |
|-----------------|-------------|-------------|-------------|-------------|-------------|
| builder         | 30s         | 15s         | 10s         | 5s          | 5s          |
| planning        | 60s         | 30s         | 20s         | 10s         | 10s         |
| qa-falsification| 60s         | 30s         | 20s         | 10s         | 10s         |
| qa-proof        | 30s         | 20s         | 10s         | 5s          | —           |
| closeout        | 30s         | 15s         | 10s         | —           | —           |

Builder waits are shortest (high volume; degrade fast). Planning/QA-falsif waits are longest (sequential per cascade; can afford to wait for the good model).

---

## What This Solves

- Local-Ollama RAM exhaustion: bounded by `NUM_PARALLEL`-shaped flock semaphore.
- Ollama Cloud 3-concurrent cap: detected via slot exhaustion; falls back to codex automatically.
- Codex API failure (auth expired, rate-limit): preflight catches it; falls back to claude-native.
- Anthropic plan rate-limit: dispatch failure → next tier.
- Cross-project sharing: file locks live in `/tmp/agent-dispatch/`; all projects see the same locks.

## What This Does NOT Solve

- Priority across projects (FCFS only). Mitigated only by Option 4 broker.
- Fairness across projects (a busy project hogs slots). Same mitigation.
- Cost tracking (codex $ spend per day). Option 3 territory.
- Cross-machine coordination. Out of scope; single Mac only.

## What ta Does NOT Do

- ta is NOT an agent manager. ta is JSON-style fast-edit API for unstructured files (md, html, toml, etc.). The agent routing + chain mechanism lives in project-local shell scripts (`.claude/agent-routing.sh`, `.claude/agent-chains.sh`, `bin/agent-dispatch.sh`), never in ta's Go code.
- Account-localized auth (per-project codex / claude-code credentials) is handled by a separate project, not by this dispatcher.
- The eventual broker daemon (Option 4) lives in its own repo, not in ta.

---

## Phased Plan

1. **Now (selected)**: extend `bin/agent-dispatch.sh` with Option 2 file-lock semaphore + fallback chains. Add `.claude/agent-chains.sh` with the per-role tier tables above. Validate in ta/main first.
2. **Soon**: copy the pattern to other projects (manual). Iterate on per-role tier orders + wait policies based on real cascade load.
3. **Later (separate repo)**: when 5+ project workload routinely needs priority/fairness/queue-depth/cost-tracking that file-locks can't provide, build `agentq` as a standalone Go binary in its own repo. Dispatcher becomes a thin client; falls back to direct invocation if `agentq` socket is unreachable.

The phased approach is reversible at each step. File-lock semaphore can coexist with `agentq` later (semaphore as the local cache, broker as the global coordinator).
