"""Рёбра от вызова метода интерфейса к его единственной реализации.

Вызов через интерфейс статически ведёт только на объявление. Если в проекте ровно
один класс реализует интерфейс и определяет метод, добавляется догадка ``calls`` на
реализацию: она помечена ``heuristic`` и пересчитывается на каждой синхронизации целиком.
"""

import posixpath
from typing import TYPE_CHECKING, Any

from atlas.model import EdgeKind, Provenance

if TYPE_CHECKING:
    from atlas.store.db import Store

VIA = "names.single-implementation"


def rebuild(store: Store) -> int:
    """Пересчитать рёбра правила; вернуть их число."""
    store.delete_edges_by_via(VIA)
    rows = store.execute(
        """
        SELECT e.source, e.line, e.col, e.origin_file, m.name, i.id, i.language
        FROM edges e
        JOIN nodes m ON m.id = e.target AND m.kind = 'method'
        JOIN edges c ON c.target = m.id AND c.kind = 'contains'
        JOIN nodes i ON i.id = c.source AND i.kind = 'interface'
        WHERE e.kind = 'calls'
        """
    ).fetchall()
    implementations: dict[tuple[int, str], int | None] = {}
    edges: list[tuple[object, ...]] = []
    for source, line, col, origin_file, method, interface_id, language in rows:
        key = (int(interface_id), str(method))
        if key not in implementations:
            implementations[key] = _single_implementation(
                store, key[0], key[1], str(language)
            )
        target = implementations[key]
        if target is None:
            continue
        edges.append(
            (
                source,
                target,
                EdgeKind.CALLS,
                line,
                col,
                Provenance.HEURISTIC,
                0.6,
                VIA,
                origin_file,
            )
        )
    store.insert_edge_rows(edges)
    return len(edges)


def _single_implementation(
    store: Store, interface_id: int, method: str, language: str
) -> int | None:
    found = implementing_methods(store, interface_id, method, language)
    return found[0][0] if len(found) == 1 else None


def implementing_methods(
    store: Store, interface_id: int, method: str, language: str
) -> list[tuple[int, str, int, int]]:
    """Методы ``method`` у классов, реализующих интерфейс.

    Returns:
        Кортежи ``(id, путь, первая строка, последняя строка)`` по пути и строке.
    """
    separator = "::" if language == "php" else "."
    classes = store.execute(
        "SELECT n.id, n.qualified_name, n.path, n.codebase FROM edges e "
        "JOIN nodes n ON n.id = e.source "
        "WHERE e.target = ? AND e.kind = 'implements' AND n.kind = 'class'",
        (interface_id,),
    ).fetchall()
    found: list[tuple[int, str, int, int]] = []
    for class_id, qualified_name, path, codebase in classes:
        qualified = f"{qualified_name}{separator}{method}"
        if language == "go":
            row = _go_method(store, qualified, path, codebase)
        else:
            row = store.execute(
                "SELECT n.id, n.path, n.start_line, n.end_line FROM edges e "
                "JOIN nodes n ON n.id = e.target "
                "WHERE e.source = ? AND e.kind = 'contains' AND n.kind = 'method' "
                "AND lower(n.qualified_name) = lower(?)",
                (class_id, qualified),
            ).fetchone()
        if row is not None:
            found.append((int(row[0]), str(row[1]), int(row[2]), int(row[3])))
    return sorted(found, key=lambda item: (item[1], item[2]))


def _go_method(
    store: Store, qualified: str, class_path: str, codebase: str
) -> tuple[Any, ...] | None:
    """Метод Go лежит в любом файле пакета типа, а не только в файле типа."""
    directory = posixpath.dirname(class_path)
    for row in store.execute(
        "SELECT id, path, start_line, end_line FROM nodes "
        "WHERE kind = 'method' AND language = 'go' AND codebase = ? "
        "AND lower(qualified_name) = lower(?)",
        (codebase, qualified),
    ):
        if posixpath.dirname(row[1]) == directory:
            return tuple(row)
    return None
