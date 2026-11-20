---
decided: 2026-11-20
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
revises: [20261116_0017_config_toml_holds_paths_file_names_behavior_and_profile_settings, 20261116_0123_profile_settings_are_written_only_into_a_separate_table, 20261116_0136_databases_live_in_db_next_to_the_config, 20261120_1651_background_history_sync_runs_in_a_daily_window_and_is_off_by_default, 20261120_1850_vectors_live_in_vectors_db_and_bge_m3_embeds_through_ollama]
paths: [internal/config/config.go, internal/config/template.toml, internal/cli/config_cmd.go, internal/cli/schema_cmd.go]
---
# Настройки лежат в базе профиля, в config.toml остаётся только то, без чего базу не найти

## Context

Решение [Пути, имена файлов, поведение и настройки профиля лежат в config.toml](20261116_0017_config_toml_holds_paths_file_names_behavior_and_profile_settings.md)
собрало настройки в `config.toml`: `home`, `default_profile`, `[files]`,
`[behavior]`, `[profiles.<имя>]`; позже туда добавились `[background_sync]` и
`[semantic_search]`. Списки доступа лежат отдельным файлом
`<home>/<профиль>/access.json`. Владелец 2026-11-20: «Хочу все настройки хранить
в базе данных и управлять ими через tg settings».

## Decision

- В базе профиля `db/<профиль>/<files.database>` хранятся: `[behavior]`
  (`throttle_percent`, `flood_autowait`, `expect_user`), `[background_sync]`,
  `[semantic_search]`, настройки профиля (`client`, `device`, `transport`,
  `proxy`) и все поля `access.json` (`allow`, `block`, `write`, `admin`,
  `dialogs`, `sessions`, `create`, `profile`, `files`, `downloads`).
- В `config.toml` остаются `home`, `default_profile` и `[files]`. Ключа
  `db_dir` нет: корень баз вычисляется из пути конфига, см.
  [Корень баз это каталог конфига плюс db](20261116_0327_database_root_is_the_config_directory_plus_db.md).
- База каждого профиля хранит свои настройки. Значения, общие для всех
  профилей в старом `config.toml`, при переносе попадают в базу каждого профиля.
- Схема таблиц, форма ключей и миграция схемы описаны в плане
  `20261120_2242_settings_in_database`; номер миграции схемы берётся следующий
  свободный (на 2026-11-20 это `0012`).

## Why

- Так решил владелец: одно место для настроек, которые меняет команда.
- Отвергнуто: оставить настройки в `config.toml` и править его командой. Тогда
  нужна запись чужого TOML с сохранением комментариев для каждой секции, а
  списки доступа всё равно лежат вторым файлом.
- `home`, `default_profile` и `[files]` остаются в файле: без них нельзя найти
  базу, из которой читаются остальные настройки.

## Cost

- Значение, общее раньше для всех профилей (дроссель, окно загрузки), теперь
  задаётся в каждом профиле отдельно.
- Владелец больше не правит настройки редактором: только `tg settings`.
- Старый бинарник не видит перенесённых значений. Откат описан в
  [переносе настроек](20261120_2242_settings_move_from_config_toml_and_access_json_once_in_one_transaction.md).
- Правятся `docs/cli/config.md`, `docs/cli/overview.md`, шаблон
  `internal/config/template.toml`, блок `config` в `tg schema`, `FEATURES.md` 1.14.
