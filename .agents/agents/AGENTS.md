# Project agents

| Agent | Role |
| --- | --- |
| [pathfinder](pathfinder.md) | scouting through the code and documents of tg-cli with Atlas, read only |

The Claude Code profile of an agent is `.claude/agents/<name>.md` and points to the file in this
folder. The Codex profile is `.codex/agents/<name>.toml`; both clients read the shared instruction
`<name>.md` from this folder.

The skill `pathfinder` (`.agents/skills/pathfinder/`) holds the order of work and the Atlas commands;
its `scripts/readonly-guard.py` is a `PreToolUse` hook from `.claude/settings.json` that lets only
read commands into the shell of this agent.
