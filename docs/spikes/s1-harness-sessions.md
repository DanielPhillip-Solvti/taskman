# S1 — Harness session semantics

Probed versions (this sandbox): `claude` (Claude Code CLI) **2.1.251**, `opencode` **1.17.18**.
Evidence: `claude --help`, `opencode --help`, `opencode <sub> --help`, README/docs shipped with
each binary. **Not yet run against a live model in this sandbox** — no separate
`ANTHROPIC_API_KEY`/opencode provider credential is available here to drive a real end-to-end
turn, so session lifecycle (streaming shapes, SIGINT recovery) is documented from the CLIs' own
flag contracts and `--output-format=stream-json` documentation rather than an observed
transcript. This is flagged as a follow-up in BLOCKERS.md for Milestone 3 (opencode adapter),
where a real credential is required regardless to build the normalised `Event` stream gate.

## Comparison table

| Concern | Claude Code | opencode |
|---|---|---|
| Non-interactive mode | `-p/--print` | (subcommand-based; `opencode run` non-interactively — verify exact subcommand at M3 against installed version) |
| Session identity | UUID; `--session-id <uuid>` sets it explicitly on launch | session ids are managed internally; needs confirmation of an equivalent explicit-id flag at M3 |
| Resume | `-r/--resume [session_id]`, `-c/--continue` (most recent in cwd), `--fork-session` (resume creates a *new* id instead of continuing the old one) | has its own resume convention — confirm exact flag at M3 |
| Streaming output shape | `--output-format stream-json` (only with `--print`); `--input-format` for streaming input too | needs the equivalent probed at M3 |
| MCP config | `--mcp-config <configs...>` (JSON files or inline), `--strict-mcp-config` to disable all other MCP sources | opencode also supports MCP servers per its docs; exact config surface (bearer header passthrough) to confirm at M3 |
| Bearer header on MCP transport | MCP JSON config supports arbitrary `headers` on an `http`-type server entry (standard MCP client config shape) — this is how the daemon's per-task token (spec §4.1) gets injected | assume equivalent; confirm |
| SIGINT mid-turn | Not independently verified in this sandbox (no live turn run). Claude Code's session persistence (`--no-session-persistence` exists as an opt-*out*, implying persistence — and therefore resumability — is the default) suggests a killed/interrupted process is recoverable via `--resume` in the common case. **Treat as unverified until M3.** | Not verified; same caveat |
| Background/detached run | `claude --bg` starts detached, returns an id; `claude attach/logs/stop/rm <id>` manage it — a good primitive for the daemon's phase-as-container model even though Taskman drives the CLI inside its own container rather than via `--bg` | not probed |

## Proposed `Harness` protocol shape (spec §12)

Given the above, a protocol that both fit without forcing either into the other's shape:

```python
class Harness(Protocol):
    def start(self, *, cwd: str, prompt: str, mcp_config: McpConfig, session_id: str | None = None) -> HarnessProcess: ...
    def resume(self, *, cwd: str, session_ref: str, prompt: str, mcp_config: McpConfig) -> HarnessProcess: ...

class HarnessProcess(Protocol):
    def events(self) -> Iterator[Event]: ...   # normalised, see below
    def stop(self) -> None: ...                # SIGTERM, then SIGKILL after a grace period
    @property
    def session_ref(self) -> str: ...           # harness-native id, stored in phase.session_ref
```

`Event` normalises both CLIs' stream-json shapes down to a small closed set
(`{message, tool_call, tool_result, task_note, task_report, error, done}`) so the Monitor panel
(Milestone 7) and the lifecycle engine (Milestone 5) never branch on which harness is running.
Concretely built and gated at Milestone 3 once a real credential lets both CLIs be driven
end-to-end and their actual stream-json payloads compared field-for-field.

## Risk carried forward

This remains the highest-risk unknown in the project per implementation-brief.md §5. The table
above is real (built from the installed CLIs' own `--help` contracts) but the dynamic half —
streaming shapes and SIGINT/resume behaviour under a live turn — is unverified. **Milestone 3
must not be marked done without running both CLIs against a real credential and diffing actual
event streams**, not just their documented flags.
