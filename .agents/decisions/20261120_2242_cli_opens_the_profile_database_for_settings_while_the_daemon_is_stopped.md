---
decided: 2026-11-20
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
revises: [20261114_0005_cli_may_reach_the_store_only_through_the_daemon, 20261116_0017_config_toml_holds_paths_file_names_behavior_and_profile_settings]
paths: [internal/cli/daemon_cmd.go, internal/cli/cli_test.go, internal/daemon/db_binding.go]
---
# При остановленном Daemon tg settings открывает базу профиля сам, через функцию пакета daemon

## Context

Решение [cli зависит от store только через daemon](20261114_0005_cli_may_reach_the_store_only_through_the_daemon.md)
говорит: «В базу пишет только Daemon, CLI базу не открывает». Когда настройки
лежат в базе, сломанный прокси не даёт Daemon подключиться, и починить его
через Daemon нельзя. `daemon start` сегодня сам читает `[profiles.<имя>]` из
`config.toml` в процессе CLI и передаёт значения в `daemon.Run`.

## Decision

- Daemon отвечает на сокете: `tg settings` идёт через JSON-RPC.
- Daemon не отвечает: `tg settings` вызывает функцию пакета `daemon`, которая
  берёт замок профиля и `db.lock`, проверяет привязку базы (`data-dir`),
  открывает базу и выполняет то же действие. `internal/cli` по-прежнему не
  импортирует `internal/store`.
- Daemon читает настройки профиля из базы при старте сам.
- `daemon start --client`, `--device`, `--transport`, `--proxy` и
  `TG_CLI_PROXY` остаются: они перекрывают значение из базы и записываются в
  неё, как раньше записывались в `config.toml`.

## Why

- Так решил владелец: настройки должны чиниться без работающего Daemon.
- Функция в `daemon`, а не прямой импорт `store` в `cli`: граница пакетов
  сохраняется, а замки и проверка привязки базы живут в одном месте.
- Отвергнуто: только через Daemon. Сломанный прокси или клиент не дал бы
  запустить Daemon и исправить себя.
- Отвергнуто: аварийный файл рядом с базой. Второй источник истины.

## Cost

- Фраза «CLI никогда не открывает базу» в `docs/cli/overview.md` и в решении
  о границе больше не верна: CLI открывает её для настроек при остановленном
  Daemon.
- Пока `tg settings` держит `db.lock`, `daemon start` получает отказ «база
  занята» (код 1).
- Открытие базы новым бинарником применяет миграции схемы и без Daemon.
