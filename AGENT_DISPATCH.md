# Agent Dispatch Model — ta/main

> ta is an **FE+Go** repo and IS the **ta cascade substrate** tool. It uses the **ta**
> coordination substrate (`mcp__ta__*`, `cascade.planner`/`cascade.droplet` records).
> The **canonical model** lives in `sand/main/AGENT_DISPATCH.md` — read it for the full
> dual-path + hermetic-codex + MCP-injection spec. This file is ta's project profile.

## Profile

- **13 agents** (FE+Go): `ta-closeout` + 6 `ta-fe-*` + 6 `ta-go-*` (planning, plan-qa-proof,
  plan-qa-falsification, builder, build-qa-proof, build-qa-falsification per family).
- **Tool matrix**: planning + plan-QA get hylla read-only + context7 + WebSearch (+ LSP/gopls
  for Go, + Playwright for FE); **build-QA gets NO hylla**; builder keeps hylla.
- **Substrate**: ta (`mcp__ta__*`). This is the ONLY distinction from the tillsyn-family
  personas, which use `mcp__tillsyn__till_*`.

## Dispatch (see sand keystone §3–§4)

- OAuth claude (builder/qa-proof/closeout) → **built-in Agent tool only** (never `claude -p`).
- codex (planning/qa-falsification) → **hermetic** `codex exec` (`--ignore-user-config` +
  `-c project_doc_max_bytes=0` + `--ignore-rules` + hermetic `CODEX_HOME` + `web_search="live"`
  + role-conditional inline MCP injection: ta always; hylla-ro for planning/plan-qa; context7
  always; gopls for `*-go-*`; playwright for `*-fe-*`).
- Today: `bin/agent-dispatch.sh` + `.claude/agent-chains.sh` (ta is the canonical copy synced
  to hylla/valv/sand). When sand is working, sand generates this as TOML.

## Endpoint (orch passes it into FE spawn prompts)

- ta's FE is the `web/` Astro/Solid viewer. **ta's CLAUDE.md must hardcode the web-viewer
  live-backend URL** (confirm the port and record it there) and carry the rule: *"the
  orchestrator ALWAYS passes the live-backend URL into FE agent spawn prompts; personas are
  generic and never hardcode it."* The bare Astro standalone (binding-less) is never a target.

## Capabilities, not skills

Per the keystone §5: capabilities come from MCP injection, NOT codex skills. Nothing is copied
into `.agents/skills` or `.codex/`. `@playwright/mcp` is a global npm install.
