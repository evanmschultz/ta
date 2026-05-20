# Agent Backend Routing

This document is the full explainer for the multi-backend subagent routing pattern: how Claude Code orchestrator sessions dispatch to Ollama (local + cloud), Codex, or Anthropic backends per role, with per-role fallback chains and Ollama-only concurrency locks.

It is **project-agnostic**. Any Claude-Code-orchestrated repo can adopt the pattern by copying three files and a CLAUDE.md section. References to the user's home directory point at the canonical reference implementation in `ta/main` — use them as your starting templates.

---

## Why This Exists

Claude Code is the orchestrator of choice. Its subagent dispatch (`Agent` tool + `.claude/agents/*.md` personas) gives specialization, fresh context per dispatch, clean tool allowlists. But every dispatch consumes Anthropic tokens — even routine focused builder edits that a cheap local model could handle.

Three 2026-era facts make a smarter routing pattern viable:

1. **Ollama v0.14.0 (Jan 2026) serves the Anthropic Messages API directly** at `http://localhost:11434/v1/messages`. Set `ANTHROPIC_BASE_URL` to that, and `claude -p` runs as a full Claude Code session (all tools, all MCP servers) with Ollama doing the inference. Zero Anthropic token cost.
2. **`codex exec --ephemeral --ignore-user-config --ignore-rules`** runs headless with no memory across calls as long as no `AGENTS.md` exists in the project root. Per-call persona via the prompt itself.
3. **Claude Code's headless `claude -p`** mode supports `--allowedTools`, `--append-system-prompt`, `--no-session-persistence`, `--output-format json`. It honors `ANTHROPIC_BASE_URL` redirection for the Ollama trick AND falls back to api.anthropic.com when the env var is unset.

Put together: each subagent role routes to a primary backend, with ordered fallbacks to other backends. The dispatcher walks the chain until one tier succeeds.

---

## Role-Appropriate Tool Allowlists (The Actual Sandbox)

**Rule: Every dispatched agent runs in its host runtime (Claude Code via Ollama endpoint, Claude Code via Anthropic OAuth, or codex exec) and DOES THE WORK ITSELF using a role-appropriate tool allowlist. The orchestrator coordinates; the agent acts.**

The sandbox is not "no tools" — it's "the specific tools this role needs, scoped to its responsibility":

- **Planners**: ta create/update/get/search (write plans into ta cascade records) + read code (Read, Grep, hylla) + library docs (context7). NO Edit/Write/Bash on source files — planners author plans, not code.
- **Plan-QA + Build-QA**: read-only — Read, Grep, Glob, hylla (read symbols), ta get/search (read cascade), `Bash(git diff *)` + `Bash(git log *)` for diff inspection. NO Edit/Write — QA verifies, doesn't change.
- **Builders**: Edit, Write, Read, Grep, Glob, Bash, LSP — the full editing surface for source code. ta get/search (read cascade context) but NOT ta create/update (cascade records belong to planners + closeout). Builders DO the actual file edits — that's the entire point of the role.
- **Closeout**: Read + `Bash(git *)` (verify diff) + `Bash(mage check)` (final gate) + ta update (mark cascade nodes complete). No Edit/Write — closeout coordinates, doesn't author.

### How The Sandbox Is Enforced

The persona file's YAML frontmatter `tools:` line is the canonical role-to-tools mapping. The dispatcher passes that as `--allowedTools` to the spawned Claude Code session:

```bash
claude -p --allowedTools "Read, Edit, Write, Grep, Glob, Bash, LSP, mcp__ta__get, mcp__hylla__hylla_search, ..." ...
```

Claude Code enforces the allowlist at runtime. The model can REQUEST any tool, but only the allowlisted ones execute. Tools outside the list silently fail.

For codex dispatch, the equivalent mechanism is `--sandbox`:
- `--sandbox read-only` for planners + QA + closeout (no file writes)
- `--sandbox workspace-write` for builders (can edit files inside the project tree)

**The persona file IS the sandbox spec.** Edit `.claude/agents/<role>.md` (via `mcp__ta__update` since these are ta records, then `ta template save --kind=agent`) to tighten role permissions. Do NOT add ta Go code to enforce this — the sandbox lives in the per-role markdown allowlist + the dispatcher's flag-passing.

### Token-Efficiency For Ollama Dispatches

For ollama-routed dispatches specifically, the spawned `claude -p` uses additional flags to keep input tokens manageable:

```bash
claude -p --bare --mcp-config <project>/.mcp.json --allowedTools "<persona-tools-line>" \
  --append-system-prompt "<persona-body>" --no-session-persistence ...
```

- `--bare` — skips CLAUDE.md auto-discovery, hooks, LSP auto-init, auto-memory, plugin sync, keychain reads. Saves ~30K input tokens per call (we measured 32K → ~5K).
- `--mcp-config <path>` — explicitly loads the project's MCP servers (ta, hylla, context7). `--bare` disables auto-discovery; this re-enables MCP for the specific config file.
- `--allowedTools "<list>"` — restricts to the persona's `tools:` line. The model only sees descriptions for tools in this list.

Codex and Claude-native dispatches DO NOT use `--bare` (they need full context / keychain auth). Only ollama dispatch is `--bare`-ified for the token savings, since `claude -p`'s default behavior loads a 32K+ system prompt that the small local model doesn't benefit from.

### What's NOT Hermetic Anymore (Correction From An Earlier Draft Of This Document)

An earlier version of this doc proposed `--tools ""` (no tools at all) for ollama dispatches, with the orchestrator parsing text-out responses and applying edits itself. **That was wrong.** Builders are supposed to do the editing work; if the orchestrator does it, the role becomes pointless. The correct model is: builders run with role-appropriate tools (Edit/Write/Read/Bash + scoped MCP), the orchestrator dispatches and coordinates, the persona's allowlist limits what the dispatched agent can do.

The earlier hermetic design IS still available if you want to use a small local model in a generation-only mode (no tools, output as text). But it's NOT how the cascade methodology is supposed to work — and we don't use it in this reference implementation.

---

## Cascade Methodology Constraint (Read This First)

The cascade agent methodology — documented in [`docs/cascade-methodology.md`](cascade-methodology.md) for ta, and adapted per-project elsewhere — defines a **strict atomicity contract** for builder droplets:

> A builder droplet touches **≤4 small blocks of code**. If more, the planner has under-decomposed and must split further. Builders are NOT general-purpose programmers; they are focused leaf-level workers.

This constraint is **load-bearing** for the routing design. Because builder droplets are atomic 1-4 block edits, **a small coder model handles them fine**. The reference implementation uses **exactly one local model — `qwen2.5-coder:7b`** — as the builder primary. Larger local models (20B, 30B) are NOT in any chain because:

- A 30B local model carries ~24 GB of KV cache at moderate context sizes (measured: qwen3-coder:30b loaded with `model weights: 17.1 GiB device=Metal` + `kv cache: 24.0 GiB device=Metal` + compute graph = 42 GB total resident). That melts the fan and starves the rest of the machine.
- Reaching a larger local model in a builder dispatch would only happen if a planner emitted an under-decomposed droplet. The right fix is **fix the planner, not the model** — split the droplet smaller.
- Builder fallback when the 7B slot is unavailable: route to cloud (codex cheap effort, then claude haiku). Faster, doesn't thermal-throttle, and frees the local slot for other projects.

**Planning + QA roles never use local Ollama at all.** They need stronger reasoning, and their per-cascade volume is small (one planner call per planning step; one QA pair per planner level). Cost of cloud API calls is justified by the volume.

If your project's cascade-decomposition contract differs, retune the per-role chains in `.claude/agent-chains.sh` accordingly. The pattern is the same; the model picks are project-specific. But: **never add a 20B+ local model to a builder chain** unless your hardware can sustain it indefinitely without thermal-throttling.

---

## Architecture

### Files (per project that adopts this)

| Path                                       | Purpose                                                                |
|--------------------------------------------|------------------------------------------------------------------------|
| `<project>/.claude/agent-chains.sh`        | Per-role fallback chains. Sourceable bash. Each role = a function emitting a pipe-delimited tier table. |
| `<project>/bin/agent-dispatch.sh`          | The dispatcher. Walks the role's chain; preflight + slot acquisition (Ollama only) + dispatch per tier. |
| `<project>/.claude/agents/<role>.md`       | Personas (existing Claude Code agent definitions; unchanged, shared between native Agent tool and the Bash dispatcher). |
| `<project>/CLAUDE.md`                      | Agent Routing section pointing the orchestrator at the dispatcher.    |
| `<project>/docs/agent-backend-routing.md`  | This file.                                                            |

Canonical reference implementation on the user's system:

- `/Users/evanschultz/Documents/Code/hylla/ta/main/.claude/agent-chains.sh`
- `/Users/evanschultz/Documents/Code/hylla/ta/main/bin/agent-dispatch.sh`
- `/Users/evanschultz/Documents/Code/hylla/ta/main/CLAUDE.md` (Agent Routing section)
- `/Users/evanschultz/Documents/Code/hylla/ta/main/docs/agent-backend-routing.md` (this file)

### Backends

Four backends are supported by the dispatcher:

- **`ollama-local`** — Local Ollama daemon at `http://localhost:11434`. Spawns `claude -p` with `ANTHROPIC_BASE_URL` redirected. Subagent runs as a full Claude Code session (Read/Edit/Bash/MCP all work) with the local model doing inference. Anthropic token cost = zero. ONLY model in chains: `qwen2.5-coder:7b` (builder primary).
- **`codex-exec`** — `codex exec --ephemeral --ignore-user-config --ignore-rules` spawns a headless Codex session. Codex's own tool surface (NOT Claude Code's). Persona pre-pended to the prompt. No AGENTS.md in project root → full isolation per call. Default model: `gpt-5.4` (verified working on ChatGPT-tier subscription; `gpt-5-codex` does NOT work for ChatGPT-tier auth).
- **`claude-native`** — `claude -p` with `ANTHROPIC_BASE_URL` UNSET — uses the user's actual Anthropic subscription against api.anthropic.com. CONSUMES ANTHROPIC TOKENS. Used as a fallback when ollama and codex are unavailable, OR as the primary for qa-proof + closeout roles (where Claude Opus is the right tool for the job).

**`ollama-cloud` was previously a backend but is REMOVED from the reference chains** — the user's plan exposes specific cloud model tags (e.g. `glm-4.7:cloud`, `minimax-m2.1:cloud`) but `qwen3-coder:cloud` is NOT one of them. The dispatcher's preflight can't distinguish "tag exists" from "tag doesn't exist" without making an API call, so we drop the tier entirely to avoid 404 noise. If your Ollama Cloud plan exposes a coder-class model and you want it as a builder fallback, add it back to `.claude/agent-chains.sh` with the verified tag — but be aware of the 3-concurrent cap.

### Chain Mode

Each role maps to an ordered list of tiers in `.claude/agent-chains.sh`. The dispatcher walks the chain:

1. **Preflight check**: backend reachable? Auth valid? Model pulled (Ollama local only)?
2. **For ollama-* tiers only**: acquire a slot via atomic `mkdir`. Wait up to `wait_max` seconds; if no slot, advance to next tier.
3. **Dispatch**: invoke backend with persona + task on stdin.
4. **On non-zero exit OR preflight fail OR slot timeout**: advance to next tier.
5. **On all-tiers-exhausted**: print `CHAIN FAILED` to stderr, exit 1.

### Lock Policy — Ollama Only

Lock dirs at `/tmp/agent-dispatch/<backend>.<N>.lock`. Atomic acquisition via `mkdir` (POSIX-portable; no `flock(1)` dependency — macOS doesn't ship it). Stale-lock cleanup via PID file inside each lock + `kill -0` liveness check.

**Codex and Claude Code tiers get no lock**. The reasoning:

- We can't predict OpenAI's or Anthropic's per-account rate limit from our side.
- The API itself signals overload (429s, 401s on auth fail).
- The dispatcher just dispatches and checks the exit code. Non-zero = advance to next tier.
- Simpler than guessing; lets the service be the authority on its own concurrency.

This means cross-project parallelism naturally degrades: if 5 projects fire Codex calls and Codex tier rate-limits, those calls fall through to claude-native or back to ollama, which they CAN control. The system gracefully redistributes load.

---

## Per-Role Chain Defaults (Reference Implementation)

Defined in `<project>/.claude/agent-chains.sh`. Each project's chains can differ.

```
builder chain (1-4 small blocks per cascade methodology — small models suffice):
  tier 1: ollama-local qwen2.5-coder:7b           (20s wait, 4 slots) ← PRIMARY
  tier 2: codex-exec gpt-5.4 (effort=low)         (no wait, no slot)
  tier 3: claude-native haiku                     (no wait, no slot)

planning chain (decomposition needs reasoning — cloud APIs only):
  tier 1: codex-exec gpt-5.4 (effort=medium)      (no wait) ← PRIMARY
  tier 2: codex-exec gpt-5.4 (effort=low)         (no wait)
  tier 3: claude-native opus                      (no wait)
  tier 4: claude-native sonnet                    (no wait)

qa-falsification chain (adversarial — deepest reasoning, cloud APIs only):
  tier 1: codex-exec gpt-5.4 (effort=high)        (no wait) ← PRIMARY
  tier 2: codex-exec gpt-5.4 (effort=medium)      (no wait)
  tier 3: claude-native opus                      (no wait)
  tier 4: claude-native sonnet                    (no wait)

qa-proof chain (evidence verification, cloud APIs only):
  tier 1: claude-native opus                      (no wait) ← PRIMARY
  tier 2: codex-exec gpt-5.4 (effort=medium)      (no wait)
  tier 3: claude-native sonnet                    (no wait)

closeout chain (cloud APIs only):
  tier 1: claude-native opus                      (no wait) ← PRIMARY
  tier 2: codex-exec gpt-5.4 (effort=medium)      (no wait)
  tier 3: claude-native sonnet                    (no wait)
```

### Builder Tier Ordering Rationale

Tier 1 is the ONLY local coder model in the chain — qwen2.5-coder:7b. The cascade methodology guarantees builder droplets are 1-4 small blocks; a 7B coder handles them fine. Tiers 2-3 are cloud APIs as the local-overloaded fallback (codex cheap effort, then claude haiku). No larger local models exist in the chain — sustained 20B+ inference thermal-throttles the machine, and reaching them would only happen if a planner under-decomposed a droplet (fix the planner, not the model).

### Planning + QA-Falsification Tier Ordering Rationale

Decomposition and adversarial reasoning benefit from strong models. All tiers are external APIs (Codex effort cascading, then Claude opus/sonnet). No Ollama tiers — these roles fire less frequently than builders (one planner per planning step, one QA pair per planner level), so cloud API cost is justified by the lower volume, and the reasoning quality matters more than the cost savings.

### QA-Proof + Closeout Tier Ordering Rationale

Proof verification and closeout coordinate across artifacts and need the orchestrator's strength. Claude Opus is the primary. Closeout has no Ollama fallback because closeout-against-a-local-30B is worse than failing loudly and asking the dev to retry.

---

## Dispatcher Flow

```
                    ┌─────────────────────────────┐
                    │  Claude Code Orchestrator   │
                    │  (your interactive session) │
                    └──────────────┬──────────────┘
                                   │
                Reads CLAUDE.md "Agent Routing" section:
                    - native Agent tool for some roles
                    - Bash → bin/agent-dispatch.sh for others
                                   │
                ┌──────────────────┴───────────────────┐
                ▼                                      ▼
   ┌──────────────────────┐              ┌────────────────────────┐
   │ Claude-native via    │              │  bin/agent-dispatch.sh │
   │ Agent tool (when     │              │  --role <ta-role>      │
   │ chain mode is        │              │                        │
   │ bypassed — see       │              │  Walks chain:          │
   │ "Native Agent path") │              │  1. Preflight tier N   │
   └──────────────────────┘              │  2. Slot acquire (lock │
                                          │     for ollama only)  │
                                          │  3. Dispatch:         │
                                          │     - dispatch_ollama │
                                          │     - dispatch_codex  │
                                          │     - dispatch_claude │
                                          │       _native         │
                                          │  4. If non-zero exit: │
                                          │     advance tier N+1  │
                                          │  5. All tiers fail:   │
                                          │     CHAIN FAILED      │
                                          └───────────┬────────────┘
                                                      │
                            ┌─────────────────────────┼─────────────────────────┐
                            ▼                         ▼                         ▼
                ┌──────────────────────┐  ┌──────────────────────┐  ┌──────────────────────┐
                │ Ollama Daemon        │  │ Codex CLI            │  │ Claude Code CLI      │
                │ localhost:11434      │  │ codex exec --ephem.  │  │ claude -p            │
                │                      │  │                      │  │                      │
                │ Local model (qwen3-  │  │ gpt-5.4          │  │ Without              │
                │ coder, gpt-oss,      │  │ (high|medium|low     │  │ ANTHROPIC_BASE_URL   │
                │ qwen2.5-coder)       │  │  effort)             │  │ → api.anthropic.com  │
                │                      │  │                      │  │ (consumes tokens)    │
                │ OR cloud-tagged      │  │                      │  │                      │
                │ model (cloud proxy)  │  │                      │  │                      │
                └──────────────────────┘  └──────────────────────┘  └──────────────────────┘
```

### Native Agent Path (Not Through Dispatcher)

For roles like `ta-go-qa-proof`, `ta-fe-qa-proof`, `ta-closeout` (where the primary is `claude-native` with the user's Anthropic subscription anyway), CLAUDE.md instructs the orchestrator to use Claude Code's NATIVE `Agent` tool with `subagent_type = <role>`. This:

- Reads `.claude/agents/<role>.md` directly (same persona file).
- Uses Anthropic Opus per the user's session.
- Skips the dispatcher entirely (no chain walk needed since the primary tier == the orchestrator's own model).

The dispatcher's `claude-native` backend is the FALLBACK path for builder/planning/qa-falsif roles when ollama and codex have failed. For roles whose PRIMARY is claude-native (qa-proof, closeout), the orchestrator bypasses the dispatcher and uses Agent directly.

---

## Anti-Recursion

When `claude -p` runs in dispatch mode, the spawned Claude session reads the project's CLAUDE.md — including the Agent Routing section telling it to call `bin/agent-dispatch.sh` for builder/planning/qa-falsif roles. Without intervention, a spawned-builder Claude could try to dispatch sub-builders, infinitely.

Mitigation: the dispatcher appends a "DISPATCH CONTEXT" suffix via `--append-system-prompt`:

```
You are the <role> agent, dispatched via bin/agent-dispatch.sh. Execute the task below directly. Do NOT call agent-dispatch.sh, do NOT use the Agent tool to spawn other roles, do NOT route the task elsewhere. You ARE the role. The orchestrator will handle any further dispatches.
```

This works because the suffix is the LAST thing in the spawned session's system prompt — strongest position for recency-biased instruction following. In practice, the spawned model has a narrow task and doesn't try to fan out.

---

## Memory Isolation

- **`claude -p`** (ollama-local, ollama-cloud, claude-native paths): `--no-session-persistence` is set per call. No session log written. Each invocation starts fresh.
- **`codex exec`**: `--ephemeral` + `--ignore-user-config` + `--ignore-rules` + no project-root `AGENTS.md` = fully isolated. No session log, no global config, no `.rules`, no project AGENTS.md.
- **Project CLAUDE.md is still read** by `claude -p` unless `--bare` is passed. Deliberately not passed so MCP servers + tools remain discoverable. Anti-recursion suffix handles the side effect.

---

## Adopting This Pattern In A New Project

### One-Time Machine Setup

1. **Install Ollama v0.14.0+**: `brew install ollama` or download from [ollama.com](https://ollama.com). Verify: `ollama --version` reports ≥ 0.14.
2. **Start the daemon** (`ollama serve` or menubar app). Verify END-TO-END through Claude Code (not via raw curl — we want to confirm Claude Code can reach Ollama, not just that Ollama is up):
   ```bash
   echo "Reply with one sentence: ping." | \
     ANTHROPIC_BASE_URL=http://localhost:11434 \
     ANTHROPIC_AUTH_TOKEN=ollama \
     claude -p --model qwen2.5-coder:7b --output-format json --no-session-persistence
   ```
   Expected: a JSON blob with `result` containing the model's reply. This is the canonical "Claude Code + Ollama endpoint" smoke — if this works, the whole inference path is wired correctly.
3. **Set Ollama daemon env vars** for parallel work:
   ```bash
   export OLLAMA_NUM_PARALLEL=4
   export OLLAMA_MAX_LOADED_MODELS=2
   export OLLAMA_KEEP_ALIVE=24h
   ```
   (Or `launchctl setenv` + relaunch menubar Ollama.app.)
4. **Pull THE builder model** (only one): `ollama pull qwen2.5-coder:7b` (~5 GB Q4). This is the ONLY local model in the chains. Do not pull bigger local coder models for the dispatcher — they aren't in any chain (see "Cascade Methodology Constraint" above for why).
5. **Install codex CLI**: `brew install codex` or per OpenAI's current install instructions. Authenticate: `codex login`. Verify the model your tier supports: `codex exec --ephemeral --skip-git-repo-check -m gpt-5.4 "ping"` — if it returns "model not supported", check `~/.codex/config.toml`'s `model =` line and update `.claude/agent-chains.sh` to match. ChatGPT-tier subscriptions typically do NOT have `gpt-5-codex`; use `gpt-5.4` or whatever your config has set.
6. **Verify Claude Code is current**: `claude --version` — needs `-p`, `--model`, `--allowedTools`, `--no-session-persistence`, `--append-system-prompt`. Present as of Claude Code 2026-04+.

### Per-Project Setup

Copy three files into `<myproj>`:

1. **`<myproj>/.claude/agent-chains.sh`** — copy from `/Users/evanschultz/Documents/Code/hylla/ta/main/.claude/agent-chains.sh`. Edit the per-role chain functions to match `<myproj>`'s agents. Adjust model picks based on your project's preferences and budget.
2. **`<myproj>/bin/agent-dispatch.sh`** — copy from `/Users/evanschultz/Documents/Code/hylla/ta/main/bin/agent-dispatch.sh`. No edits required; the script auto-resolves `REPO_ROOT` from its own location. Make it executable: `chmod +x bin/agent-dispatch.sh`.
3. **`<myproj>/CLAUDE.md`** — add an "Agent Routing — Backend Dispatch" section copied from the reference impl. Adjust the role list to match `<myproj>`'s agents.

Verify with a dry run:

```bash
cd <myproj>
echo "ping" | ./bin/agent-dispatch.sh --role <a-builder-role> --dry-run
```

It should print the `claude -p` command it WOULD execute, including the env-var settings and the `--allowedTools` string parsed from the persona file's frontmatter.

Then a live run:

```bash
echo "ping" | ./bin/agent-dispatch.sh --role <a-builder-role>
```

Should return a JSON blob (Claude Code's `--output-format json` shape) whose `result` field contains the model's response. If it does — the pattern is live.

### Tell The Orchestrator To Use The Routing

The CLAUDE.md "Agent Routing — Backend Dispatch" section IS the instruction. When you start a Claude Code session in `<myproj>`, it reads CLAUDE.md and sees the routing table. From then on, when the orchestrator decides to dispatch a builder/planning/qa-falsif role, it invokes Bash → `./bin/agent-dispatch.sh` instead of the native `Agent` tool. For qa-proof + closeout, it uses the native Agent tool directly (those roles' primary is claude-native anyway).

Verify by asking the orchestrator: "How would you dispatch the `<myproj-builder>` role for the next slice?" — it should describe the Bash invocation, not the Agent tool.

---

## Operational Checklist (Multi-Project Cascade Work)

Before running cascades across 5+ projects simultaneously:

```
□ Ollama daemon up:               pgrep -x ollama
□ Env vars active:                env | grep OLLAMA_NUM_PARALLEL  (=4)
□ qwen2.5-coder:7b pulled:        ollama list | grep qwen2.5-coder
                                  (this is the ONLY local model needed)
□ Primary model warm via Claude Code (NOT raw ollama):
    echo "ping" | ANTHROPIC_BASE_URL=http://localhost:11434 \
      ANTHROPIC_AUTH_TOKEN=ollama claude -p --model qwen2.5-coder:7b \
      --output-format json --no-session-persistence >/dev/null
□ Codex auth valid:               codex --version
□ Claude auth valid:              claude --version
□ (Ollama Cloud no longer in chains; skip OLLAMA_API_KEY unless you've added a verified cloud tier yourself.)
□ Per-project setup:
    □ .claude/agent-chains.sh present
    □ bin/agent-dispatch.sh executable
    □ CLAUDE.md has the Agent Routing section
    □ .claude/agents/<role>.md persona files present
□ Dry-run dispatch from each project:
    echo "ping" | ./bin/agent-dispatch.sh --role <a-builder-role> --dry-run
```

If `OLLAMA_NUM_PARALLEL=4` and you run 5+ projects, expect 4 concurrent local-builder dispatches at any moment; the rest queue inside the Ollama daemon (which is fine — daemon handles queueing). Cross-project parallelism is structurally safe: each project has its own checkout, its own cascade records, its own MCP server set.

---

## Known Caveats

1. **claude-native fallback consumes Anthropic tokens.** This is the deliberate trade-off. When ollama AND codex are unavailable, the chain falls to claude-native — using your subscription. Better than failing the dispatch entirely, but watch your subscription usage if claude-native is firing frequently (it shouldn't unless your local + codex setup has chronic problems).
2. **Ollama's count_tokens 404.** Ollama's Anthropic endpoint returns 404 for `/v1/messages/count_tokens`. Claude Code falls back to local estimation. `total_cost_usd` in `--output-format json` may be inaccurate for Ollama calls.
3. **tool_choice ignored** by Ollama's Anthropic endpoint. If a persona says "you must use tool X", the spawned model may not honor it. Write personas permissively.
4. **LSP staleness.** The `pre_agent_lsp_refresh.sh` hook (if you've installed it) fires on Claude's native `Agent` tool dispatch — NOT on Bash-dispatched calls. After recent file edits, manually invoke your LSP-refresh skill (e.g. `/gopls-sync` for Go) BEFORE dispatching any Bash-routed role.
5. **Anti-recursion is prompt-shaped.** It's an instruction in the system prompt, not a hard runtime block. A misbehaving model could ignore it. If recursion symptoms appear, harden by adding `--disallowedTools "Bash(./bin/agent-dispatch.sh *)"` to the dispatcher's `claude -p` invocation.
6. **MCP server proliferation.** Each `claude -p` spawns the project's `.mcp.json` server set. 25 concurrent builder spawns = ~25 ta-MCP + ~25 hylla-MCP + ~25 context7-client processes. Each is small (~30 MB) but the count is real (~2-3 GB total).
7. **Stale lock cleanup.** If a dispatcher process dies abnormally (kill -9, OOM), its lock dir persists in `/tmp/agent-dispatch/`. Stale-lock detection via PID file + `kill -0` reaps these on the next acquire attempt. Worst case: one slot is unavailable for ~1 second per acquire-loop iteration. Reboot clears `/tmp` on most macOS setups; otherwise `rm -rf /tmp/agent-dispatch/*` resets manually.
8. **Cascade methodology compliance.** The builder chain's primary is a SMALL model (qwen2.5-coder:7b) because the cascade contract says builders are atomic 1-4 block edits. If your planners are emitting larger builder droplets, you're using the wrong tool — fix the planner, not the routing. The 30B fallback exists for genuine edge cases, not as a hedge against under-decomposition.

---

## Future Work (Out Of Scope For This File)

- **`agentq` broker daemon**: when 5+ project cascade work routinely needs priority/fairness/queue-depth across projects that file-locks alone can't provide, build a small Go binary in its own repo (NOT in any individual project). Dispatcher becomes a thin client; falls back to direct invocation if the broker socket is unreachable. See `docs/agent-routing-options.md` for the design sketch.
- **JSON-wrapped responses**: today the dispatcher writes the backend's response to stdout unchanged. A v2 could wrap with metadata (`served_by`, `originally_requested`, `fallback_used`, `tiers_tried`, `total_duration_s`) for orchestrator-side parsing without grepping stderr. Skipped for v1 — stderr diagnostic is enough.
- **Per-project budget tokens**: orthogonal to capacity caps. Useful when one project should never hog the machine. Layer on top of any chain config; minor addition.
- **Cross-machine coordination**: if a team shares an Ollama instance, the file-lock approach doesn't extend across machines. The `agentq` broker could grow to handle this; out of scope for the shell-script pattern.

---

## References

- [Ollama Anthropic API compatibility docs](https://docs.ollama.com/api/anthropic-compatibility) — confirms `/v1/messages` endpoint, supported features, known limitations.
- [Ollama Claude blog (Jan 2026)](https://ollama.com/blog/claude) — release announcement.
- [Anthropic Claude Code headless docs](https://code.claude.com/docs/en/headless) — `claude -p`, `--allowedTools`, `--no-session-persistence`, env-var routing.
- [Qwen2.5-Coder:7B HF model card](https://huggingface.co/Qwen/Qwen2.5-Coder-7B-Instruct) — the recommended builder primary.
- [Qwen3-Coder:30B HF model card](https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct) — deep fallback for non-atomic droplets.
- [OpenAI Codex subagents docs](https://developers.openai.com/codex/subagents) — codex subagent .toml schema (we don't use this; persona injection goes via prompt prefix).
- `docs/cascade-methodology.md` (in `ta/main`) — the cascade contract whose atomic-builder rule justifies the small-model-primary design for builders.
- `docs/agent-routing-options.md` — the five capacity-management options considered, with rationale for the file-lock-semaphore choice and the eventual `agentq` broker.
