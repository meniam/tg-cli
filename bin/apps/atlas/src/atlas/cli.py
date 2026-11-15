"""Командная строка Atlas: одна команда — один JSON-объект в stdout.

Здесь собирается граф объектов: конфигурация, хранилище, синхронизация, запросы.
Прогресс и предупреждения идут в stderr, чтобы stdout всегда разбирался как JSON.
"""

import argparse
import contextlib
import difflib
import json
import os
import re
import sys
import time
from pathlib import Path
from typing import TYPE_CHECKING, Any, NoReturn

import atlas
from atlas import explore, query, search, status_parts
from atlas.config import CONFIG_NAME, Config, discover, load, schema_text
from atlas.errors import AtlasError, ExitCode
from atlas.resolve.ecmascript import EcmaResolver
from atlas.resolve.go import GoResolver
from atlas.resolve.go_modules import GoModules
from atlas.resolve.index import NodeIndex
from atlas.resolve.modules import ModuleResolver
from atlas.resolve.php import PhpResolver
from atlas.resolve.python import PythonResolver
from atlas.resolve.python_modules import PythonModules
from atlas.resolve.resolver import Resolver
from atlas.store.db import Store
from atlas.sync import SyncReport, stale_files, synchronize, write_lock

if TYPE_CHECKING:
    from collections.abc import Iterable, Mapping, Sequence

    from atlas.records import NodeRow
    from atlas.resolve.inputs import ResolutionInputs

_INDEX_COMMANDS = frozenset(
    {
        "explore",
        "query",
        "status",
        "sync",
        "node",
        "incoming",
        "outgoing",
        "affected",
        "unresolved",
    }
)


_FORMATTER = argparse.RawDescriptionHelpFormatter

# Форматы вывода ``-o``: JSON одной строкой, JSON с отступами, пути файлов строками.
_OUTPUTS = ("json", "pretty", "path")
# Команды, у которых ответ — список файлов: только они понимают ``-o path``.
_PATH_OUTPUT = frozenset({"query", "affected"})

# Поля ответа в справке команды: агент читает ответ по этим именам. Подробно и с
# обоснованием каждого поля — docs/command/<команда>.md.
_ANSWERS: dict[str, str] = {
    "explore": """\
Answer (JSON):
  missing_names    code names from the query found nowhere in the project
  ambiguous_names  names with more than five definitions and their count:
                   narrow them down as Class::method
  seeds            symbols the answer starts from: kind, name, file; check that
                   the right one was picked
  chain            call path between the named symbols, if one was found:
    name, file     a step of the path
    at             lines where the previous step calls this one
  files            source of the relevant symbols, most relevant first:
    file           path
    symbols        short names of the shown symbols
    code           numbered lines read from disk; gaps marked with …
    collapsed      some bodies are cut to signatures: read them with node --code
  more_files       files beyond --limit or --max-chars
  docs             up to three docs on the topic: file, title, snippet
  boundaries       places where the static path ends and the target is chosen
                   at run time:
    file           the place
    form           bus, container, dynamic_method, dynamic_static,
                   dynamic_class, callable, getattr, dynamic_import, globals,
                   computed_call, ambiguous, interface
    code           the line
    key            dispatch key: class from new X, X::class or a string literal
    name           symbol the call branches on: ambiguous, interface
    candidates     places of possible targets
  impact           what depends on the found symbols, two levels up:
    symbols        dependent symbols; 0 means the code is free to change
    files, tests   dependent files and test files among them
    top_callers    up to five main callers: kind, name, file
Empty fields and defaults are omitted.""",
    "node": """\
Answer (JSON):
  node             the symbol card; with 2-5 symbols under one name:
                   ambiguous: true and all cards in nodes
    kind           class, interface, method, function, file, route ...
    name           full name; a route is its method and path
    file           path:start-end, can be passed to node as is
    codebase       when not the root one
    scope          file scope when not source
    generated      true for a generated file: an edit here is lost
    signature      declaration header
    extends, implements, traits
                   ancestors of a class
    returns, directives, wrapped_by
                   return type, 'use client', memo(...)
    route          framework, path, methods, route_name, apps, controller,
                   host, condition, operation, origin
    parent         enclosing symbol: name, file
    members        class members by kind: {"method": {"handle": 21}}
    incoming       who refers to the symbol, by link kind
    outgoing       what the symbol refers to, by link kind: calls,
                   instantiates, references, extends, implements, uses_trait,
                   renders, imports, routes_to, requests
      kind, name, file
                   the symbol at the other end
      at           lines where the link is written: in the caller's file for
                   incoming, inside the card for outgoing
      guess        confidence of a guessed link: check it by reading the line
      more         how many are not shown beyond --limit, per link kind
    unresolved     references that did not resolve: line, kind, name, status,
                   candidates
    code           source with line numbers, with --code
Empty fields and defaults are omitted.""",
    "incoming": """\
Answer (JSON):
  symbol           the symbol the walk starts from: name, file
  incoming         callers, breadth first:
    kind, name, file
                   the caller
    at             call lines in the caller's file
    depth          walk level when more than 1
    edge           link kind when not calls: instantiates, renders, routes_to,
                   requests
    guess          confidence when every link to it is guessed: check by reading
Empty fields and defaults are omitted.""",
    "outgoing": """\
Answer (JSON):
  symbol           the symbol the walk starts from: name, file
  outgoing         callees, breadth first:
    kind, name, file
                   the callee
    at             call lines in the calling symbol's file
    depth          walk level when more than 1
    edge           link kind when not calls: instantiates, renders, routes_to,
                   requests
    guess          confidence when every link to it is guessed: check by reading
Empty fields and defaults are omitted.""",
    "affected": """\
Answer (JSON):
  files            test files that depend on the changed files, nearest first;
                   an empty list means no test depends on them
  unknown          given paths that are not in the index: their tests were not
                   searched
With -o path: test paths one per line, unknown paths go to stderr.""",
    "unresolved": """\
Answer (JSON):
  counts           references by status and kind: status, kind, count
  refs             references, ordered by path and line:
    file           the place
    kind           the lost link: calls, instantiates, extends ...
    name           the name that did not resolve
    status         unresolved, ambiguous or external
    candidates     places of the candidates, for ambiguous
Empty fields are omitted.""",
    "sync": """\
Answer (JSON):
  {}               the index is in sync
  index            full_rebuild: true after a full rebuild; stale_files when
                   another process holds the index""",
    "schema": """\
Answer (JSON):
  schema           JSON Schema for .atlas.json""",
    "init": """\
Answer (JSON):
  created          files created: add them to git
  existing         on error: files that are already there""",
    "query": """\
Answer (JSON):
  meta             how the query was understood and how complete the answer is:
    words          query words after stop words are dropped
    variants       extra forms a word was searched by: stem, English terms
                   from the glossary, .atlas.json synonyms ("вход" -> login);
                   only if any
    missing_words  words found nowhere in the project: fix the spelling or
                   rephrase
    total          records that matched before the cut: 3 and 30000 mean
                   different confidence that the answer is in the first ten
  results          records, best first; at most two per file:
    file           path:start-end, can be passed to node as is
    kind           class, interface, trait, enum, type, function, method, file
                   (as in node) or section (a Markdown section)
    name           short symbol name, for code
    title          heading, for a section
    snippet        the record line with the query words
    score          final score; the gap between neighbours matters, not the
                   value
    match          strongest place of the match: exact (a code name from the
                   query found whole), name, path, links (file imports), text
    missed         query words absent from this record; omitted when all
                   matched
    coverage       share of the query weight the record covers (rare words
                   weigh more); only with missed
    scope          file scope when not source: test, documentation, agent or a
                   custom one
    generated      true for a generated file; it ranks below hand-written code
Empty fields and defaults are omitted.""",
}

# Цвета справки те же, что у just (bin/lib/help.sh): заголовок ярко-белым, ключи белым.
_HEADING = "\033[1;97m"
_KEY = "\033[37m"
_RESET = "\033[0m"
_ANSWER_KEY = re.compile(
    r"^( {2}| {4}| {6})([\w.]+(?:, [\w.]+)*)(?=  |$)", re.MULTILINE
)


def _answer_help(text: str) -> str:
    """Описание полей ответа; в терминале заголовок и ключи подсвечены.

    Returns:
        Текст для справки команды.
    """
    if not sys.stdout.isatty() or os.environ.get("NO_COLOR"):
        return text
    heading, _, body = text.partition("\n")
    body = _ANSWER_KEY.sub(rf"\1{_KEY}\2{_RESET}", body)
    return f"{_HEADING}{heading}{_RESET}\n{body}"


# Команды, которым нужен вход: символ, текст, файлы или каталог.
_NEEDS_INPUT = frozenset(
    {"explore", "query", "node", "incoming", "outgoing", "affected", "init"}
)

# WHY: всё, что видит агент (справка, ошибки, прогресс), — на английском: агенты читают его
# увереннее, а в ответе не смешиваются языки.
_COMMANDS: dict[str, tuple[str, str]] = {
    "explore": (
        "how X works in one call: symbols, call chain, source, dynamic boundaries",
        'atlas explore "how PlaceOrderHandler charges money through Gateway"\n'
        '  atlas explore "PlaceOrderHandler::handle StripeGateway::charge"\n'
        '  atlas explore "marketplace order import" --limit 6 --max-chars 20000',
    ),
    "query": (
        "search code and docs by words; documents rank above code",
        'atlas query "some phrase"\n'
        '  atlas query "supplier list" --scope documentation\n'
        "  atlas query ListSuppliersHandler --kind method --limit 20\n"
        "  atlas query AclDomain -o path            # file paths only, like rg -l\n"
        '  atlas query "invoice migration" --scope migration',
    ),
    "node": (
        "card of a symbol or file: where it is, what it contains, its incoming and outgoing calls",
        "atlas node App\\UseCase\\Supplier\\Supplier\\List\\ListSuppliersHandler\n"
        "  atlas node ListSuppliersHandler::handle --code\n"
        "  atlas node admin/src/features/suppliers/api/suppliers.ts:84",
    ),
    "incoming": (
        "who calls a symbol, walking several levels up",
        "atlas incoming ListSuppliersHandler::handle --depth 3",
    ),
    "outgoing": (
        "what a symbol calls, walking several levels down",
        "atlas outgoing ListSuppliersProvider::provide --depth 2",
    ),
    "affected": (
        "tests affected by changed files",
        "atlas affected src/Order/PlaceOrderHandler.php\n"
        "  git diff --name-only --relative | atlas affected - -o path\n"
        "  atlas affected src/Payment/Gateway.php --glob 'tests/Unit/*'",
    ),
    "unresolved": (
        "references that could not be resolved or resolved unambiguously",
        "atlas unresolved --path src/AdminApi\n  atlas unresolved --status ambiguous --limit 20",
    ),
    "status": (
        "index state by group: index, file, graph, ref, search, framework",
        "atlas status\n  atlas status kind             # values for query --kind\n"
        "  atlas status scope kind\n"
        "  atlas status file help        # what the fields mean\n"
        "  atlas status --no-sync -o pretty",
    ),
    "sync": (
        "sync the index with disk (rarely needed: every command does it itself)",
        "atlas sync\n  atlas sync --full",
    ),
    "schema": (
        "JSON Schema for .atlas.json, for editor hints",
        "atlas schema > atlas.schema.json",
    ),
    "init": (
        "put bin/atlas and a .atlas.json template into a project directory",
        "atlas init ~/www/my-project",
    ),
    "help": ("this help or help for a command", "atlas help node"),
}

_OVERVIEW = """Atlas: a project map for AI agents, a code graph for TS, JS, PHP, Python and Go plus a docs index.

Usage: atlas <command> [arguments] [options]

Commands:
{commands}

A symbol can be given as:
  App\\Foo\\Bar               fully qualified class or function name
  Bar::run, Store.load      method with a short class name
  Bar                       short name (if ambiguous: list of candidates, exit code 4)
  src/Foo/Bar.php           file
  src/Foo/Bar.php:42        innermost symbol on the line

Options of every command, accepted anywhere:
  -o, --output FORMAT       json (default, one line), pretty (indented) or
                            path (file paths, one per line: query, affected)
  -n, --no-sync             answer from the current index without syncing
  -h, --help                help

Every command syncs the index with disk first and prints one JSON object
to stdout; progress and warnings go to stderr.
.atlas.json is looked up upwards from the bin/atlas launcher.

Exit codes: 0 success, 1 error, 2 no config or index,
3 symbol not found, 4 ambiguous name.

More on a command: atlas <command> help, -h or --help
"""


class _Parser(argparse.ArgumentParser):
    """Ошибки аргументов не печатают usage с кодом 2: их показывает ``main``."""

    def error(self, message: str) -> NoReturn:
        raise AtlasError(f"{self.prog}: {message}", usage=self.prog)


def _overview() -> str:
    width = max(len(name) for name in _COMMANDS)
    lines = [
        f"  {name:<{width}}  {summary}" for name, (summary, _) in _COMMANDS.items()
    ]
    return _OVERVIEW.format(commands="\n".join(lines))


def main(
    argv: Sequence[str] | None = None,
    *,
    launcher: Path | None = None,
    environ: Mapping[str, str] | None = None,
) -> int:
    """Точка входа; возвращает код выхода.

    ``launcher`` — путь пускового файла, от которого ищется ``.atlas.json``; по умолчанию
    ``sys.argv[0]``. Тесты передают его явно. Ошибки в терминале печатаются текстом со
    справкой, а в скрипте или у агента — JSON-объектом.

    Returns:
        Код завершения команды.

    """
    arguments = list(sys.argv[1:] if argv is None else argv)
    rest = _without_common(arguments)
    positional = [item for item in rest if not item.startswith("-")]
    command = positional[0] if positional else ""
    if not rest and not arguments:
        sys.stdout.write(_overview())
        return int(ExitCode.OK)
    if command == "help" or (not positional and {"-h", "--help"} & set(rest)):
        return _help(positional[1] if len(positional) > 1 else None)
    # WHY: «atlas query help» — то же, что «atlas query -h», во всех командах. Только когда
    # help — единственное слово после команды: «atlas query help me» ищет, а не справку.
    if positional[1:] == [status_parts.HELP] and command in _COMMANDS:
        return _help(command)
    # WHY: команда, которой нечего делать без входа, без аргументов показывает справку;
    # команда-действие (status, sync, unresolved) выполняется.
    if command in _NEEDS_INPUT and rest == [command]:
        return _help(command)
    try:
        _validate_command(command)
        options = _parser().parse_args(arguments)
        command = options.command
        output = _output(options)
        if command == "status":
            parts, wants_help = status_parts.requested(options.part)
            if wants_help:
                sys.stdout.write(status_parts.help_text(parts))
                return int(ExitCode.OK)
        result = _run(options, launcher or Path(sys.argv[0]), environ)
    except SystemExit as exit_request:
        # WHY: argparse печатает справку по «-h» и выходит через SystemExit; это успех.
        return int(exit_request.code or 0)
    except AtlasError as error:
        if sys.stdout.isatty() and "usage" in error.details:
            message = (
                str(error) if str(error).startswith("atlas") else f"atlas: {error}"
            )
            sys.stderr.write(f"{message}\n\n{_usage_hint(str(error.details['usage']))}")
            return int(error.code)
        _emit({"error": str(error), **error.details})
        return int(error.code)
    if output == "path":
        _emit_paths(result)
        return int(ExitCode.OK)
    _emit(result, pretty=output == "pretty")
    return int(ExitCode.OK)


def _output(options: argparse.Namespace) -> str:
    """Формат вывода; ``path`` — только у команд, чей ответ — список файлов.

    Returns:
        ``json``, ``pretty`` или ``path``.

    Raises:
        AtlasError: ``-o path`` у команды, чей ответ не список файлов.
    """
    output = str(getattr(options, "output", "json"))
    if output == "path" and options.command not in _PATH_OUTPUT:
        raise AtlasError(
            f"atlas {options.command}: -o path works only with "
            f"{' and '.join(sorted(_PATH_OUTPUT))}",
            usage=f"atlas {options.command}",
        )
    return output


def _without_common(arguments: Sequence[str]) -> list[str]:
    """Аргументы без общих опций и их значений: по остатку видно команду и её вход."""
    rest: list[str] = []
    skip = False
    for item in arguments:
        if skip:
            skip = False
        elif item in {"-o", "--output"}:
            skip = True
        elif not (
            item in {"-n", "--no-sync"}
            or item.startswith("--output=")
            or (item.startswith("-o") and len(item) > 2 and not item.startswith("--"))
        ):
            rest.append(item)
    return rest


def _emit_paths(result: Mapping[str, Any]) -> None:
    """Пути строками для пайпа; то, что строки не вмещают, уходит предупреждением в stderr."""
    sys.stdout.write("".join(f"{path}\n" for path in result.get("files", [])))
    meta = result.get("meta", {})
    if missing := meta.get("missing_words"):
        sys.stderr.write(f"atlas: no matches for: {', '.join(missing)}\n")
    if partial := meta.get("partial_files"):
        sys.stderr.write(
            f"atlas: {partial} more files match only parts of the name, not shown\n"
        )
    if unknown := result.get("unknown"):
        sys.stderr.write(f"atlas: not in the index: {', '.join(unknown)}\n")


def _help(command: str | None) -> int:
    if command is None:
        sys.stdout.write(_overview())
        return int(ExitCode.OK)
    if command not in _COMMANDS:
        sys.stderr.write(f"atlas: {_unknown_command(command)}\n\n{_overview()}")
        return int(ExitCode.ERROR)
    _subparsers()[command].print_help(sys.stdout)
    return int(ExitCode.OK)


def _unknown_command(command: str) -> str:
    if not command:
        return "no command given"
    close = difflib.get_close_matches(command, list(_COMMANDS), n=3, cutoff=0.6)
    close += [
        name for name in _COMMANDS if name.startswith(command) and name not in close
    ]
    hint = f" Did you mean {', '.join(close)}?" if close else ""
    return f"unknown command '{command}'.{hint}"


def _usage_hint(prog: str) -> str:
    name = prog.removeprefix("atlas").strip()
    if name in _COMMANDS:
        return _subparsers()[name].format_help()
    return _overview()


def _parser() -> _Parser:
    parser, _ = _build()
    return parser


def _subparsers() -> dict[str, _Parser]:
    _, subparsers = _build()
    return subparsers


def _add_help(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("-h", "--help", action="help", help="show this help")


def _add_common(parser: argparse.ArgumentParser) -> None:
    """Общие опции: и до команды, и после неё; значение по умолчанию не затирает заданное."""
    parser.add_argument(
        "-o",
        "--output",
        choices=_OUTPUTS,
        metavar="FORMAT",
        default=argparse.SUPPRESS,
        help="json (default, one line), pretty (indented) or path "
        "(file paths, one per line: query, affected)",
    )
    parser.add_argument(
        "-n",
        "--no-sync",
        action="store_true",
        default=argparse.SUPPRESS,
        help="answer from the current index without syncing",
    )


def _build() -> tuple[_Parser, dict[str, _Parser]]:
    parser = _Parser(
        prog="atlas",
        description=_overview(),
        formatter_class=_FORMATTER,
        add_help=False,
    )
    _add_help(parser)
    _add_common(parser)
    commands = parser.add_subparsers(
        dest="command", required=True, parser_class=_Parser
    )
    subparsers: dict[str, _Parser] = {}

    def add(name: str) -> _Parser:
        summary, examples = _COMMANDS[name]
        sub = commands.add_parser(
            name,
            prog=f"atlas {name}",
            help=summary,
            description=summary[:1].upper() + summary[1:] + ".",
            epilog=f"Examples:\n  {examples}"
            + (f"\n\n{_answer_help(_ANSWERS[name])}" if name in _ANSWERS else "")
            + (f"\n\n{status_parts.overview()}" if name == "status" else ""),
            formatter_class=_FORMATTER,
            add_help=False,
        )
        _add_help(sub)
        _add_common(sub)
        subparsers[name] = sub
        return sub

    explore_parser = add("explore")
    explore_parser.add_argument(
        "words",
        nargs="+",
        metavar="text",
        help="question; code names (Class, Class::method) become entry points",
    )
    explore_parser.add_argument(
        "-l",
        "--limit",
        type=int,
        default=12,
        metavar="N",
        help="how many files to show (12)",
    )
    explore_parser.add_argument(
        "--max-chars",
        type=int,
        default=30_000,
        metavar="CHARS",
        help="source characters in the answer at most (30000)",
    )

    query_parser = add("query")
    query_parser.add_argument(
        "words",
        nargs="+",
        metavar="text",
        help="query words; the whole phrase ranks higher",
    )
    query_parser.add_argument(
        "-l",
        "--limit",
        type=int,
        default=None,
        metavar="N",
        help="how many to return: records 1-50 (10); files with -o path 1-500 (50)",
    )
    query_parser.add_argument(
        "-k",
        "--kind",
        action="append",
        default=[],
        metavar="KIND",
        help="record kind, as in node: class, interface, trait, enum, type, function, method, "
        "file; section for a Markdown section; repeatable",
    )
    query_parser.add_argument(
        "-s",
        "--scope",
        action="append",
        default=[],
        metavar="SCOPE",
        help="scope: source, test, documentation, agent or a custom one from .atlas.json; "
        "repeatable (default: all built-in)",
    )

    status = add("status")
    status.add_argument(
        "part",
        nargs="*",
        help="keep only these parts, see Parts below; add help to explain their fields",
    )

    sync = add("sync")
    sync.add_argument("--full", action="store_true", help="rebuild the whole index")

    node = add("node")
    node.add_argument(
        "symbol", help="symbol or file: full name, Class::method, path, path:line"
    )
    node.add_argument(
        "-c", "--code", action="store_true", help="include the symbol source from disk"
    )
    node.add_argument(
        "-l",
        "--limit",
        type=int,
        default=30,
        metavar="N",
        help="how many edges of each kind to show (30)",
    )

    for name in ("incoming", "outgoing"):
        walk = add(name)
        walk.add_argument("symbol", help="symbol: full name, Class::method, path:line")
        walk.add_argument(
            "-d", "--depth", type=int, default=1, metavar="N", help="walk depth (1)"
        )
        walk.add_argument(
            "-l",
            "--limit",
            type=int,
            default=100,
            metavar="N",
            help="how many nodes to return (100)",
        )

    affected_parser = add("affected")
    affected_parser.add_argument(
        "paths",
        nargs="*",
        metavar="file",
        help="changed files relative to the project root; - reads them from stdin, "
        "one per line",
    )
    affected_parser.add_argument(
        "-d",
        "--depth",
        type=int,
        default=5,
        metavar="N",
        help="dependency walk depth (5)",
    )
    affected_parser.add_argument(
        "-g",
        "--glob",
        metavar="GLOB",
        help="test path glob instead of the test scope, e.g. 'tests/Unit/*'",
    )

    unresolved = add("unresolved")
    unresolved.add_argument(
        "-p", "--path", metavar="PATH", help="only this file or directory"
    )
    unresolved.add_argument(
        "--status",
        action="append",
        choices=["unresolved", "ambiguous", "external"],
        metavar="STATUS",
        help="unresolved, ambiguous or external; repeatable "
        "(default: unresolved and ambiguous)",
    )
    unresolved.add_argument(
        "-l",
        "--limit",
        type=int,
        default=50,
        metavar="N",
        help="how many references to return (50)",
    )

    add("schema")

    init = add("init")
    init.add_argument("directory", type=Path, help="project directory")

    add("help").add_argument("topic", nargs="?", help="command")
    return parser, subparsers


def _run(
    options: argparse.Namespace, launcher: Path, environ: Mapping[str, str] | None
) -> dict[str, Any]:
    if options.command == "schema":
        return {"schema": json.loads(schema_text())}
    if options.command == "init":
        return _init(options.directory)
    if options.command not in _INDEX_COMMANDS:
        raise AtlasError(f"Unknown command: {options.command}")

    config = load(discover(launcher, environ))
    store = Store.open(config.database_path)
    try:
        report = _sync(config, store, options)
        # WHY: полное состояние синхронизации нужно человеку в ``status``; агенту в остальных
        # командах важно только, что часть файлов не попала в индекс и ответ по ним устарел.
        full = options.command == "status"
        index = _index_info(config, store, report) if full else _index_warning(report)
        envelope = {"index": index} if index else {}
        result = {**envelope, **_command(options, config, store)}
        if full and options.part:
            return status_parts.pick(result, status_parts.requested(options.part)[0])
        return result
    finally:
        store.close()


def _sync(config: Config, store: Store, options: argparse.Namespace) -> SyncReport:
    if getattr(options, "no_sync", False):
        return SyncReport(stale_files=stale_files(config, store))
    with write_lock(config) as locked:
        if not locked:
            sys.stderr.write(
                "atlas: another process is syncing the index, answering from the current one\n",
            )
            return SyncReport(stale_files=stale_files(config, store))
        return synchronize(
            config,
            store,
            resolver_factory=build_resolver,
            full=options.command == "sync" and options.full,
        )


def _command(
    options: argparse.Namespace, config: Config, store: Store
) -> dict[str, Any]:
    match options.command:
        case "explore":
            return explore.explore(
                store,
                config.root,
                " ".join(options.words),
                synonyms=config.synonyms,
                max_files=max(1, options.limit),
                budget=max(1000, options.max_chars),
            )
        case "affected":
            raw = [path for path in options.paths if path != "-"]
            if "-" in options.paths:
                raw += sys.stdin.read().splitlines()
            paths = _project_paths(config.root, raw)
            if not paths:
                raise AtlasError("No changed files given", usage="atlas affected")
            return query.affected(
                store, paths, depth=max(1, options.depth), pattern=options.glob
            )
        case "query":
            paths_only = getattr(options, "output", "json") == "path"
            default_limit = 50 if paths_only else 10
            return search.search(
                store,
                " ".join(options.words),
                synonyms=config.synonyms,
                limit=options.limit or default_limit,
                kinds=options.kind,
                scopes=options.scope,
                files_only=paths_only,
                custom_scopes=list(config.scopes),
            )
        case "status":
            return query.status(store)
        case "sync":
            return {}
        case "node":
            found = query.find(store, options.symbol)
            if 1 < len(found) <= query.MAX_INLINE_CANDIDATES:
                # WHY: несколько одноимённых символов показываются все сразу, без выбора
                # за агента: выбор угадыванием противоречит принципу, а ошибка стоит лишнего вызова.
                return {
                    "ambiguous": True,
                    "nodes": [
                        query.node_card(
                            store,
                            config.root,
                            node,
                            code=options.code,
                            limit=options.limit,
                        )
                        for node in found
                    ],
                }
            node = query.find_one(store, options.symbol)
            return {
                "node": query.node_card(
                    store, config.root, node, code=options.code, limit=options.limit
                )
            }
        case "incoming" | "outgoing":
            node = query.find_one(store, options.symbol)
            incoming = options.command == "incoming"
            traversed = query.traverse(
                store, node, incoming=incoming, depth=options.depth, limit=options.limit
            )
            symbol = query.summary(node)
            return {
                "symbol": {"name": symbol["name"], "file": symbol["file"]},
                options.command: traversed,
            }
        case "unresolved":
            statuses = options.status or ["unresolved", "ambiguous"]
            return query.unresolved(
                store, path=options.path, statuses=statuses, limit=options.limit
            )
        case _:
            raise AtlasError(f"Unknown command: {options.command}")


def _project_paths(root: Path, raw: Iterable[str]) -> list[str]:
    """Пути от корня проекта: абсолютные приводятся к корню, ``./`` снимается."""
    paths: list[str] = []
    for item in raw:
        text = item.strip()
        if not text:
            continue
        path = Path(text)
        if path.is_absolute():
            with contextlib.suppress(ValueError):
                text = path.resolve().relative_to(root.resolve()).as_posix()
        paths.append(text.removeprefix("./"))
    return list(dict.fromkeys(paths))


def _index_warning(report: SyncReport) -> dict[str, Any]:
    """Сведения о свежести, которые меняют чтение ответа; пусто, если индекс свежий."""
    warning: dict[str, Any] = {}
    if report.stale_files:
        warning["stale_files"] = list(report.stale_files)
    if report.full_rebuild:
        warning["full_rebuild"] = True
    return warning


def _index_info(config: Config, store: Store, report: SyncReport) -> dict[str, Any]:
    synced_at = store.get_meta("synced_at")
    info: dict[str, Any] = {
        "root": str(config.root),
        "schema_version": atlas.SCHEMA_VERSION,
        "synced_files": report.synced_files,
        "removed_files": report.removed_files,
        "stale_files": list(report.stale_files),
        "sync_ms": report.duration_ms,
    }
    if report.full_rebuild:
        info["full_rebuild"] = True
    if report.check:
        info["check"] = report.check
    if report.phases_ms:
        info["phases_ms"] = report.phases_ms
    if synced_at is not None:
        info["synced_at"] = time.strftime(
            "%Y-%m-%dT%H:%M:%SZ", time.gmtime(int(synced_at))
        )
    return info


_LAUNCHER_TEMPLATE = """#!/usr/bin/env -S uv run --quiet --script
# /// script
# requires-python = ">=3.14"
# dependencies = ["atlas"]
{sources}# ///
# Atlas launcher: .atlas.json is looked up upwards from this file, not from the current directory.
from atlas.cli import main

# WHY: parse workers on macOS start via spawn and re-import this file;
# without the guard every worker would run the whole command.
if __name__ == "__main__":
    raise SystemExit(main())
"""

_CONFIG_TEMPLATE = {
    "version": 1,
    "index": "var/cache/atlas",
    "paths": {"code": ["src"], "docs": ["docs", "README.md", "AGENTS.md"]},
}


def _init(directory: Path) -> dict[str, Any]:
    """Положить пусковой файл и шаблон конфигурации, не перезаписывая существующие."""
    launcher = directory / "bin" / "atlas"
    config = directory / CONFIG_NAME
    existing = [str(path) for path in (launcher, config) if path.exists()]
    if existing:
        raise AtlasError(
            "Files already exist, init does not overwrite them", existing=existing
        )
    source = _source_checkout()
    sources = (
        f'# [tool.uv.sources]\n# atlas = {{ path = "{source}", editable = true }}\n'
        if source
        else ""
    )
    launcher.parent.mkdir(parents=True, exist_ok=True)
    launcher.write_text(_LAUNCHER_TEMPLATE.format(sources=sources), encoding="utf-8")
    launcher.chmod(0o755)
    config.write_text(
        json.dumps(_CONFIG_TEMPLATE, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    return {"created": [str(launcher), str(config)]}


def _source_checkout() -> Path | None:
    """Каталог исходников Atlas, если пакет установлен из рабочей копии."""
    candidate = Path(atlas.__file__).resolve().parents[2]
    pyproject = candidate / "pyproject.toml"
    if pyproject.is_file() and 'name = "atlas"' in pyproject.read_text(
        encoding="utf-8"
    ):
        return candidate
    return None


def _emit(result: Mapping[str, Any], *, pretty: bool = False) -> None:
    indent = 2 if pretty else None
    separators = None if pretty else (",", ":")
    sys.stdout.write(
        json.dumps(result, ensure_ascii=False, indent=indent, separators=separators)
        + "\n"
    )


def build_resolver(
    nodes: Iterable[NodeRow], paths: set[str], inputs: ResolutionInputs
) -> Resolver:
    """Собрать граф объектов разрешения на готовом снимке данных."""
    index = NodeIndex(nodes)
    return Resolver(
        index,
        PhpResolver(index),
        EcmaResolver(index, ModuleResolver(paths, inputs.configs)),
        PythonResolver(index, PythonModules(paths)),
        GoResolver(index, GoModules(paths, inputs.configs)),
    )


def _validate_command(command: str) -> None:
    if command not in _COMMANDS:
        raise AtlasError(_unknown_command(command), usage="atlas")
