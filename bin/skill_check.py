#!/usr/bin/env python3
"""Check the agent skill against the binary: docs/skill/tg versus `tg schema`.

Checks:
  * every `tg <group> <command>` in the skill text names a command of the schema;
  * every `--flag` in a command of a code block belongs to that command or is global;
  * recipes have the frontmatter fields, valid values, and `commands` equal to the
    commands their code blocks use.

Usage: bin/skill_check.py [skill_dir]   (default docs/skill/tg)
Environment: TG_BIN selects the binary (default dist/tg when it exists, else tg).
Exit code: 0 clean, 1 findings, 2 the schema could not be read.
"""
import json
import os
import re
import subprocess
import sys
from pathlib import Path

SAFETY = {}
ACCESS = {"none", "write", "files", "downloads", "admin", "create", "dialogs", "sessions", "profile"}
# Permissions of access.json that a command needs besides `write`: a small map, extend it with the schema.
NEEDS = {
    "message send-media": {"write", "files"},
    "message edit-media": {"write", "files"},
    "message download": {"downloads"},
    "message export-month": {"downloads"},
    "message export-verify": {"downloads"},
    "account terminate": {"sessions"},
    "account set": {"profile"},
    "account privacy set": {"profile"},
    "chat create": {"create"},
    "chat set": {"admin"},
    "chat approve": {"admin"},
    "chat decline": {"admin"},
    "chat ban": {"admin"},
    "chat kick": {"admin"},
    "chat archive": {"dialogs"},
    "chat unarchive": {"dialogs"},
    "chat pin": {"dialogs"},
    "chat unpin": {"dialogs"},
    "chat mute": {"dialogs"},
    "chat unmute": {"dialogs"},
    "chat notify": {"dialogs"},
    "chat restrict": {"admin"},
    "chat unban": {"admin"},
    "chat permissions": {"admin"},
    "chat slow-mode": {"admin"},
    "folder create": {"dialogs"},
    "folder edit": {"dialogs"},
    "folder order": {"dialogs"},
    "folder delete": {"dialogs"},
}
LIVE = re.compile(r"^(no|partial|\d{4}-\d{2}-\d{2})$")
REQUIRED = ("title", "task", "writes", "access", "commands", "live_checked", "tags")
FENCE = re.compile(r"^\s*(```|~~~)")
TG_CALL = re.compile(r"(?:^|[\s|;&(){}])tg\s+([^|;&()\n]*)")
INLINE = re.compile(r"`([^`\n]+)`")
FLAG = re.compile(r"(?<![\w-])--([a-z][a-z0-9-]*)")
WORD = re.compile(r"^[a-z][a-z-]*$")


def load_schema():
    binary = os.environ.get("TG_BIN") or ("dist/tg" if Path("dist/tg").exists() else "tg")
    try:
        out = subprocess.run([binary, "schema"], capture_output=True, text=True, check=True).stdout
        data = json.loads(out)["data"]
    except (OSError, subprocess.CalledProcessError, ValueError, KeyError) as err:
        print(f"cannot read `{binary} schema`: {err}", file=sys.stderr)
        sys.exit(2)
    commands = {c["path"]: {f["name"] for f in c.get("flags") or []} for c in data["commands"]}
    glob = {f["name"] for f in data.get("global_flags") or []}
    GLOBAL_FLAGS.update(glob)
    SAFETY.update({c["path"]: c.get("safety", "") for c in data["commands"]})
    return commands, glob


GLOBAL_WITH_VALUE = {"profile", "format", "fields", "expect-user"}


GLOBAL_FLAGS = set()


def strip_global(words):
    """Drop known global flags that stand before the command: `tg --profile work chat list`."""
    i = 0
    while i < len(words) and words[i].startswith("--"):
        name = words[i][2:].split("=")[0]
        if name not in GLOBAL_FLAGS:
            break
        i += 2 if name in GLOBAL_WITH_VALUE and "=" not in words[i] else 1
    return words[i:]


def split_command(words, commands):
    """Longest command path at the start of the words; returns (path, rest)."""
    words = strip_global(words)
    path = []
    for w in words:
        if not WORD.match(w):
            break
        path.append(w)
    for n in range(len(path), 0, -1):
        if " ".join(path[:n]) in commands:
            return " ".join(path[:n]), words[n:]
    return None, words


def logical_lines(block):
    out, cur = [], ""
    for line in block:
        stripped = line.rstrip()
        if stripped.endswith("\\"):
            cur += stripped[:-1] + " "
        else:
            out.append(cur + stripped)
            cur = ""
    if cur:
        out.append(cur)
    return out


def bare_line(line):
    """The line without quoted strings and without a trailing `# comment`.

    A quoted redirect target (`> "$out"`) stays visible as X. Backslash escapes inside double quotes are honored.
    """
    out, quote, prev, redirect, i = [], "", " ", False, 0
    while i < len(line):
        ch = line[i]
        if quote:
            if ch == "\\" and quote == '"':
                i += 2
                continue
            if ch == quote:
                quote = ""
            i += 1
            continue
        if ch in "\"'":
            quote = ch
            if redirect:
                out.append("X")
                redirect = False
            i += 1
            continue
        if ch == "#" and prev.isspace():
            break
        if ch == ">":
            redirect = True
        elif not ch.isspace():
            redirect = False
        out.append(ch)
        prev = ch
        i += 1
    return re.sub(r"<[^<>\s]*>", "", "".join(out))


def code_lines(text):
    out, block, in_block = [], [], False
    for raw in text.split("\n"):
        if FENCE.match(raw):
            if in_block:
                out.extend(logical_lines(block))
                block = []
            in_block = not in_block
        elif in_block:
            block.append(raw)
    return out


def parse_frontmatter(text):
    if not text.startswith("---\n"):
        return None, text
    end = text.find("\n---\n", 4)
    if end < 0:
        return None, text
    fields = {}
    for line in text[4:end].split("\n"):
        if ":" in line:
            k, v = line.split(":", 1)
            fields[k.strip()] = v.strip()
    return fields, text[end + 5:]


def parse_list(value):
    m = re.match(r"^\[(.*)\]$", value)
    return [x.strip() for x in m.group(1).split(",") if x.strip()] if m else None


def scan(path, text, commands, glob, flags_in_blocks, problems):
    """Return the set of commands used in code blocks."""
    used = set()
    in_block = False
    block = []
    lineno = 0
    for raw in text.split("\n"):
        lineno += 1
        if FENCE.match(raw):
            if in_block:
                for line in logical_lines(block):
                    for call in TG_CALL.finditer(line):
                        words = call.group(1).split()
                        cmd, rest = split_command(words, commands)
                        if cmd is None:
                            first = next((w for w in words if not w.startswith("-")), "")
                            if WORD.match(first):
                                problems.append(f"{path}: no such command in the schema: tg {' '.join(words[:3])}")
                            continue
                        used.add(cmd)
                        if flags_in_blocks:
                            for f in FLAG.findall(" ".join(rest)):
                                if f not in commands[cmd] and f not in glob:
                                    problems.append(f"{path}: `tg {cmd}` has no flag --{f}")
                block = []
            in_block = not in_block
            continue
        if in_block:
            block.append(raw)
            continue
        for span in INLINE.findall(raw):
            m = re.match(r"^tg\s+(.+)$", span)
            if not m:
                continue
            words = strip_global(m.group(1).split())
            if not words or not WORD.match(words[0]) or words[0] in ("schema", "version"):
                if words and words[0].startswith("--"):
                    problems.append(f"{path}:{lineno}: unknown flag before the command: `{span}`")
                continue
            cmd, _ = split_command(words, commands)
            second = words[1] if len(words) > 1 else ""
            if cmd is None and (not _is_group(words[0], commands) or WORD.match(second)):
                problems.append(f"{path}:{lineno}: no such command in the schema: `{span}`")
    return used


def _is_group(word, commands):
    return any(p == word or p.startswith(word + " ") for p in commands)


def check_recipe(path, text, commands, glob, problems):
    fm, body = parse_frontmatter(text)
    if fm is None:
        problems.append(f"{path}: no frontmatter")
        return
    for key in REQUIRED:
        if key not in fm:
            problems.append(f"{path}: frontmatter has no `{key}`")
    if fm.get("writes") not in ("true", "false"):
        problems.append(f"{path}: `writes` must be true or false")
    access = parse_list(fm.get("access", ""))
    if not access or not set(access) <= ACCESS:
        problems.append(f"{path}: `access` must be a non-empty list of {sorted(ACCESS)}")
        access = access or []
    elif "none" in access and len(access) > 1:
        problems.append(f"{path}: `access` has `none` together with other values")
    if not LIVE.match(fm.get("live_checked", "")):
        problems.append(f"{path}: `live_checked` must be a date, `partial` or `no`")
    declared = parse_list(fm.get("commands", ""))
    if declared is None:
        problems.append(f"{path}: `commands` must be a list")
        return
    for c in declared:
        if c not in commands:
            problems.append(f"{path}: `commands` names {c!r}, which is not in the schema")
    writing = [c for c in declared if SAFETY.get(c) in ("account_write", "local_write")]
    if writing and fm.get("writes") != "true":
        problems.append(f"{path}: `commands` has write commands {writing}, so `writes` must be true")
    for c in declared:
        if SAFETY.get(c) == "account_write" and c.startswith("message ") and "write" not in access:
            problems.append(f"{path}: `{c}` needs `write` in `access`")
    account = [c for c in declared if SAFETY.get(c) == "account_write" and not c.startswith("auth ")]
    if account and not set(access) & {"write", "admin", "create", "dialogs", "sessions", "profile"}:
        problems.append(f"{path}: the recipe uses account_write commands {account}, so `access` must name a write permission (write, admin, create, dialogs, sessions or profile)")
    for c in declared:
        missing = NEEDS.get(c, set()) - set(access)
        if missing:
            problems.append(f"{path}: `{c}` needs {sorted(missing)} in `access`")
    for line in code_lines(body):
        if line.lstrip().startswith("#"):
            continue
        bare = bare_line(line)
        if re.search(r">>?\s*[^\s&>]", bare) or re.search(r"--out(put)?\b", bare) or re.search(r"\|\s*tee\b", bare):
            if fm.get("writes") != "true":
                problems.append(f"{path}: the code writes a file ({line.strip()[:60]!r}), so `writes` must be true")
            break
    used = scan(path, body, commands, glob, True, problems)
    for c in sorted(used - set(declared)):
        problems.append(f"{path}: the code uses `tg {c}`, which `commands` does not list")
    for c in sorted(set(declared) - used):
        problems.append(f"{path}: `commands` lists {c!r}, which no code block uses")


def main():
    skill = Path(sys.argv[1] if len(sys.argv) > 1 else "docs/skill/tg")
    commands, glob = load_schema()
    problems = []
    files = sorted(skill.glob("*.md")) + sorted((skill / "references").glob("*.md"))
    for f in files:
        scan(str(f), f.read_text(), commands, glob, True, problems)
    recipes = [f for f in sorted((skill / "recipes").glob("*.md")) if f.name != "_template.md"]
    for f in recipes:
        check_recipe(str(f), f.read_text(), commands, glob, problems)
    for p in problems:
        print(p)
    print(f"checked {len(files)} files and {len(recipes)} recipes against {len(commands)} commands: "
          f"{len(problems)} findings")
    sys.exit(1 if problems else 0)


if __name__ == "__main__":
    main()
