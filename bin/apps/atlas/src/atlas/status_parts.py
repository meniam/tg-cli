"""Части ответа ``status``: выбор части и текстовая справка по её полям.

Часть — группа ответа целиком (``file``) или разбивка внутри группы (``kind``).
``atlas status file`` отдаёт только её, ``atlas status file help`` объясняет её поля.
"""

from __future__ import annotations

import textwrap
from dataclasses import dataclass
from typing import Any

from atlas.errors import AtlasError

HELP = "help"
_WIDTH = 88


@dataclass(frozen=True, slots=True)
class Part:
    """Часть ответа: путь до значения, строка обзора и поля для справки."""

    path: tuple[str, ...]
    summary: str
    fields: tuple[tuple[str, str], ...]


# WHY: справка на английском, как всё, что видит агент через CLI.
PARTS: dict[str, Part] = {
    "index": Part(
        ("index",),
        "sync state: root, format version, what was reindexed and how fast",
        (
            ("root", "project root; .atlas.json is looked up upwards from bin/atlas"),
            ("schema_version", "JSON answer format version, for scripts"),
            ("synced_files", "files reindexed before this answer"),
            ("removed_files", "files dropped from the index before this answer"),
            (
                "stale_files",
                "files changed since the last sync; answers on them are stale",
            ),
            ("sync_ms", "time spent syncing before the answer"),
            ("check", "how changes were found: git (fast) or full (every file)"),
            ("phases_ms", "time per sync phase; shown when something was rebuilt"),
            ("synced_at", "last sync time, UTC"),
            ("full_rebuild", "true when the answer followed a full rebuild"),
        ),
    ),
    "file": Part(
        ("file",),
        "indexed files by language, codebase and scope; generated and broken files",
        (
            (
                "language",
                "files per language: php, typescript, tsx, python, go, markdown...",
            ),
            (
                "codebase",
                "files per codebase: a directory with composer.json, "
                "package.json or pyproject.toml; '.' is the project root",
            ),
            (
                "scope",
                "files per scope: source, test, documentation, agent or a custom "
                "one from .atlas.json; the values of query --scope",
            ),
            ("generated", "files recognized as generated code"),
            ("parse_errors", "files that failed to parse; their graph is incomplete"),
        ),
    ),
    "graph": Part(
        ("graph",),
        "code graph size and quality: symbols, links, share of guesses, coverage",
        (
            ("node", "symbols per kind: class, method, function, route..."),
            ("edge", "links per kind: calls, contains, extends, instantiates..."),
            (
                "provenance",
                "links per origin: syntax (from the parse tree), resolved "
                "(proven by the resolver), heuristic (a guess)",
            ),
            (
                "coverage",
                "per language: files with symbols, files that another file "
                "depends on, their share; guesses do not count",
            ),
        ),
    ),
    "ref": Part(
        ("ref",),
        "references by resolution status: how many links are lost or ambiguous",
        (
            (
                "status",
                "references per status: resolved, external (outside the "
                "project), ambiguous (several candidates), unresolved",
            ),
            (
                "resolved_share",
                "share of resolved references; shown when there are any",
            ),
        ),
    ),
    "search": Part(
        ("search",),
        "full-text search records",
        (
            ("records", "search records: a file, class, method or doc section"),
            ("kind", "records per kind; the values of query --kind"),
        ),
    ),
    "framework": Part(
        ("framework",),
        "routes and the frontend HTTP requests matched to them",
        (
            ("routes", "routes found in Symfony, API Platform and Next.js"),
            ("route_edges", "links from routes to their handlers"),
            ("requests", "frontend HTTP requests matched to exactly one route"),
            ("guessed_requests", "requests linked to a route by a guess"),
            ("ambiguous_requests", "requests that fit several routes equally"),
            ("unmatched_requests", "requests with no matching route"),
        ),
    ),
    "language": Part(
        ("file", "language"),
        "files per language",
        (("<language>", "number of indexed files in that language"),),
    ),
    "codebase": Part(
        ("file", "codebase"),
        "files per codebase",
        (
            (
                "<codebase>",
                "number of files under a directory with composer.json, "
                "package.json or pyproject.toml; '.' is the project root",
            ),
        ),
    ),
    "scope": Part(
        ("file", "scope"),
        "files per scope; the values of query --scope",
        (
            ("source", "project code"),
            ("test", "tests"),
            ("documentation", "docs from paths.docs in .atlas.json"),
            ("agent", "agent rules, skills and instructions: AGENTS.md, .agents/..."),
            ("<custom>", "a scope from paths.scopes in .atlas.json, e.g. migration"),
        ),
    ),
    "node": Part(
        ("graph", "node"),
        "graph symbols per kind",
        (("<kind>", "number of symbols: file, class, method, function, route..."),),
    ),
    "edge": Part(
        ("graph", "edge"),
        "graph links per kind",
        (
            ("calls", "a function or method calls another"),
            ("contains", "a file or class contains a symbol"),
            ("extends, implements, uses_trait", "inheritance"),
            ("instantiates", "creates an object of a class"),
            ("references", "mentions a symbol without calling it"),
            ("imports", "imports a module or symbol"),
            ("renders", "a component renders another"),
            ("routes_to", "a route leads to its handler"),
            ("requests", "a frontend function requests a route"),
        ),
    ),
    "provenance": Part(
        ("graph", "provenance"),
        "graph links per origin",
        (
            ("syntax", "taken from the parse tree, always true"),
            ("resolved", "proven by the resolver"),
            ("heuristic", "a guess; answers mark such links with guess"),
        ),
    ),
    "coverage": Part(
        ("graph", "coverage"),
        "graph coverage per language",
        (
            ("files", "source files with symbols, generated ones excluded"),
            ("covered", "files that another file depends on by a proven link"),
            ("share", "covered / files; an uncovered file is an entry point or a gap"),
        ),
    ),
    "kind": Part(
        ("search", "kind"),
        "search records per kind; the values of query --kind",
        (
            ("class, interface, trait, enum, type", "a type, as in node"),
            ("function, method", "a function or method"),
            ("file", "the head of a code file"),
            ("section", "a Markdown section: docs or agent rules, see file.scope"),
        ),
    ),
}


def requested(words: list[str]) -> tuple[list[str], bool]:
    """Части из аргументов ``status`` и признак справки; неизвестная часть — ошибка."""
    parts = [word for word in words if word != HELP]
    unknown = [part for part in parts if part not in PARTS]
    if unknown:
        raise AtlasError(
            f"Unknown status part {unknown[0]}.", allowed_parts=list(PARTS)
        )
    return list(dict.fromkeys(parts)), HELP in words


def pick(result: dict[str, Any], parts: list[str]) -> dict[str, Any]:
    """Только выбранные части; одна часть — без обёртки, её имя уже в команде."""
    picked: dict[str, Any] = {}
    for part in parts:
        value: Any = result
        for key in PARTS[part].path:
            value = value.get(key, {}) if isinstance(value, dict) else {}
        picked[part] = value
    if len(picked) == 1:
        return dict(next(iter(picked.values())))
    return picked


def overview() -> str:
    """Список частей для ``atlas status -h``: группы и разбивки внутри них."""
    groups = [name for name, part in PARTS.items() if len(part.path) == 1]
    breakdowns = [name for name in PARTS if name not in groups]
    width = max(map(len, PARTS))
    lines = [
        "Parts: name one or more to keep only them; add help to explain their fields.",
        "",
        "Groups:",
        *_rows([(name, PARTS[name].summary) for name in groups], width),
        "",
        "Breakdowns inside groups:",
        *_rows([(name, PARTS[name].summary) for name in breakdowns], width),
    ]
    return "\n".join(lines)


def help_text(parts: list[str]) -> str:
    """Справка по полям названных частей; без частей — список частей."""
    if not parts:
        return overview() + "\n"
    blocks = []
    for name in parts:
        part = PARTS[name]
        path = ".".join(part.path)
        lines = [f"atlas status {name}: {part.summary}"]
        if path != name:
            lines.append(f"  (the {path} field of the full answer)")
        lines.append("")
        lines += _rows(list(part.fields), max(len(field) for field, _ in part.fields))
        blocks.append("\n".join(lines))
    return "\n\n".join(blocks) + "\n"


def _rows(rows: list[tuple[str, str]], width: int) -> list[str]:
    """Строки «имя  описание» с описанием, перенесённым под свою колонку."""
    lines: list[str] = []
    for name, about in rows:
        indent = " " * (width + 4)
        wrapped = textwrap.wrap(
            about, _WIDTH, initial_indent=indent, subsequent_indent=indent
        )
        lines.append(f"  {name:<{width}}  {wrapped[0].lstrip()}")
        lines += wrapped[1:]
    return lines
