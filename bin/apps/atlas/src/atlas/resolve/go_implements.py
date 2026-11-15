"""Рёбра ``implements`` для Go: тип реализует интерфейс, если у него есть все его методы.

В Go реализация неявна, в коде нет строки ``implements``, которую можно разобрать.
Совпадение считается по именам методов без сравнения сигнатур, поэтому ребро помечено
``heuristic`` и пересчитывается на каждой синхронизации целиком.

Чтобы не плодить ложные связи, берутся только интерфейсы из двух и более методов
(``Close``, ``String`` и ``Error`` есть у половины типов), и только те, чей набор методов
известен полностью: интерфейс со встроенным внешним интерфейсом пропускается.
Неэкспортируемые методы требуют того же пакета. Типы из тестов не считаются
реализациями: фейки не должны превращать единственную реализацию во множество.
"""

import posixpath
from collections import defaultdict
from typing import TYPE_CHECKING

from atlas.model import EdgeKind, Provenance

if TYPE_CHECKING:
    from atlas.records import NodeRow
    from atlas.store.db import Store

VIA = "go.structural-match"
MIN_METHODS = 2
_MAX_DEPTH = 16
_CONFIDENCE = 0.6

# Ключ пакета: каталог и имя пакета; тип и его методы всегда в одном пакете.
type _Package = tuple[str, str]


def rebuild(store: Store) -> int:
    """Пересчитать рёбра ``implements`` для Go; вернуть их число."""
    store.delete_edges_by_via(VIA)
    nodes = store.nodes_of_language("go")
    if not nodes:
        return 0
    scopes = dict(
        store.execute("SELECT id, scope FROM files WHERE language = 'go'").fetchall()
    )
    embedded = _embedded(store)
    packages = {
        n.path: str(n.meta.get("package", "")) for n in nodes if n.kind == "file"
    }

    def package_of(node: NodeRow) -> _Package:
        return posixpath.dirname(node.path), packages.get(node.path, "")

    own: dict[tuple[_Package, str], set[str]] = defaultdict(set)
    for node in nodes:
        if node.kind == "method":
            owner = node.qualified_name.split(".", 1)[0]
            own[(package_of(node), owner)].add(node.name)

    by_id = {n.id: n for n in nodes}

    def methods_of(node: NodeRow, depth: int = 0) -> set[str]:
        found = set(own.get((package_of(node), node.qualified_name), ()))
        if depth >= _MAX_DEPTH:
            return found
        for target_id in embedded.get(node.id, ()):
            target = by_id.get(target_id)
            if target is not None:
                found |= methods_of(target, depth + 1)
        return found

    interfaces: list[tuple[NodeRow, frozenset[str]]] = []
    for node in nodes:
        if node.kind != "interface" or _has_open_embedding(node, embedded):
            continue
        names = frozenset(methods_of(node))
        if len(names) >= MIN_METHODS:
            interfaces.append((node, names))
    if not interfaces:
        return 0

    implementers: list[tuple[NodeRow, set[str]]] = []
    by_method: dict[str, set[int]] = defaultdict(set)
    for node in nodes:
        if node.kind not in {"class", "type"} or node.meta.get("alias"):
            continue
        if scopes.get(node.file_id) == "test":
            continue
        have = methods_of(node)
        if len(have) < MIN_METHODS:
            continue
        implementers.append((node, have))
        for name in have:
            by_method[name].add(len(implementers) - 1)

    edges: list[tuple[object, ...]] = []
    for interface, names in interfaces:
        rarest = min(names, key=lambda name: len(by_method.get(name, ())))
        local = any(not name[:1].isupper() for name in names)
        for index in sorted(by_method.get(rarest, ())):
            node, have = implementers[index]
            if node.codebase != interface.codebase or not names <= have:
                continue
            if local and package_of(node) != package_of(interface):
                continue
            edges.append(
                (
                    node.id,
                    interface.id,
                    EdgeKind.IMPLEMENTS,
                    node.start_line,
                    0,
                    Provenance.HEURISTIC,
                    _CONFIDENCE,
                    VIA,
                    node.file_id,
                )
            )
    store.insert_edge_rows(edges)
    return len(edges)


def _embedded(store: Store) -> dict[int, list[int]]:
    """Встроенные типы: источник и разрешённые цели рёбер ``extends`` между узлами Go."""
    result: dict[int, list[int]] = defaultdict(list)
    for source, target in store.execute(
        "SELECT e.source, e.target FROM edges e JOIN nodes n ON n.id = e.source "
        "WHERE e.kind = 'extends' AND n.language = 'go'"
    ):
        result[int(source)].append(int(target))
    return result


def _has_open_embedding(node: NodeRow, embedded: dict[int, list[int]]) -> bool:
    """У интерфейса есть встроенный тип, который не найден в проекте."""
    return len(node.meta.get("extends", [])) > len(embedded.get(node.id, ()))
