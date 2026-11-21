# Пошаговый план: настройки в базе профиля и команда tg settings

ID: 20261120_2242_settings_in_database
Created: 2026-11-20
Owner: Eugene Myazin
Branch: `feature/20261120_2242_settings_in_database`
PR: —

## Status

- Current status: in_progress (2026-11-21: ветка и worktree `../tg-cli-settings`
  созданы, coder взял Э1; 2026-11-20 владелец ответил на Q1-Q13)
- Last update: 2026-11-21
- Next safe step: coder выполняет Э1 (реестр настроек, `internal/config/settings.go`),
  менеджер проверяет `just check` и diff. Параллельно и без блокировки Э1: менеджер согласует с владельцем разведение
  с `web_viewer` и текст для `AGENTS.md` (раздел «Предложения менеджеру»); оба
  нужны до Э8.
- Восемь строк Decision log с `Confirmed: no` (реестр в `internal/config`,
  `expect_user` из базы в Daemon, коды отказа по фразе, ограничение подбора,
  ключ `[files] access`, цепочка старых файлов профиля, `settings.*` без
  Telegram, журнал записи) владелец не отклонил; они действуют и ждут его вето.
- Этапы: Э1-Э9 готовы (2026-11-21; `background_sync.enabled` остался `restart`: останов запущенного задания фоновой загрузки не управляется; открыто для этапа Т: обрыв фиксации нескольких баз после первого коммита требует ручного продолжения; открыто для этапа Т и владельца: ограничение подбора фразы не хранится между офлайн-вызовами; `voice_transcription.*` остаются в config.toml и вне реестра); этап Т: тесты написаны, go test, -race и just check зелёные (2026-11-21), ревью безопасности идёт; порядок изменён: код Э4b-Э9, затем этап Т (тесты), см. «Порядок работы».

## Context and sources

Просьба владельца 2026-11-20: «Хочу все настройки хранить в базе данных, давай
это обсудим и управлять ими через tg settings». Обсуждение с менеджером закрыто,
владелец решил шесть пунктов; они записаны решениями (см. «Decision log»).

- Прочитано, документы: `docs/cli/config.md`, `docs/cli/access.md`,
  `docs/cli/overview.md` (разделы Configuration, Profiles), `docs/contracts.md`,
  `internal/config/template.toml`, `FEATURES.md` (1.13, 1.14, 9.2, 9.6),
  `AGENTS.md` (строки 25, 44, 51, 52, 87-89), план
  `.agents/plans/20261120_1959_web_viewer.md` (фаза 2, Decision log, Q8-Q10).
- Прочитано, решения: `20261116_0017`, `20261116_0123`, `20261116_0136`,
  `20261116_0327_database_is_bound...`, `20261116_0327_database_root...`,
  `20261113_2111`, `20261113_2113`, `20261114_0005`, `20261114_0032`,
  `20261114_0900`, `20261114_1100`, `20261114_2010_media...`,
  `20261120_1651`, `20261120_1850`, `20261120_2116`.
- Прочитано, код: `internal/config/config.go`, `write.go`, `migrate.go`
  (через Atlas), `internal/cli/daemon_cmd.go`, `network_flags.go`,
  `profile_settings.go`, `client.go`, `session_cmd.go:120-200`,
  `terminal.go:30-70`, `internal/daemon/daemon.go:1-330, 841-866`,
  `internal/daemon/access.go`, `internal/daemon/launch_unix.go:20-60`,
  `internal/daemon/background_sync_config.go`, сигнатуры `internal/access/access.go`
  и `internal/throttle/throttle.go`.
- Проверено командами: `bin/atlas explore`, `bin/atlas query --scope decision`,
  `rg` по `access\.json`, `config\.toml`, `TG_CLI_`, `Behavior\.`,
  `.Profiles`, `UpdateProfile`, `ExpectUser`; `ls internal/store/migrations`;
  `ls ~/.config/tg-cli` и заголовки секций `config.toml` владельца (без значений).
- Не проверено: содержимое `access.json` и значения `config.toml` владельца
  (секреты и списки не читались); можно ли менять планировщик фоновой загрузки
  на лету без перезапуска; стоимость argon2id на машине владельца; поведение
  `config.toml` с комментариями шаблона после удаления секций.

## Goal

Все настройки, кроме путей, лежат в базе профиля и меняются командой
`tg settings`. `config.toml` держит только `home`, `default_profile` и
`[files]`. Списки доступа меняет только человек, знающий парольную фразу
владельца. Настройки чинятся и при остановленном Daemon. Существующие
`config.toml` и `access.json` переносятся в базу один раз без потери правил.

## Scope

В объёме:

- реестр настроек (ключ, тип, умолчание, проверка, переменная, флаг, применение,
  защита, секретность);
- таблицы в `store.db` и миграция схемы `0012` (номер свободен на 2026-11-20);
- хэш парольной фразы владельца, её задание, смена, сброс, проверка в Daemon;
- политика доступа из базы вместо файла;
- методы JSON-RPC `settings.*` и офлайн-доступ через функцию пакета `daemon`;
- `daemon start` читает настройки из базы; флаги и `TG_CLI_PROXY` пишут в базу;
- применение на лету или `restart_required`;
- перенос `config.toml` (`[behavior]`, `[background_sync]`, `[semantic_search]`,
  `[profiles.*]`) и `access.json` в базу;
- команда `tg settings`, блок `settings` в `tg schema`, сокращённые
  `tg config show` и шаблон `config.toml`;
- документы, навык `docs/skill/tg`, `FEATURES.md`, `README.md`, записи changelog.

Вне объёма:

- веб-панель и передача фразы из браузера (план `20261120_1959_web_viewer`, см.
  Q13 и «Предложения менеджеру»);
- локальные настройки отдельного чата (режим фоновой загрузки, фаза 3 `web_viewer`);
- шифрование базы; защита от агента, который сознательно открывает SQLite
  напрямую;
- `tg settings export|import` (Q12, рекомендация «не сейчас»);
- правка `AGENTS.md` (только предложение текста).

Затронутые пакеты: `internal/config`, `internal/store`, `internal/access`,
`internal/daemon`, `internal/throttle`, `internal/rpc`, `internal/cli`;
документы `docs/cli/*`, `docs/contracts.md`, `docs/skill/tg/**`, `README.md`,
`FEATURES.md`, `bin/skill_check.py` (комментарий).

## Main invariant

1. Ни один чат, закрытый правилами `access.json` до переноса, не открывается
   после переноса; ни одна запись, разрешённая или запрещённая до переноса, не
   меняет статус. Тест: политика из файла и политика из базы после переноса
   дают одинаковые ответы `Allowed`, `WriteAllowed`, `AdminAllowed`,
   `FileRoots`, `DownloadRoots`, `*Allowed` на одном наборе id и путей.
2. Ошибка чтения настроек доступа из базы закрывает всё (`denied`), как
   сломанный файл сегодня.
3. Ни один путь записи `access.*` (CLI онлайн, CLI офлайн, прямой JSON-RPC)
   не проходит без верной фразы. Тест на каждый путь.
4. Значения настроек, действующие до переноса, действуют после: дроссель,
   окно загрузки, индекс, клиент, устройство, транспорт, прокси. Сверка
   `config show` до и `settings list` после на копии.
5. Приоритет флаг, переменная, база, умолчание сохраняется для каждого ключа,
   у которого сегодня есть флаг или переменная.
6. `internal/cli` не импортирует `internal/store` (тест прямых импортов).
7. Никаких живых проверок на аккаунте; перенос проверяется на копии файлов и
   базы без `session.json`.

## Risks and strategy

- Риск: перенос теряет правило и открывает закрытый чат. Стратегия: одна
  транзакция, сверка после записи, удаление файлов только после сверки,
  копия в `.config-migration-<время>`, тест-инвариант 1, прогон на копии
  боевых файлов.
- Риск: `access.json`, созданный заново после переноса (по старой привычке или
  по инструкции из чужого сообщения), импортируется без фразы. Стратегия: после
  первого переноса файл не импортируется никогда (Q8).
- Риск: агент подбирает фразу через сокет. Стратегия: argon2id, задержка после
  неудач в памяти Daemon (Decision log), минимальная длина фразы.
- Риск: агент с псевдотерминалом задаёт первую фразу раньше владельца.
  Стратегия: `token init` только пока хэша нет; владелец задаёт фразу сразу
  после деплоя (шаг деплоя), Q9.
- Риск: сломанный прокси в базе не даёт запустить Daemon. Стратегия: офлайн
  `tg settings set profile.proxy none` при остановленном Daemon.
- Риск: старый бинарник после отката видит пустые списки (всё открыто).
  Стратегия: миграция схемы `0012` не даёт старому бинарнику открыть базу
  (`store.Open` отказывает на базе новее), откат только из копий; шаги
  отката в «Deploy / rollback».
- Риск: значение из `TG_CLI_*` молча перекрывает записанное. Стратегия: ответ
  `get` и `set` показывает источник и перекрытие (Q5).
- Риск: правка дросселя на лету ломает очередь запросов. Стратегия: метод смены
  процента в `throttle.Limiter` под тем же мьютексом, тест с запросами в
  очереди; при сомнении ключ остаётся `restart_required`.

## Decision log

| Date | Decision | Reason | Confirmed |
| --- | --- | --- | --- |
| 2026-11-20 | [Настройки лежат в базе профиля, в config.toml остаётся только то, без чего базу не найти](../decisions/20261120_2242_settings_live_in_the_profile_database_and_config_toml_keeps_only_what_finds_it.md) | Решение владельца, пункт 1; `db_dir` не ключ, а производная | yes |
| 2026-11-20 | [Команда tg settings читает и пишет настройки, приоритет флаг, переменная, база, умолчание](../decisions/20261120_2242_tg_settings_reads_and_writes_settings_with_flag_env_database_default_priority.md) | Решение владельца, пункт 2 | yes |
| 2026-11-20 | [Списки доступа пишутся только с парольной фразой владельца, набранной в терминале](../decisions/20261120_2242_access_settings_are_written_only_with_the_owner_passphrase_typed_on_a_terminal.md) | Решение владельца, пункт 3 | yes |
| 2026-11-20 | [При остановленном Daemon tg settings открывает базу профиля сам, через функцию пакета daemon](../decisions/20261120_2242_cli_opens_the_profile_database_for_settings_while_the_daemon_is_stopped.md) | Решение владельца, пункт 4 | yes |
| 2026-11-20 | [Настройки из config.toml и access.json переносятся в базу один раз, одной транзакцией](../decisions/20261120_2242_settings_move_from_config_toml_and_access_json_once_in_one_transaction.md) | Решение владельца, пункт 5 | yes |
| 2026-11-20 | [Безопасные настройки применяются сразу, остальные помечаются restart_required](../decisions/20261120_2242_safe_settings_apply_at_once_and_the_rest_report_restart_required.md) | Решение владельца, пункт 6 | yes |
| 2026-11-20 | Реестр настроек живёт в `internal/config` (новый файл `settings.go`), а не в новом пакете | `cli` уже может импортировать `config`, строка импортов в `AGENTS.md` не меняется; проверки значений уже там | no |
| 2026-11-20 | Daemon подставляет `behavior.expect_user` из базы, когда запрос пришёл без `expect_user`; флаг и переменная по-прежнему идут в запросе от CLI | CLI без Daemon базу не читает, а лишний запрос `settings.get` перед каждой командой дороже | no |
| 2026-11-20 | Неверная или отсутствующая фраза: код 9 `write_disabled` с текстом `owner passphrase required` или `owner passphrase is wrong`; нет терминала: код 2 до обращения к Daemon | Отказ в праве записи уже значит код 9; отсутствие терминала это ошибка вызова | no |
| 2026-11-20 | После 5 неверных фраз подряд Daemon отвечает отказом без проверки 1 минуту, затем срок удваивается до 1 часа; счётчик в памяти, сбрасывается верной фразой; фраза от 12 символов, при задании вводится дважды | Подбор через сокет становится медленнее ручного ввода, человек не блокируется надолго | no |
| 2026-11-20 | Ключ `[files] access` остаётся: он называет файл, который переносится, и файл, наличие которого после переноса проверяет Q8 | Владелец мог переименовать файл; иначе перенос не нашёл бы его | no |
| 2026-11-20 | Цепочка старых файлов профиля (`client`, `device`, `transport`, `proxy`) сохраняется: сначала `config.MigrateProfiles` в `config.toml`, затем перенос в базу | Код и тесты уже есть; новый путь из файлов в базу дублировал бы его | no |
| 2026-11-20 | Методы `settings.*` не относятся к `accountMethod` и к `needsTelegram`: работают без соединения и после потери авторизации | Настройки чинят именно сломанное соединение | no |
| 2026-11-20 | Каждая запись настройки пишет в журнал Daemon ключ, источник (`rpc`, `offline`, `migration`, `daemon_start`) и для списков число добавленных и убранных элементов, без значений `access.*` и без прокси | След правок без утечки id чатов и паролей | no |
| 2026-11-20 | Q1: [Настройки лежат одной таблицей ключ-значение, хэш фразы владельца лежит отдельной таблицей](../decisions/20261120_2344_settings_are_one_key_value_table_and_the_passphrase_hash_has_its_own_table.md) | Ответ владельца, вариант A | yes |
| 2026-11-20 | Q2: [Ключи настроек имеют вид секция.ключ, списки меняются подкомандами add и remove](../decisions/20261120_2344_setting_keys_are_section_dot_key_and_lists_change_by_add_and_remove.md) | Ответ владельца, вариант A | yes |
| 2026-11-20 | Q3: секреты (`profile.proxy`) в `get` и `list` всегда скрыты, как `Redacted` в `config show`; показа нет | Ответ владельца, вариант A; цена: пароль прокси задают заново, а не читают | yes |
| 2026-11-20 | Q4: [tg settings показывает id чатов из allow и block только с фразой владельца](../decisions/20261120_2344_tg_settings_shows_chat_ids_of_allow_and_block_only_with_the_owner_passphrase.md) | Ответ владельца, вариант B | yes |
| 2026-11-20 | Q5: `get` отвечает действующим значением `value`, источником `source` (`flag`, `env`, `database`, `default`) и записанным `stored`; офлайн-ответ даёт `database` или `default` и пометку, что переменные применятся при старте | Ответ владельца, вариант A | yes |
| 2026-11-20 | Q6: перенос за один проход пишет общие секции в базы всех профилей (`db/*/` и `[profiles.*]`); работающий Daemon любого профиля даёт отказ кодом 1; секции удаляются из файла после всех баз | Ответ владельца, вариант A; у владельца один профиль | yes |
| 2026-11-20 | Q7: перенос идёт сам в `daemon start` (в родителе, под замками), в офлайн `tg settings` и в `tg config migrate` | Ответ владельца, вариант A; так же сделаны переносы каталога и базы | yes |
| 2026-11-20 | Q8: [access.json, появившийся после переноса, не импортируется, а Daemon с ним не стартует](../decisions/20261120_2344_access_json_that_appears_after_the_move_is_not_imported_and_stops_the_daemon.md) | Ответ владельца, вариант A, код 2 с путём файла | yes |
| 2026-11-20 | Q9: [Первую фразу владельца задаёт tg settings token init, пока хэша в базе нет](../decisions/20261120_2344_the_first_owner_passphrase_is_set_by_token_init_while_no_hash_exists.md) | Ответ владельца, вариант A плюс шаг деплоя | yes |
| 2026-11-20 | Q10: [Фраза владельца защищает только ключи access.*, остальные настройки пишутся без неё](../decisions/20261120_2344_the_owner_passphrase_guards_only_access_settings.md) | Ответ владельца, вариант A | yes |
| 2026-11-20 | Q11: [tg config show печатает только ключи файла config.toml](../decisions/20261120_2344_config_show_prints_only_the_keys_of_the_file.md) | Ответ владельца, вариант A; changelog `--breaking`, правка навыка `tg` | yes |
| 2026-11-20 | Q12: `tg settings export|import` не делается в этом плане; копия настроек это копия базы | Ответ владельца, вариант A | yes |
| 2026-11-20 | Q13: веб-панель показывает `access.*` только для чтения, пока план `web_viewer` не решит, как веб передаёт фразу | Ответ владельца, вариант C; решение о форме фразы в вебе остаётся за `web_viewer` | yes |

## Ключи и применение (Q1, Q2, Q3, Q4, Q5 решены 2026-11-20)

Форма ключа `секция.ключ` (Q2, вариант A). Секреты скрыты всегда (Q3).
`list` показывает числа для `access.allow` и `access.block`, id отдаёт только
`get` с фразой (Q4). Профиль выбирает `--profile`, в ключе
его нет. «Сразу» и «перезапуск» предварительные: этап Э4 подтверждает каждое
«сразу» тестом или переводит ключ в «перезапуск».

| Ключ | Тип | Умолчание | Переменная | Флаг | Применение | Защита |
| --- | --- | --- | --- | --- | --- | --- |
| `behavior.throttle_percent` | number | 60 | `TG_CLI_THROTTLE_PERCENT` | | сразу (нужен `Limiter.SetPercent`) | |
| `behavior.flood_autowait` | duration | 10s | `TG_CLI_FLOOD_AUTOWAIT` | | сразу | |
| `behavior.expect_user` | string | "" | `TG_CLI_EXPECT_USER` | `--expect-user` | сразу | |
| `background_sync.enabled` | bool | false | `TG_CLI_BACKGROUND_SYNC_ENABLED` | | сразу, если планировщик запускается и останавливается без перезапуска | |
| `background_sync.window_start`, `window_end`, `interval_min`, `interval_max`, `page_pause_min`, `page_pause_max` | string | как сейчас | `TG_CLI_BACKGROUND_SYNC_*` | | сразу | |
| `semantic_search.enabled`, `ollama_url`, `model`, `batch`, `interval` | как сейчас | как сейчас | `TG_CLI_SEMANTIC_SEARCH_*` | | перезапуск | |
| `semantic_search.min_similarity` | number | 0.5 | `TG_CLI_SEMANTIC_SEARCH_MIN_SIMILARITY` | `search --min-similarity` | сразу | |
| `profile.client` | string | "" | | `daemon start --client` | перезапуск | |
| `profile.device` | string | "" | | `daemon start --device` | перезапуск | |
| `profile.transport` | string | tcp | | `daemon start --transport` | перезапуск | |
| `profile.proxy` | string, секрет | none | `TG_CLI_PROXY` | `daemon start --proxy` | перезапуск | скрыт в выводе (Q3) |
| `access.allow`, `access.block`, `access.write`, `access.admin` | список id | [] | | | сразу | фраза |
| `access.files`, `access.downloads` | список путей | [] | | | сразу | фраза |
| `access.dialogs`, `access.sessions`, `access.create`, `access.profile` | bool | false | | | сразу | фраза |

Подкоманды (Q2, вариант A): `list [--prefix P]`, `get <key>`,
`set <key> <value>`, `unset <key>` (возврат к умолчанию), `add <key> <value>...`
и `remove <key> <value>...` (только ключи-списки), `token init|change|reset`.
Ответ `set`, `unset`, `add`, `remove`: `key`, `value` (новое, скрытое для
секрета), `previous` (так же), `applied` (`live`, `restart_required` или
`next_start` при остановленном Daemon), `overridden_by` (`env` или `flag`, если
действующее значение Daemon берёт не из базы).

## Порядок работы (изменён 2026-11-21)

Решение владельца 2026-11-21 (`Confirmed: yes`): сначала реализовать все этапы (Э4b-Э9), потом писать и запускать тесты. До этого момента coder не пишет новые тесты и не запускает `go test`; его проверка на этапе реализации это `go build ./...`, `go vet ./...`, `gofmt -l`. Тесты Э1-Э4a уже есть и остаются.

Цена решения: существующие тесты, которых коснётся код Э4b-Э9, будут красными до этапа Т, и ошибки этапов накопятся в одной точке. Этапы Э5-Э9 ссылаются на тесты в своих пунктах: эти пункты переходят в этап Т.

### Этап Т. Тесты и приёмка (после Э9)

- [ ] Починить существующие тесты, сломанные этапами Э4b-Э9, не ослабляя проверок.
- [ ] Написать тесты по пунктам «Тесты» этапов Э4b-Э9 и инварианты плана (политика из базы совпадает с файлом, повреждённое значение закрывает всё, перенос без потерь, повтор без изменений).
- [ ] `go test ./...`, `go test -race ./...`, `just check` зелёные.
- [ ] Ревью безопасности сильной моделью: перенос (Э6) и сквозная проверка фразы владельца (Э3-Э7), отчёты `review_e6.md`, `review_final.md`.
- [ ] Прогон на копии боевых файлов (пункты Step 1 и Э6), сверка с `baseline_*.json`.

## Step 0. Fix decisions before changes

- [x] Шесть решений владельца записаны файлами в `.agents/decisions/` (2026-11-20).
- [x] Владелец ответил на Q1-Q13 (2026-11-20); ответы записаны в «Open
  questions», семь получили файлы решений (Q1, Q2, Q4, Q8-Q11), остальные
  строки Decision log.
- [ ] Менеджер согласовал с владельцем разведение с `web_viewer` и правку его
  Decision log.
- [ ] Владелец согласовал или отклонил текст для `AGENTS.md`.

## Step 1. Audit and baseline

Реестр мест, где читаются настройки и `access.json` (собран 2026-11-20, `rg` и
чтение). Колонка «Этап» говорит, кто правит.

| Место | Что читает сейчас | Что станет | Этап |
| --- | --- | --- | --- |
| `internal/config/config.go` `Config`, `Load`, `defaults` | все секции файла | `home`, `default_profile`, `[files]`; старые секции читаются отдельной структурой только для переноса | Э1, Э6 |
| `internal/config/background_sync.go`, `semantic_search.go` | секции файла, проверки | проверки остаются, их зовёт реестр | Э1 |
| `internal/config/write.go` `UpdateProfile*`, `editProfile` | запись `[profiles.*]` | только для старой цепочки миграции и удаления секций | Э6 |
| `internal/config/migrate.go` `MigrateProfiles` | старые файлы профиля | без изменений, перед переносом в базу | Э6 |
| `internal/config/template.toml`, `template.go` | шаблон со всеми ключами | шаблон с `home`, `default_profile`, `[files]` | Э7 |
| `internal/cli/throttle_config.go` | `Behavior`, `TG_CLI_THROTTLE_PERCENT`, `TG_CLI_FLOOD_AUTOWAIT` | значения из базы плюс переменные | Э5 |
| `internal/cli/background_sync_config.go`, `semantic_search_config.go` | секции, `TG_CLI_*` | значения из базы плюс переменные | Э5 |
| `internal/cli/network_flags.go` `networkSettings`, `checkRunningNetwork` | `Profiles[*].Transport/Proxy`, `TG_CLI_PROXY`, запись в файл | база, запись в базу | Э5 |
| `internal/cli/daemon_cmd.go` `credentials`, `deviceModel`, `daemonStart`, `startBackground` | `Profiles[*].Client/Device`, запись в файл | база через офлайн-функцию в родителе, чтение в дочернем процессе | Э5 |
| `internal/cli/profile_settings.go` `saveProfile`, `migrateProfiles` | запись в файл | запись в базу; перенос | Э5, Э6 |
| `internal/cli/session_cmd.go:79, 175-190` | `Profiles[*].Client`, запись при `session import` | база (офлайн, под тем же замком профиля) | Э5 |
| `internal/cli/client.go:63-77` `expectedUser` | флаг, переменная, `Behavior.ExpectUser` | флаг и переменная; значение из базы подставляет Daemon | Э4 |
| `internal/cli/config_cmd.go` `config show` | все секции | только файл (Q11) | Э7 |
| `internal/cli/schema_cmd.go:48-74` блок `config` | все ключи | блок `config` для файла, новый блок `settings` | Э7 |
| `internal/cli/write_cmd.go:226` | подсказка «add the chat to the write list in ... access.json» | подсказка про `access.write` и владельца | Э7 |
| `internal/cli/send_media_cmd.go`, `home_move.go` | имя `access.json` в тексте, перенос каталога | тексты; перенос каталога без изменений | Э7 |
| `internal/daemon/daemon.go` `Options.Access`, `access.Open(opts.Paths.Access)` | `access.Loader` файла | политика из снимка базы | Э3 |
| `internal/daemon/access.go` `policy()` | загрузчик файла | снимок в памяти, перечитывается после записи | Э3 |
| `internal/daemon/paths.go` `Paths.Access`, `accessMessage` | путь и имя файла в текстах ошибок | путь нужен переносу; тексты называют ключи `access.*` | Э3, Э6 |
| `internal/daemon/owner_state.go`, `dialogs.go`, `sessions.go`, `channels_profile.go`, `chat_admin.go`, `download.go`, `send_media.go`, `export_month.go`, `message_extras.go` | тексты «requires ... in access.json» | тексты с ключами `access.*` | Э3 |
| `internal/daemon/file_guard.go:82, 205-208` | конфликт имени `access.json` | после переноса файл не нужен; проверка по Q8 | Э6 |
| `internal/daemon/home_move.go:322` | копия `access.json` при переезде каталога | без изменений (файла может не быть) | проверить в Э6 |
| `internal/daemon/semantic_search.go:57`, `semantic_index.go` | `opts.SemanticSearch` | снимок настроек | Э4 |
| `internal/daemon/background_sync.go` | `opts.BackgroundSync` | снимок и смена на лету | Э4 |
| `internal/daemon/account_guard.go:39-47` | `req.ExpectUser` | плюс значение из базы | Э4 |
| `internal/daemon/daemon.go` `register`, `needsTelegram`, тест `TestRegisteredMethodsHaveAccessClass` | список методов | методы `settings.*` | Э4 |
| `internal/throttle/throttle.go` `Limiter` | `cfg.Percent`, `cfg.FloodAutoWait` неизменны | методы смены на лету | Э4 |
| `internal/access/access.go` `Open`, `Loader`, `parse` | файл JSON | разбор и проверка остаются для переноса; `Policy` строится из значений базы | Э3 |
| `internal/daemon/launch_unix.go:46-49` | не передаёт `TG_CLI_PROXY` дочернему процессу | без изменений: прокси из переменной пишется в базу родителем | Э5 |
| `docs/cli/config.md`, `access.md`, `overview.md`, `daemon-and-auth.md`, `chats.md`, `folders-and-account.md`, `messages-read.md`, `messages-write.md`, `saved.md`, `users-and-contacts.md`, `notes-and-search.md`, `docs/contracts.md` | `access.json`, `config.toml` | новая модель; новый `docs/cli/settings.md` | Э8 |
| `docs/skill/tg/SKILL.md`, `references/{safety,errors,admin,media,writing,history,stream,daemon-auth,reading}.md`, `recipes/{_template,monthly-archive,send-media,send-report}.md` | `access.json`, `config.toml` | `access.*` через владельца, `tg settings` | Э8 |
| `README.md:22, 201` и раздел о конфигурации | `access.json`, `config.toml` | новая модель | Э8 |
| `FEATURES.md` 1.14, 6.2, 6.3.1, 9.2, 9.6, 10.13.4, 10.16.9, 13.4.2, сценарии 1.14 и 9.2, список проверки | `access.json`, `config.toml` | новая модель; новый пункт `tg settings` (1.15, следующий свободный в разделе 1) | Э8 |
| `bin/skill_check.py:23` | комментарий про `access.json` | `access.*` | Э8 |
| `AGENTS.md:25, 44, 51, 52, 87-89` | `access.json`, `config.toml` | только предложение текста, см. ниже | менеджер |

- [ ] Базовый снимок на копии (менеджер или Э6): скопировать `config.toml`,
  `default/access.json` и `db/default/` без `session.json` в
  `var/agents/plans/20261120_2242_settings_in_database/home/.config/tg-cli/`,
  выполнить старым бинарником `HOME=<копия> tg config show` и сохранить ответ в
  `var/agents/plans/20261120_2242_settings_in_database/baseline_config_show.json`.
  Ожидаемо: код 0, профиль `default`, секции `background_sync`,
  `semantic_search`, `profiles.default`.
- [ ] Снимок политики на копии: тест-помощник Э3 печатает ответы
  `access.Policy` из файла копии на наборе id (id из `allow`, `block`, `write`,
  `admin` плюс три посторонних) в
  `var/agents/plans/20261120_2242_settings_in_database/baseline_policy.json`.

## Этапы для Codex

Каждый этап отдаётся одному coder в worktree потока, начинается с загрузки
скилла `expert-golang` и `.agents/rules/testing.md` и заканчивается зелёным
`just check`. Модель по умолчанию для Go-кода: Terra (`gpt-6.1-terra`).
Менеджер называет выбранную модель при запуске этапа.

### Э1. Реестр настроек (Terra)

- [x] `internal/config/settings.go`: тип `SettingKey` с полями `Name`, `Type`
  (`bool`, `number`, `int`, `duration`, `clock`, `string`, `ids`, `paths`),
  `Default`, `Env`, `Flag`, `Apply` (`live`, `restart`), `Protected` (фраза),
  `Secret`, функция проверки. Ключи по таблице выше; проверки берутся из
  существующих (`validateProfile`, `BackgroundSync.Resolve`,
  `SemanticSearch.Resolve`, правила `access.parse`).
- [x] Разбор значения из строки командной строки и из JSON базы; канонический
  вид в JSON (списки отсортированы, без повторов).
- [x] Проверка пар (`window_start < window_end`, `interval_min <= interval_max`,
  `transport` и `proxy`) по полному набору значений, а не по одному ключу.
- [x] Тесты: каждый ключ, каждое неверное значение, пары, канонический вид,
  умолчания совпадают с сегодняшними `defaults()` и шаблоном.
- Проверка: `go test ./internal/config -count=1` зелёный.

### Э2. Таблицы и миграция схемы (Terra)

- [x] `internal/store/migrations/0013_settings.sql` (в плане 0012; `0012_voice.sql` влит в main, номер 0013 с 2026-11-21; сверить перед
  началом: `ls internal/store/migrations`): таблицы по решению Q1
  (`20261120_2344_settings_are_one_key_value_table_and_the_passphrase_hash_has_its_own_table`): `settings(key TEXT PRIMARY KEY, value TEXT NOT NULL CHECK
  (json_valid(value)), updated_at INTEGER NOT NULL)`, `owner_secret(id INTEGER
  PRIMARY KEY CHECK (id = 1), hash TEXT NOT NULL, created_at, updated_at)`,
  `settings_meta(key TEXT PRIMARY KEY, value TEXT)` для отметки переноса.
- [x] Методы `store`: прочитать все настройки, записать набор ключей одной
  транзакцией, удалить ключ, прочитать и записать хэш, отметка переноса.
- [x] Тесты: миграция с версии 11 на копии тестовой базы, запись и чтение,
  откат транзакции при ошибке, `owner_secret` не попадает в чтение настроек.
- Проверка: `go test ./internal/store -count=1` зелёный; `PRAGMA user_version`
  тестовой базы равен 12.

### Э3. Политика доступа из базы и фраза владельца (Terra; ревью сильной моделью)

- [x] `internal/access`: `Policy` строится из значений реестра
  (`FromSettings`), разбор файла (`parse`) остаётся для переноса. Ошибка
  чтения или проверки даёт `denied`.
- [x] Daemon держит снимок политики в памяти, перечитывает его после записи
  `access.*` и при старте; `d.policy()` берёт снимок. Ошибка снимка пишет
  `access policy unavailable` в журнал, как сегодня.
- [x] Хэш фразы: `golang.org/x/crypto/argon2` (сейчас `indirect` в `go.mod`,
  станет прямой), argon2id с солью 16 байт, параметры записаны в строке хэша;
  сравнение `subtle.ConstantTimeCompare`. Замер времени проверки на машине
  разработчика в тесте-бенчмарке.
- [x] Ограничение подбора по Decision log, в памяти Daemon.
- [x] Функции пакета `daemon` для фразы: `init` (только без хэша), `change`
  (нужна старая фраза), `reset` (только офлайн, при остановленном Daemon).
- [x] Тексты ошибок «requires ... in access.json» во всех файлах из реестра
  называют ключ `access.*`.
- [x] Тесты: инвариант 1 (файл и база дают одни ответы), инвариант 2
  (повреждённое значение закрывает всё), инвариант 3 для прямого RPC без фразы,
  с неверной фразой, после блокировки; переносятся все случаи
  `internal/access/access_test.go` и `access_audit_test.go`.
- Проверка: `go test ./internal/access ./internal/daemon -run 'Access|Owner|Policy' -count=1`
  зелёный; ревью безопасности этапа сильной моделью Codex с отчётом в
  `var/agents/plans/20261120_2242_settings_in_database/review_e3.md`.

### Э4. Методы settings.* и применение на лету (Terra)

- [ ] `internal/rpc`: методы `settings.list`, `settings.get`, `settings.set`,
  `settings.unset`, `settings.add`, `settings.remove`, `settings.token_init`,
  `settings.token_change`; параметры с полем фразы для `access.*`; типы ответа
  по разделу «Ключи и применение»; ответ `get` и `list` несёт `value`,
  `source`, `stored` (Q5); `get access.allow|block` с id только с фразой (Q4). `token_reset` в RPC нет.
- [ ] `internal/daemon`: обработчики, класс доступа в таблице теста
  `TestRegisteredMethodsHaveAccessClass`, исключение из `needsTelegram` и
  `accountMethod`.
- [ ] Снимок настроек Daemon: значения базы плюс перекрытия, с которыми Daemon
  стартовал (переменные и флаги), с источником каждого значения (Q5).
- [ ] `throttle.Limiter`: методы смены процента и паузы FLOOD_WAIT под
  мьютексом; тест с запросами в очереди.
- [ ] Фоновая загрузка: смена окна, интервалов и пауз на лету; `enabled` на
  лету, если планировщик запускается и останавливается без перезапуска,
  иначе `restart`. Итог записать в таблицу ключей плана.
- [ ] `semantic_search.min_similarity` читается из снимка на каждый поиск.
- [ ] `behavior.expect_user` из снимка подставляется в `account_guard`, если
  запрос пришёл без него.
- [ ] Журнал записи по Decision log.
- [ ] Тесты: каждый ключ «сразу» действует на следующий запрос без перезапуска;
  каждый ключ «перезапуск» даёт `restart_required: true` и не меняет поведение
  до перезапуска; значение под переменной даёт `overridden_by: env`.
- Проверка: `go test ./internal/daemon ./internal/throttle ./internal/rpc -count=1` зелёный.

### Э5. Старт Daemon из базы и офлайн-доступ (Terra)

- [ ] Функция пакета `daemon` для офлайн-доступа: замок профиля
  (`WithProfileLock`), `db.lock`, `CheckDatabaseBinding`, `MoveLegacyDatabase`,
  `BindDatabase`, `store.Open`, действие, закрытие. Отказ кодом 1, если замок
  занят (Daemon работает или стартует).
- [ ] `daemon start`: родитель (`startBackground`) пишет `--client`,
  `--device`, `--transport`, `--proxy`, `TG_CLI_PROXY` в базу офлайн-функцией
  до запуска дочернего процесса; дочерний процесс (`--foreground`) читает
  профиль, дроссель, фоновую загрузку и индекс из базы и применяет переменные
  окружения. Порядок открытия базы и создания клиента Telegram согласуется с
  `daemon.Run` (сегодня клиент создаётся до `Run`, а база открывается внутри).
- [ ] `checkRunningNetwork` сравнивает с сохранённым в базе прокси через
  `settings.get` работающего Daemon.
- [ ] `session import` пишет `profile.client` в базу под тем же замком
  профиля; `session export` берёт пресет из снимка Daemon.
- [ ] Тесты `internal/cli`: `config_priority_test.go`, `network_flags_test.go`,
  `client_restart_config_test.go`, `background_sync_config_test.go`,
  `semantic_search_config_test.go`, `session_cmd_test.go` переходят на базу без
  потери случаев; тест прямых импортов `internal/cli` остаётся зелёным.
- Проверка: `go test ./internal/cli ./internal/daemon -count=1` зелёный.

### Э6. Перенос config.toml и access.json (Terra; ревью сильной моделью)

- [ ] `config.Load` читает старые секции в отдельную структуру `Legacy` и не
  падает на них (иначе после удаления полей из `Config` любой старый файл давал
  бы код 2 «unknown config key»).
- [ ] Перенос в пакете `daemon` (ему нужна база): найти профили по Q6, для
  каждой базы одной транзакцией записать значения секций и `access.json`
  (проверенные реестром и `access.parse`), поставить отметку переноса,
  перечитать и сверить; затем копия `config.toml` и `access.json` в
  `<home>/.config-migration-<время>/` (каталог 0700, файлы 0600), удаление
  секций из `config.toml` под замком конфига (`withConfigLock`, запись
  атомарная), удаление `access.json`.
- [ ] Некорректный файл: ни одной записи в базу, файлы на месте, Daemon не
  стартует (перенос идёт в `daemon start`, Q7), ошибка называет файл без
  содержимого.
- [ ] Повторный запуск без файлов: ничего не меняет (тест сравнивает базу и
  файлы байт в байт).
- [ ] Файл `access.json` или секция `config.toml`, появившиеся после отметки
  переноса: не импортируются, `daemon start` и офлайн-запись `tg settings`
  отказывают кодом 2 с путём файла или именем секции (Q8).
- [ ] Точки вызова (Q7): `daemon start` в родителе под замками, офлайн
  `tg settings`, `tg config migrate`.
- [ ] Тесты: успех, некорректный `access.json`, некорректная секция, сбой
  сверки (файлы не удалены), сбой записи копии, повтор, несколько профилей (Q6),
  инвариант 1 и 4 на фикстурах, комментарии шаблона вне удалённых секций целы.
- [ ] Прогон на копии боевых файлов (без `session.json`):
  `HOME=<копия> dist/tg settings list` при остановленном Daemon копии; сверка с
  `baseline_config_show.json` и `baseline_policy.json`.
- Проверка: `go test ./internal/config ./internal/daemon -run 'Migrat|Settings' -count=1`
  зелёный; сверка на копии без расхождений; ревью сильной моделью в
  `var/agents/plans/20261120_2242_settings_in_database/review_e6.md`.

### Э7. Команда tg settings, schema, config show (Terra)

- [ ] `internal/cli/settings_cmd.go`: подкоманды по Q2; онлайн через RPC,
  при коде «Daemon не работает» офлайн-функция; фраза через `Deps.ReadSecret`
  (без терминала код 2 до обращения к Daemon); `token init` и `change` просят
  фразу дважды.
- [ ] Классы безопасности: `list`, `get` `read`; запись `local_write`;
  `access.*` с пометкой о фразе в схеме.
- [ ] `tg schema`: блок `settings` из реестра (ключ, тип, умолчание,
  переменная, флаг, применение, защита, секрет); блок `config` только для
  `home`, `default_profile`, `[files]`.
- [ ] `tg config show` отдаёт только `home`, `db_dir`, `default_profile`,
  `files` (Q11); шаблон `config.toml` только с ними; `config init`
  пишет новый шаблон; `template_test.go` обновлён.
- [ ] Подсказки ошибок записи (`write_cmd.go:226`, `send_media_cmd.go`)
  называют `access.write`, `access.files` и владельца.
- [ ] `docs/cli/settings.md` с разделом на каждую подкоманду (тест
  `internal/cli` проверяет), таблицы флагов через маркеры `<!-- schema:... -->`.
- Проверка: `go test ./internal/cli -count=1` зелёный, включая тест разделов
  `docs/cli/`; `dist/tg schema` содержит блок `settings` со всеми ключами
  таблицы.

### Э8. Документы, навык, FEATURES, README (лёгкая модель Codex для механики; `docwriter` и `glavred` для содержательных текстов)

- [ ] `docs/cli/access.md` переписан: где лежат списки, кто и как их меняет,
  фраза, сброс, отказ в закрытую сторону, `daemon status`.
- [ ] `docs/cli/config.md`, `overview.md` (Configuration, таблица файлов,
  фраза «CLI never opens the database»), `daemon-and-auth.md`, `contracts.md`
  и остальные страницы `docs/cli/` из реестра.
- [ ] `docs/skill/tg`: агент читает настройки `tg settings list|get`, пишет
  только не-`access.*` ключи по просьбе, про `access.*` просит владельца,
  id `allow` и `block` не запрашивает; описание ответа `tg config show`
  сокращено до ключей файла (Q11); `just skill-check` зелёный.
- [ ] `FEATURES.md`: пункты из реестра и новый 1.15 «`tg settings`»; статус
  проверки по правилам файла.
- [ ] `README.md`: строки 22, 201 и раздел о конфигурации.
- [ ] `bin/skill_check.py:23` комментарий.
- Проверка: `go test ./internal/cli -run Docs -count=1`, `just skill-check`,
  `rg -n 'access\.json' docs README.md FEATURES.md docs/skill` возвращает
  только упоминания переноса и истории.

### Э9. Итоговая проверка и changelog (Terra)

- [ ] Записи changelog через `just changelog add` (текст по-английски):
  `Добавлено` «`tg settings` lists, reads and changes the profile settings
  stored in the database.»; `Изменено` --breaking «Settings moved from
  config.toml to the profile database on the first start; config.toml keeps
  home, default_profile and [files].»; `Удалено` --breaking «access.json is
  imported once and deleted; change access lists with `tg settings` and the
  owner passphrase.»; `Безопасность` «Access list changes require the owner
  passphrase typed on a terminal.»; `Изменено` --breaking «`tg config show`
  prints only home, db_dir, default_profile and files; read other settings with
  `tg settings list`.» (Q11). Каждая с `--decision` на свою запись.
- [ ] `just check` зелёный; `git diff HEAD --name-only --relative | bin/atlas affected - -o path`
  прогнан, затронутые тесты зелёные.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Реестр | `go test ./internal/config -count=1` | зелёный | not_run |
| Миграция схемы | `go test ./internal/store -count=1` | зелёный, версия 12 | not_run |
| Инварианты 1-3 | `go test ./internal/access ./internal/daemon -run 'Access\|Owner\|Policy' -count=1` | зелёный | not_run |
| Методы и применение | `go test ./internal/daemon ./internal/throttle ./internal/rpc -count=1` | зелёный | not_run |
| CLI, приоритет, импорты | `go test ./internal/cli -count=1` | зелёный, тест прямых импортов зелёный | not_run |
| Перенос | `go test ./internal/config ./internal/daemon -run 'Migrat\|Settings' -count=1` | зелёный | not_run |
| Перенос на копии | `HOME=var/agents/plans/20261120_2242_settings_in_database/home dist/tg settings list` | значения совпадают с базовым снимком, `access.json` и секции удалены, копия в `.config-migration-*` | not_run |
| Политика на копии | тест-помощник Э3 на базе копии | ответы совпадают с `baseline_policy.json` | not_run |
| Схема | `dist/tg schema \| jq '.settings'` | все ключи таблицы, у `access.*` пометка о фразе | not_run |
| Документы | `go test ./internal/cli -run Docs -count=1`, `just skill-check` | зелёные | not_run |
| Всё | `just check` | зелёный | not_run |

## Deploy / rollback

- [ ] До деплоя: Daemon остановлен; копия `~/.config/tg-cli/db/default/`
  (с `-wal` и `-shm`, после остановки) в `~/.config/tg-cli/.db-backup-<время>/`;
  ручная копия `config.toml` и `default/access.json` рядом.
- [ ] Деплой: `just install` (останавливает Daemon старым бинарником, ставит
  новый, запускает `daemon start`; перенос идёт при старте, Q7).
- [ ] Сразу после деплоя: владелец в терминале выполняет
  `tg settings token init` (Q9); проверяет `tg settings list` и
  `tg daemon status` (поле `access` совпадает по числам с прежним).
- [ ] Откат: остановить Daemon новым бинарником; вернуть `config.toml` и
  `access.json` из `.config-migration-<время>/`; вернуть `db/default/` из
  `.db-backup-<время>/` (база версии 12 старым бинарником не открывается);
  поставить старый бинарник; `tg daemon start`.
- [ ] Сессии, кэши, очереди, cron не затрагиваются; `session.json` не
  копируется и не меняется.

## Cleanup

- [ ] Временные файлы только в `var/agents/plans/20261120_2242_settings_in_database/`.
- [ ] Копия боевых файлов из `var/agents/plans/.../home` удалена после Э6
  (она содержит списки доступа и прокси).
- [ ] В `.agents/plans` нет временных файлов.

## Definition of done

- [ ] Код изменён только в объёме плана.
- [ ] Инварианты 1-7 держатся, их тесты зелёные.
- [ ] Перенос проверен на копии боевых файлов без расхождений.
- [ ] `tg schema` и `docs/cli/settings.md` описывают все ключи и подкоманды.
- [ ] Документы, навык, `FEATURES.md`, `README.md` обновлены; содержательные
  тексты прошли `glavred`.
- [ ] Записи changelog добавлены (Э9).
- [ ] Открытые вопросы плана закрыты или явно перенесены.
- [ ] Строка `AGENTS.md` правлена по согласию владельца или отказ записан.
- [ ] Нет шагов `- [0]`, нет временных файлов в `.agents/plans`.

## Known facts

Снимок на 2026-11-20:

- Последняя миграция схемы `0011_stem_fts.sql`, следующий свободный номер
  `0012`; в других планах `0012` не занят (`rg '0012' .agents/plans`).
- У владельца один профиль `default`. В `~/.config/tg-cli/` лежат `config.toml`,
  `config.lock`, `.session-start.lock`, каталоги `db/default` и `default`; в
  `default/` есть `access.json`. В `config.toml` три секции:
  `[background_sync]`, `[semantic_search]`, `[profiles.default]`; `[behavior]`
  нет.
- Daemon запускается как `tg daemon start --foreground` в дочернем процессе;
  дочерний процесс сам читает конфиг и переменные. `TG_CLI_PROXY` ему не
  передаётся (`internal/daemon/launch_unix.go:46-49`): родитель пишет прокси в
  профиль, дочерний читает сохранённое.
- Клиент Telegram создаётся в CLI до `daemon.Run`, база открывается внутри
  `Run` после замков (`internal/daemon/daemon.go:141-182`).
- `access.Loader` перечитывает файл по времени, размеру и режиму;
  `d.policy()` закрывает всё при ошибке (`internal/daemon/access.go:12-22`).
- `throttle.Limiter` не умеет менять процент и паузу на лету
  (`internal/throttle/throttle.go:168-317`, публичных сеттеров нет).
- `expect_user` из конфига читает CLI и шлёт в каждом запросе
  (`internal/cli/client.go:63-77`, `internal/rpc/client.go:139`).
- `Deps.ReadSecret` уже читает секрет без эха и отказывает без терминала
  (`internal/cli/terminal.go:49-58`).
- `golang.org/x/crypto v0.58.0` есть в `go.mod` как `indirect`.
- Автозапуска Daemon другими командами нет (решение `20261114_0032`).
- `just install` останавливает и запускает только Daemon профиля по умолчанию.
- `access.json` упоминают около 60 файлов: 12 страниц `docs/cli/` и
  `docs/contracts.md`, 15 файлов навыка, `FEATURES.md` (18 мест), `README.md`,
  `AGENTS.md` и код `internal/daemon` (тексты ошибок).

## Предложения менеджеру

### Разведение с планом web_viewer

План `web_viewer` не правился. Предложение:

- Переходит сюда из фазы 2 `web_viewer`: Ф2.0 (решения: Q8 теперь закрыт
  фразой владельца, Q9 закрыт переносом с копией), Ф2.1 (таблицы и миграция),
  Ф2.2 (политика из базы), Ф2.3 (импорт `access.json`), Ф2.5 (методы записи:
  здесь это `settings.set|unset|add|remove` с фразой вместо
  `access.set|add|remove` без токена), часть Ф2.6 (документы, решения, журнал
  правок).
- Отпадает Ф2.4 («методы скрыты из `tg schema` и навыка»): `tg settings`
  публичная команда, защита держится на фразе, а не на скрытности.
- Остаётся в фазе 2 `web_viewer`: канал владельца для браузера, то есть как
  веб передаёт фразу Daemon (Q13 здесь), подтверждение расширяющих правок,
  поле `daemon status` об источнике политики, если оно нужно вебу. Фаза 2
  `web_viewer` начинается после `done` этого плана; критерий перехода 2 → 3
  ссылается на этот план.
- Строки Decision log `web_viewer`, которые нужно поправить (по правилам плана:
  «было, стало, почему», новой строкой со ссылкой):
  1. «Списки доступа переезжают из `access.json` в базу ... отменяет правило»:
     переезд делает этот план, ссылка на решение
     `20261120_2242_settings_live_in_the_profile_database_and_config_toml_keeps_only_what_finds_it`.
  2. «Q8: записывающие методы доступа и настроек без токена владельца ...
     методы скрыты»: заменяется решением
     `20261120_2242_access_settings_are_written_only_with_the_owner_passphrase_typed_on_a_terminal`.
  3. «Q9: `access.json` один раз импортируется ... в файле ничего не
     остаётся»: импорт и удаление остаются, но с копией в
     `.config-migration-<время>` (решение
     `20261120_2242_settings_move_from_config_toml_and_access_json_once_in_one_transaction`).
  4. «Q10: веб правит все поля доступа»: остаётся, но каждая правка требует
     фразу (Q13).
- Ещё в `web_viewer`: строки Q8 и Q9 таблицы «Open questions», абзац «Цена
  решения (Q8: владелец выбрал «никак»)», первый риск раздела «Risks and
  strategy», шаг деплоя фазы 2 про ручную копию `access.json`, шаг деплоя фазы 1
  «добавить `~/.config/tg-cli/downloads` в `downloads` в `access.json`» (после
  этого плана: `tg settings add access.downloads ...`).

### Текст для AGENTS.md (по согласию владельца)

Строки 87-89 заменить на:

```markdown
- Do not send, edit, delete, forward, react, pin, vote or mark a chat read in a real Telegram account without an explicit instruction in the current task. Writing is possible only into chats listed in the `access.write` setting. Reading is allowed.
- Sending files is possible only from the directories in the `access.files` setting; downloaded files may be written only inside `access.downloads`. Never change an `access.*` setting and never try the owner passphrase: only the owner changes access lists, typing the passphrase on a terminal. If a task needs a chat or a directory that is not listed, ask the owner.
- Dialog and folder writes require `access.dialogs`; session termination requires `access.sessions`.
```

Строку 51 заменить на:

```markdown
- Configuration is `~/.config/tg-cli/config.toml`; it keeps only `home`, `default_profile` and `[files]`. Every other setting (behavior, background sync, semantic search, the profile client, device, transport and proxy, the access lists) lives in the profile database and is read and changed with `tg settings`, also while the Daemon is stopped. `tg config path`, `init`, `show` and `migrate` inspect the file, write its template or migrate former setting files. `home` selects the base directory of profile data; `TG_CLI_HOME` overrides it without changing the file. Neither moves the database: it is `<config dir>/db/<profile>/<files.database>`, and the Daemon moves a database left in the old place on start.
```

В строке 52 `stored in the selected config profile` заменить на `stored in the profile settings`, а `store the network path in that profile` на `store the network path in the profile settings`.

В строке 44 `and \`daemon\` (paths and \`Run\`)` заменить на `and \`daemon\` (paths, \`Run\` and the settings access that opens the database while the Daemon is stopped)`.

Размер `AGENTS.md` после правки нужно сверить с лимитом 32768 байт.

## Open questions

| Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- |
| Q1. Схема таблиц | Определяет миграцию `0012`, методы `store` и цену нового ключа | A: одна таблица ключ-значение `settings` (значение JSON), проверка типов в реестре Go, хэш фразы в отдельной таблице `owner_secret`. B: типизированные таблицы на секцию (`behavior`, `background_sync`, ...), колонка на ключ. C: ключ-значение плюс отдельные таблицы списков доступа (`access_chat(list, chat_id)`, `access_root(list, path)`) | A. Цена: SQLite не проверяет тип значения (только `json_valid`), проверка целиком на реестре; новый ключ без миграции схемы. Фильтр чатов и сегодня строится в Go, таблицы списков (C) не нужны запросам | решён 2026-11-20, A: [запись](../decisions/20261120_2344_settings_are_one_key_value_table_and_the_passphrase_hash_has_its_own_table.md) |
| Q2. Форма ключей и набор подкоманд | Контракт команды и схемы | A: `секция.ключ` (`behavior.throttle_percent`, `profile.proxy`, `access.write`); `list`, `get`, `set`, `unset`, `add` и `remove` для списков, `token init|change|reset`. B: только `list`, `get`, `set`, `unset`; список задаётся целиком. C: путь как в TOML (`profiles.default.proxy`) | A. Цена: девять подкоманд в схеме. Без `add` и `remove` правка списка требует переписать его целиком, и ошибка в одном id выбросит соседние | решён 2026-11-20, A: [запись](../decisions/20261120_2344_setting_keys_are_section_dot_key_and_lists_change_by_add_and_remove.md) |
| Q3. Секреты в `get` и `list` | Пароль прокси и секрет MTProxy | A: всегда скрыты, как в `config show` сегодня (`Redacted`), показа нет. B: `--reveal` только в терминале. C: показывать как есть | A. Цена: владелец не прочитает пароль прокси обратно, только задаст заново | решён 2026-11-20, A: Decision log, строка Q3 |
| Q4. Кто читает `access.*` | Сегодня закрытые id агенту не показывает ни одна команда (`docs/cli/access.md`, «What a closed chat looks like») | A: читает любой. B: `list` показывает числа для `allow` и `block` (как `daemon status`), полные `write`, `admin`, `files`, `downloads` и флаги; `get access.allow|block` с id только с фразой. C: все `access.*` только с фразой | B. Цена: два режима вывода; агент по-прежнему видит, куда ему можно писать | решён 2026-11-20, B: [запись](../decisions/20261120_2344_tg_settings_shows_chat_ids_of_allow_and_block_only_with_the_owner_passphrase.md) |
| Q5. Источник значения в `get` | Переменная окружения Daemon перекрывает базу, а у вызывающего `tg settings` могут быть другие переменные | A: Daemon отвечает действующим значением (`value`), источником (`flag`, `env`, `database`, `default`) и записанным (`stored`); офлайн-ответ даёт `database` или `default` и пометку, что переменные применятся при старте. B: показывать только записанное | A. Цена: Daemon хранит, откуда взялось каждое значение при старте | решён 2026-11-20, A: Decision log, строка Q5 |
| Q6. Несколько профилей и общие секции | Секции `[behavior]`, `[background_sync]`, `[semantic_search]` общие, а базы у каждого профиля свои; перенос первого профиля не должен лишить остальные значений | A: перенос за один проход пишет общие секции в базы всех профилей (`db/*/` и `[profiles.*]`); если Daemon какого-то профиля работает, перенос отказывает кодом 1 и просит его остановить; секции удаляются из файла после всех баз. B: перенос по профилю, общие секции удаляются, когда отмечены все известные профили. C: общие настройки в отдельной базе `db/settings.db` | A. Цена: перед деплоем стоят Daemon всех профилей; у владельца профиль один | решён 2026-11-20, A: Decision log, строка Q6 |
| Q7. Когда идёт перенос | Влияет на деплой и на поведение первого старта | A: автоматически в `daemon start` (в родителе, под замками, как перенос базы), в офлайн `tg settings` и в `tg config migrate`. B: только явной командой `tg config migrate`; до неё Daemon отказывает стартовать с подсказкой | A. Цена: первый старт нового бинарника меняет файлы без вопроса; так же сделаны переносы каталога и базы (решения `20261116_0136`, `20261120_2116`) | решён 2026-11-20, A: Decision log, строка Q7 |
| Q8. Файл, появившийся заново после переноса | `access.json` или секция `config.toml`, созданные по старой привычке или по наведённой инструкции, обошли бы фразу при повторном импорте | A: не импортируется; `daemon start` и `tg settings` отказывают кодом 2 с именем файла и подсказкой удалить его или перенести правила командой. B: импортируется снова. C: игнорируется, Error в журнале | A. Цена: забытый файл не даёт стартовать до удаления. B открывает обход защиты, C молча расходится с ожиданием человека | решён 2026-11-20, A, код 2 с путём файла: [запись](../decisions/20261120_2344_access_json_that_appears_after_the_move_is_not_imported_and_stops_the_daemon.md) |
| Q9. Первая фраза владельца | До первой фразы записи `access.*` нет вообще, а задать её может кто угодно с терминалом | A: `tg settings token init` в терминале, только пока хэша нет, онлайн или офлайн; до этого запись `access.*` отказывает. B: фраза задаётся при переносе (нужен терминал при первом старте). C: `init` только при остановленном Daemon | A плюс шаг деплоя «владелец сразу задаёт фразу». Цена: до `init` агент с псевдотерминалом мог бы задать фразу первым; окно закрывается деплоем | решён 2026-11-20, A плюс шаг деплоя: [запись](../decisions/20261120_2344_the_first_owner_passphrase_is_set_by_token_init_while_no_hash_exists.md) |
| Q10. Защита ключей вне `access.*` | Агент может поднять `throttle_percent` до 100, включить фоновую загрузку или сменить прокси | A: фраза только для `access.*` (решение владельца), остальное класс `local_write` и запись в журнал. B: фраза ещё для `profile.*` и `behavior.throttle_percent` | A. Цена: наведённый вызов может сменить прокси; но `daemon start --proxy` пишет его без фразы по решению владельца, так что B защиту не добавит | решён 2026-11-20, A: [запись](../decisions/20261120_2344_the_owner_passphrase_guards_only_access_settings.md) |
| Q11. Ответ `tg config show` | Сейчас он печатает все секции; агенты могут его разбирать | A: только ключи файла (`home`, `db_dir`, `default_profile`, `files`), остальное в `tg settings list`; запись changelog `Изменено` --breaking. B: прежняя форма, значения из базы через Daemon или офлайн | A. Цена: ломается разбор старого ответа; B оставляет две команды, показывающие одно и то же | решён 2026-11-20, A, changelog `--breaking` и правка навыка `tg`: [запись](../decisions/20261120_2344_config_show_prints_only_the_keys_of_the_file.md) |
| Q12. `tg settings export|import` | Резервная копия и перенос настроек между профилями | A: не сейчас; копия настроек это копия базы. B: `export` в JSON без секретов и без id `allow`, `block`; `import` с фразой для `access.*` | A. Цена: второй профиль настраивается руками | решён 2026-11-20, A: Decision log, строка Q12 |
| Q13. Фраза в веб-панели (пересечение с `web_viewer`, фазы 2 и 3) | Решение владельца: фразу набирают в терминале; веб хочет править все поля доступа (Q10 `web_viewer`) | A: веб спрашивает фразу в форме и передаёт её Daemon; правило «терминал» расширяется до «терминал или форма веб-панели». B: веб правит только настройки вне `access.*`, списки остаются за `tg settings`. C: веб показывает списки только для чтения | C до отдельного решения владельца в плане `web_viewer`. Цена: фаза 3 `web_viewer` теряет правку списков из браузера | решён 2026-11-20, C: Decision log, строка Q13; форма фразы в вебе остаётся за `web_viewer` |
