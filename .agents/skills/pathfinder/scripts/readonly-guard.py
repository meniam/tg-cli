#!/usr/bin/env python3
"""Хук PreToolUse для субагента pathfinder: пускает в Bash только команды чтения.

Список разрешённого закрытый: всё, чего в нём нет, блокируется. Запись в файлы, Git-команды,
меняющие состояние, интерпретаторы (`python3`, `node`, `php`), сеть и Docker не проходят. Редирект
разрешён только в `/dev/null`, `/tmp` и на дескрипторы (`2>&1`).

Хук читает JSON события со stdin. Код выхода `2` блокирует вызов и отдаёт stderr агенту, `0`
пропускает. Любая неожиданная ошибка самого хука тоже блокирует вызов: сбой не должен открывать
запись.

Действует только на вызовы с `agent_type == "pathfinder"` (поле есть и у субагента, вызванного через
Agent tool, и у сессии `claude --agent pathfinder`). Главный агент и другие субагенты проходят без
проверки.

Подключение в `.claude/settings.json` проекта:

    "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command",
      "command": "python3 -I \\"$CLAUDE_PROJECT_DIR/.agents/skills/pathfinder/scripts/readonly-guard.py\\""}]}]}

Хук во frontmatter `.claude/agents/pathfinder.md` не подходит: для субагента, вызванного через Agent
tool, он не срабатывает (проверено на `claude -p`, файлы создавались), а настройки проекта
срабатывают.
"""

from __future__ import annotations

import json
import re
import sys

READ_COMMANDS = {
    "rg", "grep", "egrep", "fgrep", "ast-grep", "sg", "fd", "find",
    "ls", "cat", "head", "tail", "sed", "wc", "cut", "sort", "uniq", "tr", "nl", "diff", "jq",
    "stat", "file", "basename", "dirname", "realpath", "readlink", "pwd", "which",
    "echo", "printf", "true", "false", "test", "[", "cd", "git",
    "paste", "column", "tac", "rev", "comm", "fold", "xargs",
}
XARGS_VALUE_OPTS = {"-n", "-I", "-P", "-d", "-L", "-s", "-E", "-a"}
GIT_READ = {
    "log", "show", "diff", "status", "ls-files", "ls-tree", "blame", "grep", "rev-parse", "rev-list",
    "cat-file", "merge-base", "describe", "shortlog", "name-rev", "show-ref", "branch", "remote",
    "config", "tag", "stash",
}
GIT_BAD_FLAGS = ("--output", "-O", "--open-files-in-pager", "--ext-diff", "--exec", "--upload-pack")
ATLAS_READ = {"explore", "query", "node", "incoming", "outgoing", "affected", "unresolved", "status", "sync", "schema", "help"}
SYSTEM_BIN = {"/bin", "/usr/bin", "/usr/local/bin", "/opt/homebrew/bin"}
ENV_OK = {"LC_ALL", "LANG", "TZ", "CI", "NO_COLOR"}
KEYWORDS_PREFIX = {"do", "then", "else", "elif", "if", "while", "until", "!", "{"}
KEYWORDS_SKIP = {"for", "done", "fi", "}", "in"}
SEPARATORS = {";", "&&", "||", "|", "&", "|&", ";;", "(", ")"}
OUT_REDIRECTS = {">", ">>", ">|", ">&", "&>", "&>>"}
OPERATORS = ("<<<", "&>>", "<<", "&&", "||", ";;", "|&", "&>", ">>", ">&", ">|", "<&", "<(", ">(",
             ";", "&", "|", "<", ">", "(", ")")
ENFORCED_AGENT = "pathfinder"
SUB_MARK = "\x00"
SED_ADDR = r"(?:\d+|\$|/(?:[^/\\]|\\.)*/)"
# Команда sed, которая пишет файл или запускает программу: `w`, `W`, `e` или флаг `w`/`e` у `s///`.
SED_WRITE = re.compile(
    rf"(?:^|[;{{}}\n])\s*(?:{SED_ADDR}(?:\s*,\s*{SED_ADDR})?)?\s*!?\s*"
    r"(?:[wWe](?:\s|$|;)|s(.).*?\1.*?\1[gpiImM0-9]*[we]\b)"
)
HINT = ("pathfinder только читает. Разрешено: bin/atlas (кроме `sync --full` и `init`), rg, grep, ast-grep (без -r/-U), "
        "fd, find (без -exec/-delete), ls, cat, head, tail, sed без -i, wc, cut, sort, uniq, tr, nl, diff, jq, "
        "git log/show/diff/status/blame/ls-files/rev-parse. JSON разбирай `jq`, не интерпретатором. "
        "Редирект только в /dev/null и /tmp.")


class Blocked(Exception):
    """Команда не из списка чтения."""


def read_subst(src: str, i: int) -> tuple[str, int]:
    """Прочитать тело `$(...)`; `i` указывает на символ после `$(`. Вернуть тело и позицию после `)`."""
    depth, j, n = 1, i, len(src)
    while j < n:
        c = src[j]
        if c == "\\":
            j += 2
            continue
        if c == "'":
            j = src.find("'", j + 1)
            if j < 0:
                break
        elif c == '"':
            j += 1
            while j < n and src[j] != '"':
                j += 2 if src[j] == "\\" else 1
        elif c == "(":
            depth += 1
        elif c == ")":
            depth -= 1
            if depth == 0:
                return src[i:j], j + 1
        j += 1
    raise Blocked("незакрытая подстановка $(...)")


def tokenize(src: str) -> tuple[list[tuple[str, str]], list[str]]:
    """Разобрать команду на слова и операторы вне кавычек; подстановки вынести отдельно."""
    tokens: list[tuple[str, str]] = []
    subs: list[str] = []
    cur: list[str] = []
    started = False
    i, n = 0, len(src)

    def flush() -> None:
        nonlocal cur, started
        if started:
            tokens.append(("word", "".join(cur)))
        cur, started = [], False

    def substitution(pos: int) -> int:
        """Обработать `$(` или обратную кавычку в позиции pos; вернуть позицию после подстановки."""
        nonlocal started
        if src.startswith("$(", pos):
            inner, nxt = read_subst(src, pos + 2)
        else:
            nxt = src.find("`", pos + 1)
            if nxt < 0:
                raise Blocked("незакрытая обратная кавычка")
            inner, nxt = src[pos + 1:nxt], nxt + 1
        subs.append(inner)
        cur.append(SUB_MARK)
        started = True
        return nxt

    while i < n:
        c = src[i]
        if c == "\\":
            if i + 1 < n and src[i + 1] == "\n":
                i += 2
                continue
            if i + 1 < n:
                cur.append(src[i + 1])
                started = True
            i += 2
            continue
        if c == "'":
            j = src.find("'", i + 1)
            if j < 0:
                raise Blocked("незакрытая кавычка")
            cur.append(src[i + 1:j])
            started = True
            i = j + 1
            continue
        if c == '"':
            started = True
            i += 1
            while True:
                if i >= n:
                    raise Blocked("незакрытая кавычка")
                d = src[i]
                if d == '"':
                    i += 1
                    break
                if d == "\\" and i + 1 < n:
                    cur.append(src[i + 1])
                    i += 2
                elif src.startswith("$(", i) or d == "`":
                    i = substitution(i)
                else:
                    cur.append(d)
                    i += 1
            continue
        if src.startswith("$(", i) or c == "`":
            i = substitution(i)
            continue
        if c in " \t":
            flush()
            i += 1
            continue
        if c in "\n\r":
            flush()
            tokens.append(("op", ";"))
            i += 1
            continue
        if c == "#" and not started:
            j = src.find("\n", i)
            i = n if j < 0 else j
            continue
        if c in ";&|<>()":
            flush()
            op = next(o for o in OPERATORS if src.startswith(o, i))
            if op in ("<<<", "<<", "<(", ">("):
                raise Blocked(f"оператор `{op}` не разрешён (heredoc и подстановка процессов)")
            tokens.append(("op", op))
            i += len(op)
            continue
        cur.append(c)
        started = True
        i += 1
    flush()
    return tokens, subs


def redirect_ok(target: str) -> bool:
    if target in ("/dev/null", "/dev/stdout", "/dev/stderr", "-") or target.isdigit():
        return True
    return target.startswith(("/tmp/", "$TMPDIR/")) and ".." not in target


def split_segments(tokens: list[tuple[str, str]]) -> list[list[str]]:
    segments: list[list[str]] = [[]]
    it = iter(tokens)
    for kind, text in it:
        if kind == "op" and text in SEPARATORS:
            segments.append([])
        elif kind == "op":
            target = next(it, ("word", ""))[1]
            if text in OUT_REDIRECTS and not redirect_ok(target):
                raise Blocked(f"запись `{text} {target}` запрещена: редирект только в /dev/null и /tmp")
        else:
            segments[-1].append(text)
    return segments


def flags(args: list[str]) -> list[str]:
    return [a for a in args if a.startswith("-")]


def check_git(args: list[str]) -> None:
    i = 0
    while i < len(args) and args[i].startswith("-"):
        if args[i] == "-C":
            i += 2
        elif args[i] in ("--no-pager", "-P"):
            i += 1
        else:
            raise Blocked(f"git: глобальная опция `{args[i]}` не разрешена")
    if i >= len(args):
        return
    sub, rest = args[i], args[i + 1:]
    if sub not in GIT_READ:
        raise Blocked(f"git {sub} меняет состояние: разрешены log, show, diff, status, blame, ls-files, rev-parse")
    for a in rest:
        if a.startswith(GIT_BAD_FLAGS):
            raise Blocked(f"git {sub} {a}: опция пишет файл или запускает программу")
    allowed = {
        "branch": {"--list", "-l", "-a", "-r", "-v", "-vv", "--show-current", "--contains", "--merged", "--no-merged"},
        "remote": {"-v"},
        "config": {"--get", "--get-all", "--list", "-l"},
        "tag": {"-l", "--list"},
        "stash": {"list", "show"},
    }
    # Голые `branch`, `remote`, `tag`, `config` только показывают; голый `stash` прячет изменения.
    bare_writes = sub == "stash"
    if sub in allowed and (rest or bare_writes) and not set(rest) & allowed[sub]:
        raise Blocked(f"git {sub} без {'/'.join(sorted(allowed[sub]))} меняет состояние")


def check_atlas(args: list[str]) -> None:
    words, i = [], 0
    while i < len(args):
        if args[i] in ("-o", "--output"):
            i += 2
            continue
        if not args[i].startswith("-"):
            words.append(args[i])
        i += 1
    sub = words[0] if words else None
    if sub is not None and sub not in ATLAS_READ:
        raise Blocked(f"bin/atlas {sub} не разрешён")
    if sub == "sync" and "--full" in args:
        raise Blocked("bin/atlas sync --full перестраивает индекс: запрещено")


def check_sed(args: list[str]) -> None:
    scripts: list[str] = []
    positional: list[str] = []
    i = 0
    while i < len(args):
        a = args[i]
        if re.match(r"-[a-zA-Z]*i", a) or a.startswith("--in-place"):
            raise Blocked("sed -i правит файл: используй sed -n")
        if a in ("-f", "--file") or a.startswith("--file="):
            raise Blocked("sed -f читает скрипт из файла, проверить его нельзя")
        if a in ("-e", "--expression"):
            scripts.append(args[i + 1] if i + 1 < len(args) else "")
            i += 1
        elif a.startswith("--expression="):
            scripts.append(a.split("=", 1)[1])
        elif not a.startswith("-"):
            positional.append(a)
        i += 1
    if not scripts and positional:
        scripts.append(positional[0])
    if any(SED_WRITE.search(script) for script in scripts):
        raise Blocked("команда `w`/`e` в sed пишет файл или запускает программу")


def check_command(name: str, args: list[str]) -> None:
    fl = flags(args)
    if name == "atlas":
        check_atlas(args)
    elif name == "git":
        check_git(args)
    elif name == "sed":
        check_sed(args)
    elif name == "xargs":
        i = 0
        while i < len(args) and args[i].startswith("-"):
            i += 2 if args[i] in XARGS_VALUE_OPTS else 1
        if i >= len(args):
            raise Blocked("xargs без команды")
        check_segment(args[i:])
    elif name == "find" and any(a in ("-exec", "-execdir", "-ok", "-okdir", "-delete") or a.startswith(("-fprint", "-fls")) for a in args):
        raise Blocked("find с -exec/-delete/-fprint меняет файлы или запускает программы")
    elif name == "sort" and any(a == "-o" or a.startswith(("--output", "-o")) for a in fl):
        raise Blocked("sort -o пишет файл")
    elif name == "rg" and any(a.startswith("--pre") for a in fl):
        raise Blocked("rg --pre запускает программу")
    elif name in ("ast-grep", "sg") and any(a in ("-r", "--rewrite", "-U", "--update-all", "-i", "--interactive") for a in fl):
        raise Blocked("ast-grep с -r/-U правит файлы")
    elif name == "fd" and any(a in ("-x", "--exec", "-X", "--exec-batch") for a in fl):
        raise Blocked("fd --exec запускает программу")


def check_segment(words: list[str]) -> None:
    while words:
        head = words[0]
        assign = re.fullmatch(r"([A-Za-z_][A-Za-z0-9_]*)=.*", head, re.S)
        if assign:
            if assign.group(1) in ENV_OK or assign.group(1).islower():
                words = words[1:]
                continue
            raise Blocked(f"присваивание {assign.group(1)}= не разрешено")
        if head in KEYWORDS_PREFIX:
            words = words[1:]
            continue
        break
    if not words or words[0] in KEYWORDS_SKIP:
        return
    cmd, args = words[0], words[1:]
    if SUB_MARK in cmd or cmd.startswith("$"):
        raise Blocked("имя команды задано переменной или подстановкой")
    name = cmd
    if "/" in cmd:
        directory, _, base = cmd.rpartition("/")
        if cmd in ("bin/atlas", "./bin/atlas") or cmd.endswith("/bin/atlas"):
            name = "atlas"
        elif directory in SYSTEM_BIN:
            name = base
        else:
            raise Blocked(f"запуск `{cmd}` не разрешён")
    if name != "atlas" and name not in READ_COMMANDS:
        raise Blocked(f"команда `{name}` не из списка чтения")
    check_command(name, args)


def inspect(command: str, depth: int = 0) -> None:
    if depth > 3:
        raise Blocked("слишком глубокие подстановки")
    tokens, subs = tokenize(command)
    for sub in subs:
        inspect(sub, depth + 1)
    for segment in split_segments(tokens):
        check_segment(segment)


def main() -> int:
    try:
        event = json.load(sys.stdin)
    except ValueError:
        return 0  # чьё это событие, неизвестно: главного агента не блокируем
    if not isinstance(event, dict) or event.get("tool_name") not in (None, "Bash") or event.get("agent_type") != ENFORCED_AGENT:
        return 0
    try:
        command = (event.get("tool_input") or {}).get("command", "")
        inspect(command)
    except Blocked as reason:
        print(f"Заблокировано: {reason}. {HINT}", file=sys.stderr)
        return 2
    except Exception as error:  # noqa: BLE001 - сбой хука не должен открывать запись
        print(f"Заблокировано: хук pathfinder не смог разобрать вызов ({error!r}). {HINT}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
