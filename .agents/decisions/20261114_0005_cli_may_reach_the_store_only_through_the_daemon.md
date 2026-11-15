---
decided: 2026-11-14
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: no
revises: [20261113_2113_cli_imports_daemon_paths_and_main_injects_telegram]
paths: [internal/cli/cli_test.go, internal/daemon/daemon.go]
---
# cli зависит от store только через daemon, прямой импорт запрещён

## Context

[Решение о границе cli и daemon](20261113_2113_cli_imports_daemon_paths_and_main_injects_telegram.md)
запрещало `internal/cli` зависеть от `tgclient`, `store` и gotd. Тест
`go list -deps` проверял всё дерево зависимостей. С [хранилищем](20261113_2230_storage_is_one_sqlite_per_profile_with_slow_opt_in_history.md)
`internal/daemon` стал импортировать `internal/store` (Daemon открывает базу и
обслуживает пометки и поиск), а `cli` по-прежнему импортирует `daemon` ради
путей профиля и запуска, поэтому `store` попал в дерево зависимостей `cli`.

## Decision

- `internal/cli` не импортирует `internal/store` напрямую: тест смотрит
  прямые импорты (`go list -f '{{join .Imports "\n"}}'`).
- Транзитивный запрет на `internal/tgclient` и `github.com/gotd/td` остаётся
  (`go list -deps`).
- В базу пишет только Daemon, CLI базу не открывает и ходит к ней по RPC.

## Why

- Смысл запрета в том, что CLI не открывает базу и не создаёт Telegram-клиент;
  транзитивная зависимость от пакета `store` этого не нарушает: она есть
  потому, что `daemon` в том же бинаре.
- Отвергнуто: вынести пути профиля и запуск Daemon в отдельный пакет, чтобы
  `cli` не тянул `daemon`. Пакет из нескольких функций ради теста, а граница
  по смыслу уже проверяется прямым импортом.
- Отвергнуто: оставить транзитивный запрет на `store`. Тест упал бы без
  нарушения замысла.

## Cost

- Граница `cli` и `store` теперь держится на тесте прямого импорта и ревью:
  кто-то может открыть базу из `cli` через вспомогательный пакет. Если это
  случится, правило ужесточается отдельным решением.
