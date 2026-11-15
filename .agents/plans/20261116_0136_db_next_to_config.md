# Пошаговый план: база профиля в `~/.config/tg-cli/db/<профиль>/`, шаблон `config.toml` и данные чатов

ID: 20261116_0136_db_next_to_config
Created: 2026-11-16
Owner: Мязин Евгений
Branch: feature/20261116_0136_db_next_to_config
PR: нет

Исполнитель кода: `pi` на модели `deepseek-flash` в панели Herdr, worktree `tg-cli-db_next_to_config`. Он не загружает скиллы Claude сам, поэтому каждое задание менеджера велит ему прочитать `~/.agents/skills/expert-golang/SKILL.md` (с `references/`) и `~/.agents/rules/testing.md` до первого изменения и сверить с ними свой diff перед отчётом. Одно задание: один шаг плана. Код, комментарии и сообщения об ошибках по-английски, отчёт по-русски. Кодер не ходит в Telegram, не открывает сессии и профиль владельца, не коммитит, не правит этот файл.

## Status

- Current status: approved (2026-11-16, менеджер по поручению владельца: «Решения сам прими, я не разбираюсь»)
- Last update: 2026-11-16
- Next safe step: шаг 1.1 кодеру `pi`. Фаза 1 (база и шаблон) идёт целиком; фаза 2 (данные чатов) начинается после шага 1.24 и после того, как менеджер закроет Q6–Q9 (до этого она `blocked` по вопросам, на фазу 1 они не влияют).

## Context and sources

- Входные требования фазы 1: решение [базы данных лежат в db рядом с конфигом](../decisions/20261116_0136_databases_live_in_db_next_to_the_config.md) (подтверждено владельцем). Оно пересматривает [config.toml держит пути, имена файлов, поведение и настройки профиля](../decisions/20261116_0017_config_toml_holds_paths_file_names_behavior_and_profile_settings.md). Смежное: [настройки профиля пишутся только в отдельную таблицу](../decisions/20261116_0123_profile_settings_are_written_only_into_a_separate_table.md) (`confirmed: no`).
- Входные требования фазы 2: поручение владельца 2026-11-16 через менеджера: VIEW `chats_state`, состав папок в `folder_chats` через `folder list`, полные данные каналов и групп фоновой задачей под общим дросселем. Образец задачи: [глубокая загрузка истории](../decisions/20261114_1353_deep_history_sync_is_a_background_job_behind_the_shared_throttle.md).
- Образец раскладки шагов и проверок: выполненный план [20261116_0020_config_toml](20261116_0020_config_toml.md).
- Образец шаблона конфига: `~/.config/tg-cli/config.toml` владельца (прочитан 2026-11-16; секретов нет, прокси и паролей нет). Формат ключа: что делает, `Default`, `Values`, `Overrides`.
- Прочитано: `AGENTS.md`, `.agents/rules/plans.md`, `.agents/rules/decisions.md`, `.agents/rules/docs_chain.md`, `.agents/rules/project/AGENTS.md`, `.agents/rules/testing.md`.
- Аудит кода: три разведки `pathfinder` (путь базы, порядок запуска Daemon, `store.Open`, тесты; конфиг, `tg config`, `tg schema`, документы, changelog; схема `store`, `dialog_state`, `folder list`, `chat info`, загрузка истории, дроссель, списки доступа). Планировщик перечитал сам `internal/daemon/paths.go`, `internal/daemon/file_guard.go`, `internal/daemon/daemon.go:93-133`, `internal/daemon/background.go:74-139`, `internal/daemon/session_file.go:15-25,52-72`, `internal/cli/daemon_cmd.go:80-95,158-215`, `internal/cli/session_cmd.go:130-150`, `internal/cli/client.go:16-34`, `internal/cli/cli.go:150-162`, `internal/cli/chat_sync_cmd.go:1-40`, `internal/cli/chat_state_cmd.go:10-12`, `internal/store/migrations/0002_schema.sql:1-60`, `internal/store/migrations/0006_chat_versions.sql`, `internal/tgclient/throttle.go:17-47`, `go.mod`.
- Не проверено: `go test` и `just check` (планировщик код не запускал); `json_extract` в `modernc.org/sqlite v1.62.0` (шаг 2.1); открытие копии базы в режиме WAL только для чтения; размер базы владельца (21 МБ из решения 0136). Профиль владельца, сессии и `access.json` не открывались.

## Goal

Фаза 1:
1. База профиля лежит в `<каталог конфига>/db/<профиль>/<files.database>`, по умолчанию `~/.config/tg-cli/db/default/store.db`. Каталоги `db/` и `db/<профиль>/` с правами `0700`, файл `0600`.
2. Сессия, сокет, замок, журнал Daemon, журнал удалений, `access.json`, `moderation.jsonl`, `account.jsonl` остаются в каталоге данных `<home>/<профиль>/`.
3. При старте Daemon старая база переносится сама: `wal_checkpoint(TRUNCATE)`, копия, сверка, удаление старого файла только после сверки. Новое место занято при живом старом файле: код 2 (кроме доведения прерванного переноса, Q2).
4. Ни один путь (авария, второй Daemon, смена `home` или имени файла) не создаёт молча пустую базу и не теряет историю.
5. `tg config init` пишет шаблон `config.toml` со всеми ключами в комментариях; шаблон встроен в бинарь и проверяется тестом против блока `config` в `tg schema`.

Фаза 2:
6. VIEW `chats_state` и команда для её чтения дают сводку по чатам из уже сохранённых данных, без запросов к Telegram.
7. Состав папок в `folder_chats` пишет `folder list`; поведение закреплено тестом и документом.
8. Полные данные каналов и групп загружаются фоновой задачей по одному запросу под общим дросселем, с прогрессом и возобновлением.

## Аудит мест (фаза 1)

### Где строится и открывается путь базы

| Место | Что делает сегодня | Что меняется |
| --- | --- | --- |
| `internal/daemon/paths.go:56-82` `ResolvePaths(home, profile, files)` | `Database = <home>/<профиль>/<files.Database>` (`:73`); каталога конфига не знает | новый вход: корень баз; новые поля `Paths.DatabaseDir` и `Paths.LegacyDatabase` |
| `internal/cli/client.go:16-34` `(*app).profile` | `home` из `TG_CLI_HOME`, иначе `config.Home`; вызывает `ResolvePaths` | передаёт корень баз `filepath.Dir(a.deps.ConfigPath)/db` |
| `internal/cli/cli.go:153-160` | путь конфига `~/.config/tg-cli/config.toml` через `os.UserHomeDir`, `Deps.ConfigPath` его подменяет | без изменений; отсюда берётся корень баз |
| `internal/cli/config_cmd.go:59-91` `configShow` | значения файла; `ResolvePaths` для каждого профиля ради длины сокета (`:76-81`) | новая сигнатура; поле `db_dir` (Q5) |
| `internal/cli/daemon_cmd.go:88` `credentials` | `StatePath: paths.Database` для курсоров обновлений | путь уже новый, правка не нужна |
| `cmd/tg-cli/main.go:29-40,91-94` `stateKV` | ленивый `store.Open(StatePath)` при первом `Get`/`Set` | открытие только после переноса (шаг 1.2) |
| `internal/daemon/daemon.go:128` | `store.Open(opts.Paths.Database)`, если `Store` не подставлен | перед ним перенос и `MkdirAll(DatabaseDir, 0700)` |
| `internal/store/store.go:40-110` `Open` | `O_RDWR|O_CREATE|O_EXCL`, `0600`, WAL, миграции; родительский каталог не создаёт (`store_test.go:236`) | без изменений; рядом помощники `Checkpoint`, `IntegrityCheck` |
| `internal/cli/session_cmd.go:141-145` `session import` | «профиль не пуст», если есть `session.json` или `p.Database` | проверяет и новую, и старую базу |
| `internal/daemon/session_file.go:52-72` `DuplicateProfiles` | обходит `<home>/*` по regexp профиля и берёт `daemon.lock` в каждом (`:68`) | пропускает каталог `db` (Q3) |
| `internal/config/migrate.go:31-232` | обходит профили в `home` ради `client`, `device`, `transport`, `proxy` | пропускает каталог `db` (Q3) |
| `internal/cli/schema_cmd.go:43-60` `configKeyDocs` | ключи блока `config`; `files.database` по умолчанию `store.db` (`:47`) | описание называет каталог `db/<профиль>/`; источник для теста шаблона |
| `internal/cli/config_cmd.go:50-58` `configCommand` | подкоманды `path`, `show`, `migrate`; регистрация `cli.go:184` | новая подкоманда `init` |

### Проверка смены имени файла (`internal/daemon/file_guard.go`)

- `CheckFileNames(p)` (`:25-122`) вызывается трижды: родитель `daemon start` (`internal/cli/daemon_cmd.go:166`), `Run` до замка (`daemon.go:98`) и после замка (`daemon.go:118`).
- Для базы она сравнивает `p.Database` с `<p.Dir>/store.db` (`:53,62-69`) и ищет в `p.Dir` любой файл с заголовком `SQLite format 3\x00` (`:81-118`). После смены пути обе ветки дадут код 2 ровно в том состоянии, которое должен обработать перенос.
- Ветка «каталога профиля нет, а `os.UserConfigDir()/tg-cli/<профиль>` есть» (`:27-44`) про `home` и остаётся.
- Тесты: `internal/daemon/file_guard_test.go:12-54,56-78`.

### Порядок в `Run` (`internal/daemon/daemon.go:93-271`)

1. `CheckFileNames` (`:98`), до замка.
2. `MkdirAll(Dir, 0700)` и `Chmod` (`:107-110`).
3. `acquireLock(<Dir>/daemon.lock)` (`:113`, `flock LOCK_EX|LOCK_NB`, `lock_unix.go:13-29`). Единственная защита от второго Daemon на профиле. Замок старого и нового бинаря лежит по одному пути.
4. `CheckFileNames` (`:118`), `guardSessionStart` (`:121`, замок `<home>/.session-start.lock`).
5. `store.Open` (`:128`), сокет (`:136-144`), `access.Open` (`:151-153`), затем `tg.Run` (около `:225`).

Фоновый старт: `daemon.Start` (`background.go:74-139`) пингует сокет, запускает потомка и ждёт ответа `StartTimeout = 15s` (`background.go:22`).

### Тесты, которые заденет смена

- `internal/daemon/daemon_test.go:426-450` (`TestResolvePaths`, `/h/work/store.db` на `:431`, `tg.db` на `:447`), `:365` (помощник `start`), `:509-520` (второй Daemon), `:540-555` (`TestFilePermissions` проверяет только `Dir`, `Socket`, `Lock`).
- `internal/daemon/file_guard_test.go:17-18,25-43,56-78`; `internal/daemon/notes_test.go:267-284` (каталог на месте базы), `:293-324`; `internal/daemon/access_test.go:20`.
- Около 30 вызовов `ResolvePaths` в тестах `internal/daemon` (`background_test.go`, `idle_test.go`) и `internal/cli` (`cli_test.go:445,841,927`, `background_cmd_test.go:52,89,139,184`, `session_cmd_test.go:22,92,183`, `daemon_logs_test.go:17,48`, `schema_fields_test.go:125`).
- `internal/config/config_test.go:50-56` (умолчания), `cmd/tg-cli/main_test.go:11`.

### Документы

`README.md:23,60,150`; `docs/cli/overview.md:58,60,68-80` (строка `store.db` на `:73`), `:87`; `docs/cli/config.md:3-6,41-61`; `docs/cli/notes-and-search.md:5` (`<profile>/store.db`); `docs/cli/access.md:9`; `docs/skill/tg-cli/SKILL.md:32`; `FEATURES.md:612` (10.1); `AGENTS.md:23,50`. `docs/contracts.md` пути базы не называет.

## Problem and contract (фаза 1)

### Раскладка

```
~/.config/tg-cli/                 0700, создаёт withConfigLock или перенос
  config.toml, config.lock
  db/                             0700
    <профиль>/                    0700
      <files.database>            0600, плюс -wal и -shm, пока работает Daemon
      data-dir                    0600, абсолютный путь каталога данных (Q1)
      db.lock                     0600, flock на время работы Daemon (Q1)
<home>/<профиль>/                 0700, как сегодня
  session.json, daemon.sock, daemon.lock, daemon.log, deletions.jsonl, access.json, ...
```

- Корень баз: `filepath.Dir(<путь конфига>)/db`. Он не зависит от `home` и `TG_CLI_HOME`, так требует решение 0136. Тесты подменяют путь конфига через `Deps.ConfigPath` и получают свой корень; пустой корень в `ResolvePaths` даёт ошибку.
- Имя профиля `db` запрещено (Q3): на Linux каталог данных по умолчанию совпадает с `~/.config/tg-cli`, и каталог профиля `db` совпал бы с корнем баз. `DuplicateProfiles` и миграция конфига пропускают каталог `db`.
- Сокет остаётся в каталоге данных, предел 103 байта и текст ошибки (`paths.go:78-80`) не меняются. Путь базы под этот предел не попадает.

### Состояния при старте

`old` = `<Dir>/<files.database>`, `new` = `<DatabaseDir>/<files.database>`.

| `old` | `new` | Что делает Daemon | Код |
| --- | --- | --- | --- |
| нет | нет | новый профиль: `MkdirAll(DatabaseDir)`, `store.Open` создаёт пустую базу | 0 |
| нет | есть | обычный старт | 0 |
| есть | нет | перенос, затем старт | 0, при сбое переноса 1 |
| есть | есть, `sha256` совпали, `old-wal` пуст | довести прерванный перенос: удалить `old` (Q2) | 0 |
| есть | есть, файлы разные | отказ «база есть в обоих местах», текст называет оба пути | 2 |

Проверка смены имени смотрит две папки:

- В `DatabaseDir`: нет `new`, но есть `store.db` или другой файл с заголовком SQLite: код 2.
- В каталоге данных: нет `old`, но есть `<Dir>/store.db` при другом `files.database`, или любой другой файл с заголовком SQLite: код 2. Перенос не угадывает, какой файл считать базой.
- `old` при пустом `new` до замка (родитель, `daemon.go:98`) не ошибка, а «ждёт переноса».

### Перенос

Функция `daemon.MoveLegacyDatabase(p Paths) (moved bool, err error)`; SQL-часть в `internal/store` (`Checkpoint(path)`, `IntegrityCheck(path)`). Вызов в `Run` сразу после `acquireLock` (`daemon.go:113`), до второго `CheckFileNames`, `guardSessionStart` и `store.Open`. Весь перенос идёт под `<Dir>/daemon.lock`.

1. Убрать хвосты прошлой аварии: `.<имя>.move-*.tmp` и их `-wal`, `-shm` в `DatabaseDir`.
2. `MkdirAll(DatabaseDir, 0700)` и `Chmod 0700` для `db/` и `db/<профиль>/`.
3. Открыть `old` без миграций схемы, `PRAGMA wal_checkpoint(TRUNCATE)`. `busy` не 0: код 1, «database is in use», ничего не меняется. Закрыть; `old-wal` нет или размер 0, иначе код 1.
4. Запомнить размер и `mtime` `old`. Скопировать в `<DatabaseDir>/.<имя>.move-<pid>.tmp` (`O_CREATE|O_EXCL`, `0600`), `fsync`. Нет места: временный файл удаляется, код 1.
5. Сверка: размер равен; `sha256` равен; `PRAGMA integrity_check` копии ровно `ok`; `PRAGMA user_version` равен. У копии не осталось `-wal` и `-shm`. Повторный `stat` `old`: размер и `mtime` прежние, `old-wal` пуст. Любое расхождение: копия удаляется, `old` цел, код 1, текст называет проверку.
6. `rename` копии в `new`, `fsync` каталога `DatabaseDir`.
7. Удалить `old`, затем пустые `old-wal` и `old-shm`, `fsync` каталога данных. Запись в журнал Daemon «moved database from X to Y».

`startBackground` в родителе вызывает тот же перенос под `daemon.WithProfileLock` (`session_file.go:15-25`) после пинга «не запущен» и до `Launch`: время переноса большой базы не входит в 15 секунд `StartTimeout`. Второй вызов в `Run` ничего не делает.

### Авария посреди переноса

| Где оборвалось | Что на диске | Следующий старт |
| --- | --- | --- |
| шаги 1-5 | `old` цел, в `DatabaseDir` временный файл | шаг 1 удаляет хвост, перенос повторяется |
| между 6 и 7 | `new` полная проверенная копия, `old` цел | `sha256` совпали, `old-wal` пуст: перенос доводится (Q2) |
| внутри 7 | `old` удалён, остались `old-wal`, `old-shm` | пустые удаляются молча, непустые остаются с предупреждением |
| отключение питания | `fsync` файла и каталогов на шагах 4, 6, 7 | одно из состояний выше |

### Второй Daemon и другие процессы

- Новый Daemon того же профиля уже работает: `acquireLock` даёт `ErrAlreadyRunning` до переноса.
- Работает Daemon старого бинаря: тот же `<Dir>/daemon.lock`, новый `daemon start` видит его по сокету (`background.go:86-91`) и не запускается. Перенос пройдёт после `tg daemon stop`.
- Сторонний процесс держит `old`: `busy` у checkpoint или изменившийся `stat`; код 1, `old` цел.
- Daemon с другим `home` и тем же именем профиля (Q1): `db.lock` занят, код 1 «already running»; `data-dir` указывает на другой каталог данных, код 2 с обоими путями и подсказкой поправить `data-dir`, если `home` сменили намеренно.

### Связь с `home`

- `home` и `TG_CLI_HOME` больше не двигают базу. Защита от смешения аккаунтов: файл `data-dir` (Q1).
- Предупреждение `file_guard.go:27-44` про каталог профиля в прежнем `home` остаётся.
- Перенос ищет `old` только в действующем `home`; база в прежнем `home` после его смены не переносится (вне плана, как в решении 0017).

### Шаблон `config.toml` и `tg config init`

- Шаблон: файл `internal/config/template.toml`, встроенный через `//go:embed`; единственный источник текста. `docs/cli/config.md` описывает команду и не копирует шаблон.
- Формат записи ключа как в образце владельца: строки комментария «что делает», `Default:`, `Values:`, `Overrides:` (если есть флаг или переменная), затем закомментированная строка `# key = <default>`. Шапка файла объясняет приоритет, права `0600` и правило «`tg` переписывает только строки `[profiles.<имя>]`».
- Отличия от образца: раздел `[files]` говорит, что база лежит в `~/.config/tg-cli/db/<профиль>/`, а остальные файлы в `<home>/<профиль>/`; ключ `home` больше не упоминает базу; упоминается, что `db/` нельзя синхронизировать. Таблица `[profiles.default]` в шаблоне не закомментирована, все её ключи закомментированы.
- `tg config init`: пишет шаблон по пути конфига под `config.lock`, `O_CREATE|O_EXCL`, `0600`; файл уже есть: код 2, файл не меняется. Ответ `{"path": "...", "created": true}`. Класс `local_write`, без флагов.
- Тест в `internal/cli`: каждый ключ из `configKeyDocs()` есть в шаблоне строкой `# <ключ> =` (для `profiles.<profile>.*` внутри `[profiles.default]`); шаблон читается `config.Load` без ошибки; после записи профиля (`config.UpdateProfile`) все строки шаблона, кроме изменённой, совпадают байт в байт.

## Scope

- Фаза 1: `internal/daemon/paths.go`, `file_guard.go`, новый `db_move.go`, `daemon.go`, `session_file.go`, `background.go` или `internal/cli/daemon_cmd.go`; `internal/store` (`Checkpoint`, `IntegrityCheck`); `internal/cli/client.go`, `config_cmd.go`, `session_cmd.go`, `schema_cmd.go`; `internal/config` (`template.toml`, `init`, имя `db`, пропуск в `migrate.go`); тесты рядом; `docs/cli/`, `docs/skill/tg-cli/SKILL.md`, `README.md`, `FEATURES.md`, `AGENTS.md:23`; changelog; файлы решений.
- Фаза 2: миграция `internal/store/migrations/0008_chats_state.sql`; `internal/store` (запросы VIEW, полные данные чата); `internal/tgclient` (флаги канала, запрос полных данных, фоновая метка дросселя); `internal/daemon` (менеджер задачи по образцу `history_sync.go`, чтение VIEW); `internal/rpc`; `internal/cli` (новые команды); `docs/cli/`, `docs/contracts.md`, навык, `FEATURES.md`; changelog; файлы решений.
- Вне плана: перенос сессии, журналов и `access.json`; ключ конфига для корня баз; перенос базы из прежнего `home`; исключение `db/` из синхронизации dotfiles (только документы и changelog); Windows; `tg db move` (отвергнута решением); вычисление состава папок по правилам (Q7); автоматическое возобновление фоновых задач после перезапуска Daemon (как у загрузки истории); новые права записи в Telegram.

## Main invariant

- История не теряется: после переноса `new` побайтно равен `old` после checkpoint, `integrity_check` = `ok`; `old` удаляется только после сверки.
- Ни одно состояние из таблицы «Состояния при старте» не создаёт пустую базу, если где-то лежит старая.
- `session.json`, `access.json`, сокет, замок и журналы не перемещаются и не меняются; повторный вход не нужен; второй клиент на сессии не запускается.
- Без старой базы поведение каждой команды прежнее, меняется только путь базы. Предел и текст ошибки сокета прежние.
- Тесты не трогают настоящие `~/.config/tg-cli` и каталог данных владельца.
- `internal/cli` не импортирует `store`, `tgclient`, gotd.
- Фаза 2: VIEW и команда её чтения не делают запросов к Telegram; фоновая задача делает только чтение, по одному запросу, под общим дросселем с фоновой меткой; скрытые списками доступа чаты не попадают ни в ответы, ни в задачу; новых прав записи нет.

## Risks and strategy

- Риск: перенос портит историю 21 МБ на живом профиле. Решение: копия во временный файл, сверка размера, `sha256`, `integrity_check`, `user_version`, `rename`, удаление `old` последним; резервная копия до первого запуска (шаг 1.25).
- Риск: новый `CheckFileNames` принимает ожидающий перенос за конфликт или пропускает конфликт. Решение: табличный тест по всем строкам «Состояния при старте» (шаг 1.7).
- Риск: `TG_CLI_HOME` агента или теста ведёт в живую базу владельца. Решение: `data-dir` и `db.lock` (Q1), корень баз в тестах из `Deps.ConfigPath`.
- Риск: `~/.config` синхронизируется, база с перепиской уезжает наружу. Решение: предупреждение в шаблоне, `docs/cli/overview.md`, `config.md` и changelog.
- Риск: откат на прежний бинарь создаёт пустую `<Dir>/store.db`. Решение: порядок отката (Deploy / rollback).
- Риск: кодер `deepseek-flash` делает за раз больше, чем просили, или пропускает проверку. Решение: шаги по одному изменению, у каждого команда проверки и ожидаемый итог; менеджер перепроверяет diff и команду перед галочкой.
- Риск: песочница кодера не создаёт Unix-сокеты. Решение: шаги помечены «песочница» или «менеджер»; тесты с сокетами и `just check` гоняет менеджер (см. Verification).
- Риск фазы 2: VIEW считает сообщения полным проходом по `messages`. Решение: частичный индекс `messages(chat_id, out) WHERE deleted_at IS NULL`, разовый замер на синтетической базе (шаг 2.4).
- Риск фазы 2: фоновая задача пишет `settings` с другим порядком ключей, триггер `chats_versions_au` (`0006_chat_versions.sql:6-16`) создаёт ложные версии. Решение: та же функция сборки `settings`, что у `chat info`; тест «повторная запись тех же значений не добавляет версию» (шаг 2.12).
- Риск фазы 2: запросы задачи идут как обычные, потому что `classifyRequest` всё незнакомое считает `General` (`internal/tgclient/throttle.go:17-47`), и забивают очередь. Решение: фоновая метка контекста, как у страниц истории (`internal/tgclient/history_page.go:32`, `throttle.go:111-146`).
- Риск фазы 2: VIEW обходит списки доступа. Решение: читать её только через `store` с `ChatFilter` (`internal/store/chat_filter.go:5-53`); агенты базу напрямую не открывают (`AGENTS.md`).

## Decision log

| Date | Decision | Reason | Confirmed |
| --- | --- | --- | --- |
| 2026-11-16 | Входные требования: [базы данных лежат в db рядом с конфигом](../decisions/20261116_0136_databases_live_in_db_next_to_the_config.md) | решение владельца | yes |
| 2026-11-16 | Q1 А: `db/<профиль>/data-dir` с путём каталога данных и `flock` на `db/<профиль>/db.lock`; несовпадение: код 2. Менеджер по поручению владельца «Решения сам прими, я не разбираюсь» | другой `TG_CLI_HOME` или смена `home` не смешают истории двух аккаунтов | no |
| 2026-11-16 | Q2 А: прерванный перенос с одинаковыми `sha256` и пустым `old-wal` доводится автоматически; пересматривает решение 0136. «Решения сам прими, я не разбираюсь» | иначе после редкой аварии нужен ручной шаг | no |
| 2026-11-16 | Q3 А: имя профиля `db` запрещено, каталог `db` пропускается при обходе профилей. «Решения сам прими, я не разбираюсь» | на Linux каталог профиля `db` совпал бы с корнем баз | no |
| 2026-11-16 | Q4 А: шаблон `config.toml` встроен в бинарь, `tg config init`, тест сверяет шаблон с `tg schema`; шаг обязательный | владелец явно просил шаблон со всеми параметрами, объяснениями, значениями по умолчанию и допустимыми значениями | yes |
| 2026-11-16 | Q5 А: поле `db_dir` в ответе `tg config show`, версия схемы без изменений. «Решения сам прими, я не разбираюсь» | `home` в ответе больше не говорит, где база | no |
| 2026-11-16 | Корень баз `filepath.Dir(<путь конфига>)/db`, без ключа и переменной; `ResolvePaths` получает его параметром, пустой корень: ошибка | решение кладёт базу рядом с конфигом; тесты не попадут в настоящий `~/.config` | no |
| 2026-11-16 | Перенос под `<Dir>/daemon.lock`: в `Run` после замка и в родителе до `Launch` | замок общий для старого и нового бинаря; большая база не съедает `StartTimeout` | no |
| 2026-11-16 | Копия через временный файл и `rename`; кроме размера и `integrity_check` ещё `sha256` и `user_version` | «`new` есть» означает полную копию; хеш ловит порчу, которую размер не видит | no |
| 2026-11-16 | `busy`, запись в `old` во время копии, нехватка места, провал сверки: код 1, `old` цел | сбой окружения, а не ошибка конфигурации | no |
| 2026-11-16 | SQL переноса в `internal/store`, файловые шаги в `internal/daemon` | `store` доступен только через `daemon` | no |
| 2026-11-16 | Смена имени и пути одновременно: код 2, без угадывания | перенос чужого файла мог бы взять не ту базу | no |
| 2026-11-16 | Пустые `old-wal`, `old-shm` после аварии удаляются молча, непустые остаются с предупреждением | непустой WAL может хранить данные | no |
| 2026-11-16 | `session import` считает профиль непустым и по `new`, и по `old` | иначе импорт без `--force` прошёл бы поверх неперенесённой базы | no |
| 2026-11-16 | `tg config init` при существующем файле: код 2, без `--force` | шаблон не должен затирать правки владельца | no |
| 2026-11-16 | Фаза 2 в этом плане и на этой ветке, после фазы 1 | план около 400 строк, меньше предела 1500; одна ветка на план | no |
| 2026-11-16 | Флаги канала (`date`, `scam`, `fake`, `restricted`, `restriction_reason`) берутся из объекта `tg.Channel`/`tg.Chat`, который уже приходит в `chat list` и `chat info`; полный запрос нужен только для `about`, медленной отправки, связанного чата и числа участников | меньше запросов к Telegram | no |
| 2026-11-16 | Новые столбцы `chats` не входят в триггер версий | версии хранят то, что владелец видит как настройки; флаги меняются редко и видны в `chats` | no |
| 2026-11-16 | Прогресс задачи полных данных хранится в `chats.full_synced_at` и `chats.full_error`; задача в памяти, возобновление явным `start`, как у загрузки истории | образец решения 1353, без новой таблицы задач | no |
| 2026-11-16 | Частичный индекс `messages(chat_id, out) WHERE deleted_at IS NULL` в миграции 0008 | VIEW считает все и мои сообщения без полного прохода по таблице | no |

Решения с `Confirmed: no`, пережившие план, получают файлы решений (шаги 1.24 и 2.18).

## Step 0. Fix decisions before changes

- [x] Q1–Q5 решены 2026-11-16 (вариант А везде), записаны в Decision log.
- [x] План утверждён менеджером по поручению владельца 2026-11-16, статус `approved`.
- [ ] Менеджер: worktree `tg-cli-db_next_to_config` подтянут к свежему `main`, кодер `pi` запущен в панели потока с `--cwd` worktree.
- [ ] Менеджер: Q6–Q9 закрыты до шага 2.1.

## Фаза 1. База рядом с конфигом и шаблон

Каждый шаг отдельным коммитом менеджера после его проверки. «Песочница»: кодер запускает проверку сам. «Менеджер»: тесты поднимают Daemon на Unix-сокете, кодер их не запускает и пишет об этом в отчёте.

### Step 1. Audit and baseline

- [x] **1.1** (кодер, песочница) Перепроверить таблицу «Где строится и открывается путь базы» по коду ветки, расхождения в отчёт. Проверка: `rg -n 'Paths\.Database|store\.db|ResolvePaths\(' cmd internal` совпадает со списком аудита.
- [x] **1.2** (кодер, песочница) Подтвердить, что `tgclient.NewWithNetwork` и всё до `tg.Run` не вызывают `StateKV.Get`/`Set`. Проверка: ссылки `path:line` в отчёте, менеджер переносит итог в Known facts.
- [x] **1.3** (менеджер) `just check` на чистой ветке, итог и время в Known facts. Ожидается зелёный.

### Implementation steps

- [x] **1.4** (кодер, песочница) `ResolvePaths(home, dbRoot, profile, files)`, поля `Paths.DatabaseDir`, `Paths.LegacyDatabase`; `Database` указывает в `<dbRoot>/<профиль>/`; пустой `dbRoot`: ошибка. Все вызовы в коде и тестах исправлены механически: в тестах корень внутри своего временного каталога, не `<home>/db`; в `internal/cli` временно `filepath.Join(filepath.Dir(a.deps.ConfigPath), "db")` прямо в `profile` и `configShow`. `TestResolvePaths` дополнен случаями: нестандартный `files.database`, пустой корень. Проверка: `go build ./... && go vet ./...` код 0; `go test ./internal/daemon -run TestResolvePaths` зелёный.
- [x] **1.5** (кодер, песочница) Запрет имени профиля `db`: regexp-проверка в `ResolvePaths` и в `internal/config` (`config.go:83`) отказывает с понятным текстом. Проверка: `go test ./internal/config/... ./internal/daemon -run 'TestResolvePaths|TestLoad'`; тест на `db` даёт ошибку.
- [x] **1.6** (кодер, песочница) `DuplicateProfiles` (`session_file.go:61-63`) и обход профилей в `internal/config/migrate.go` пропускают каталог `db`. Проверка: `go test ./internal/config/...`; тест: в `home` есть `db/` с файлом `client`, миграция его не трогает; юнит-тест `DuplicateProfiles` без сокета: `db/daemon.lock` не создан.
- [x] **1.7** (кодер, песочница) `CheckFileNames` по разделу «Состояния при старте» и «Проверка смены имени смотрит две папки»; ожидающий перенос не ошибка. Проверка: `go test ./internal/daemon -run TestCheckFileNames`; табличный тест по пяти строкам таблицы состояний и двум случаям смены имени; ни в одном отказе `tg.db` не создан.
- [x] **1.8** (кодер, песочница) `store.Checkpoint(path) (busy bool, err error)` и `store.IntegrityCheck(path) (ok bool, detail string, err error)` без миграций схемы. Проверка: `go test ./internal/store -run 'TestCheckpoint|TestIntegrityCheck'`: после записи в WAL и `Checkpoint` файл `-wal` пуст или удалён; испорченная страница даёт не `ok`.
- [x] **1.9** (кодер, песочница) `daemon.MoveLegacyDatabase`: шаги 1-4 раздела «Перенос» (хвосты, каталоги, checkpoint, копия во временный файл). Пока без `rename` и удаления. Проверка: `go test ./internal/daemon -run TestMoveLegacyDatabase`: временный файл равен `old` по `sha256`, права `0700`/`0600`, `old` не тронут.
- [x] **1.10** (кодер, песочница) Шаги 5-7: сверка, `rename`, удаление `old` и пустых хвостов, `fsync` каталогов. Проверка: тот же тест, случаи: (а) база с данными в WAL переносится, строки на месте, `old`, `old-wal`, `old-shm` удалены; (б) второй вызов ничего не меняет; (в) подменённая сверка: `old` цел, временного файла нет, код 1; (г) хвост `.move-*.tmp` удаляется.
- [x] **1.11** (кодер, песочница) Оба файла есть: одинаковые `sha256` и пустой `old-wal` доводят перенос, разные дают `FileConflictError` (код 2). Проверка: `go test ./internal/daemon -run TestMoveLegacyDatabase`, случаи (д) одинаковые, (е) разные, оба файла целы в (е).
- [x] **1.12** (кодер, песочница) Сторонний процесс держит `old` с незавершённой записью: код 1, `old` цел. Проверка: случай (ж) того же теста с открытым соединением, которое пишет.
- [ ] **1.13** (кодер, без проверки сокетами) Вызов в `Run`: после `acquireLock` перенос, затем `MkdirAll(DatabaseDir, 0700)` и `Chmod`, затем прежний порядок. Проверка песочницы: `go build ./... && go vet ./...`. Проверка менеджера: `go test ./internal/daemon -run 'TestRunDatabaseFailureBeforeSocket|TestFilePermissions|TestSecondDaemon'`; `TestFilePermissions` дополнен `DatabaseDir` (`0700`) и файлом базы (`0600`).
- [ ] **1.14** (кодер, песочница) `data-dir` (Q1): пишется при создании или переносе базы, читается до `store.Open`; несовпадение: код 2 с обоими путями. Проверка: `go test ./internal/daemon -run TestDatabaseBinding` на функции без Daemon: два каталога данных, один корень баз; второй получает код 2, база не открыта.
- [ ] **1.15** (кодер, без проверки сокетами) `flock` на `db/<профиль>/db.lock` на всё время `Run`. Проверка менеджера: тест двух Daemon с разными `home` и одним профилем: второй выходит с «already running», база открыта один раз.
- [ ] **1.16** (кодер, без проверки сокетами) `startBackground` вызывает перенос под `WithProfileLock` после пинга и до `Launch`. Проверка менеджера: `go test ./internal/cli -run TestBackground` и новый тест «старая база переносится до запуска потомка».
- [ ] **1.17** (кодер) `session import` проверяет `new` и `old`. Проверка менеджера: `go test ./internal/cli -run TestSessionTransfer`; новый случай «старая база в каталоге данных, импорт без `--force`: код 2».
- [ ] **1.18** (кодер, песочница) Поле `db_dir` в ответе `tg config show`; `configKeyDocs` описывает `files.database` с каталогом `db/<профиль>/`. Проверка: `go test ./internal/cli -run 'TestConfigShow|TestSchema'`; если `TestConfigShow` поднимает Daemon, его гоняет менеджер.
- [ ] **1.19** (кодер, песочница) `internal/config/template.toml` по разделу «Шаблон» (текст по образцу владельца, с новым местом базы) и `config.Template()` через `//go:embed`. Проверка: `go test ./internal/config/... -run TestTemplate`: шаблон читается `config.Load` без ошибки, значения по умолчанию.
- [ ] **1.20** (кодер, песочница) Тест сверки шаблона с `tg schema`: каждый ключ `configKeyDocs()` есть в шаблоне. Проверка: `go test ./internal/cli -run TestConfigTemplateCoversSchema` зелёный; временно удалённая строка ключа из шаблона даёт красный (проверить и вернуть).
- [ ] **1.21** (кодер, песочница) Тест сохранения комментариев: шаблон, затем `config.UpdateProfile(path, "default", {"device": "X"})`: файл отличается от шаблона одной строкой `device`. Проверка: `go test ./internal/config/... -run TestTemplateKeepsComments`.
- [ ] **1.22** (кодер, песочница) Команда `tg config init` по разделу «Шаблон», раздел `tg config init` в `docs/cli/config.md`, блок флагов (`go test ./internal/cli -run TestDocsGenerated -update-docs`). Проверка: `go test ./internal/cli -run 'TestConfigInit|TestDocs|TestSchema|TestEveryCommand'`: второй вызов даёт код 2, файл не изменён, права `0600`.
- [ ] **1.23** (`docwriter` и `glavred` по `docs_chain.md`) `docs/cli/overview.md` (раскладка, таблица файлов, предупреждение про синхронизацию `~/.config`), `docs/cli/config.md`, `docs/cli/notes-and-search.md:5`, `docs/cli/access.md:9`, `docs/skill/tg-cli/SKILL.md:32`, `README.md:23,60`, `FEATURES.md:612`, `AGENTS.md:23`. Проверка: `go test ./internal/cli -run TestDocs`, вердикт `glavred` `pass`.
- [ ] **1.24** (менеджер) Журналы: `just changelog add` по-английски, тип `Изменено`, решение 0136, действие оператора «exclude `~/.config/tg-cli/db` from dotfiles sync»; вторая запись тип `Добавлено` про `tg config init`; файлы решений для строк Decision log с `Confirmed: no` (Q1, Q2 с `revises: 20261116_0136_databases_live_in_db_next_to_the_config`, Q3, корень баз), строки в `_TOC.md`. Затем `just check` и `ls -laR ~/.config/tg-cli` до и после. Проверка: `just check` зелёный, `just changelog list` показывает записи, `~/.config/tg-cli` без изменений.
- [ ] **1.25** (менеджер, только с явного согласия владельца) Резервная копия и живой перенос. (1) `tg daemon stop`; `ls -la` каталога профиля в `var/agents/plans/20261116_0136_db_next_to_config/profile-before.txt` без содержимого файлов; `mkdir -m 700 "$HOME/Library/Application Support/tg-cli/.db-move-backup-<YYYYMMDDTHHMMSS>"`; `sqlite3 "<профиль>/store.db" ".backup '<копия>/store.db'"`; `chmod 600`; `sqlite3 -readonly '<копия>/store.db' 'PRAGMA integrity_check'` даёт `ok`; число строк таблиц `chats`, `messages`, `people` рядом со снимком. Копия в каталоге данных, не в `~/.config`. (2) Новый `dist/tg`, `tg daemon start`: `<профиль>/store.db` нет, `~/.config/tg-cli/db/default/store.db` есть, каталоги `0700`, файл `0600`, в журнале строка о переносе, числа строк совпали, `tg auth status` тот же пользователь без нового входа. Только чтение, без записи в Telegram, сессию и `access.json` не открывать.

## Фаза 2. Данные чатов

Начинается после шага 1.24 на той же ветке; номер миграции `0008` (последняя сейчас `0007_transcriptions.sql`). Поведение шагов зависит от Q6–Q9; описано по рекомендациям.

### Что уже есть (аудит)

- `dialog_state` (`0005_folders_account_state.sql:8`): `chat_id`, один JSON-столбец `data` (`rpc.ChatState`, `internal/rpc/owner_state.go:58-74`: `unread`, `unread_mentions`, `unread_reactions`, `unread_mark`, `pinned`, `archived`, `top_message`, `mute_until`, `silent`, `show_previews`, `ttl_period`, `read_*_max_id`), `synced_at`. Отдельных столбцов нет, VIEW берёт поля через `json_extract`. Пишут: `chat state` (`internal/daemon/owner_state.go:136,159-161`), `chat unread` (только непрочитанные, `internal/daemon/unread.go:43-53,68-70`), `message unread`, закрепление, архив, уведомления (`internal/daemon/dialogs.go:77-83,115-121`). Периодического обновления нет, строки есть не у всех чатов: нужен `LEFT JOIN` и поле свежести. Правило «muted» уже есть: `internal/tgclient/unread.go:52`. Архив: `folder_id == 1` (`internal/tgclient/owner_state.go:25-32`).
- `chats` (`0002_schema.sql:1-16`): `id`, `type`, `title`, `username`, `about`, `members`, `forum`, `verified`, `unread`, `settings` (JSON), `raw` (не пишется), `search_text`, `first_seen_at`, `last_seen_at`. `messages` (`0002_schema.sql:38-58`): `chat_id`, `id`, `date`, `out`, `text`, `deleted_at` и другие; индекс только `messages_chat_date_idx(chat_id, date DESC)` (`:60`). `sync_state` (`0002_schema.sql:94-100`): `complete` по чату.
- `folder list` без `--local` уже пишет состав папок: `messages.getDialogFilters` (`internal/tgclient/folders.go:125`), разбор `DialogFilter` с правилами и списками `pinned`, `include`, `exclude`, `DialogFilterChatlist` только `pinned` и `include` (`folders.go:80-119`), сохранение одной транзакцией `store.ReplaceFolders` (`internal/store/folders.go:52-99`, вызов `internal/daemon/owner_state.go:89-91`), скрытые чаты пропускаются (`folders.go:66`). При одновременном `pinned` и `include` остаётся `pinned` (`ON CONFLICT DO NOTHING`, `:84-93`).
- `chat info` (`internal/daemon/read.go:76-97`, `internal/tgclient/peers.go:171-209`): для канала `channels.getFullChannel` через `peers.Channel.FullRaw`, для группы `messages.getFullChat`. Отдаёт `rpc.ChatInfo` (`internal/rpc/api.go:228-238`): `id`, `type`, `title`, `username`, `about`, `members`, `forum`, `verified`, `settings`. `settings` канала (`internal/tgclient/chat_settings.go:64-82`): `no_forwards`, `slow_mode_seconds`, `linked_chat_id` и другие; группы (`:50-63`): `no_forwards` и другие. Сохраняет в `chats` (`internal/daemon/persist.go:97-106`). Своего кэша полных данных нет.
- Глубокая загрузка истории (`internal/daemon/history_sync.go`): один рабочий, одна задача за раз, пауза 2 с (`:16,339-347`), состояния `idle`, `queued`, `running`, `waiting_flood`, `done`, `failed`, `cancelled` (`internal/rpc/api.go:347-371`), отмена контекстом, проверка доступа на каждой странице, прогресс в `sync_state`, задачи в памяти, возобновление явным `start`. Фоновая метка дросселя: `internal/tgclient/history_page.go:32`, `throttle.go:111-146`, очередь `internal/throttle/throttle.go:193-240`. Команды `chat sync start|status|cancel` (`internal/cli/chat_sync_cmd.go:11-35`), `start` и `cancel` класса `local_write`, `status` класса `read`.

### Поля полных данных

| Поле | Откуда приходит | Есть сегодня | Где хранится | Миграция |
| --- | --- | --- | --- | --- |
| дата создания | `tg.Channel.Date`, `tg.Chat.Date` (объект чата в ответе `messages.getDialogs` и при разрешении) | нет | `chats.created_at INTEGER` | да, 0008 |
| verified | `tg.Channel.Verified` | да, `peers.go:177`, `chats.verified` | `chats.verified` | нет |
| scam | `tg.Channel.Scam` | только для людей (`users_read.go:59`) | `chats.scam INTEGER NOT NULL DEFAULT 0` | да |
| fake | `tg.Channel.Fake` | только для людей | `chats.fake INTEGER NOT NULL DEFAULT 0` | да |
| ограничения | `tg.Channel.Restricted`, `RestrictionReason` (`platform`, `reason`, `text`) | нет | `chats.restricted INTEGER NOT NULL DEFAULT 0`, `chats.restriction_reason TEXT` (JSON) | да |
| медленная отправка | `tg.ChannelFull.SlowmodeSeconds` (полный запрос) | да, `settings.slow_mode_seconds` через `chat info` | `chats.settings` | нет |
| связанный чат | `tg.ChannelFull.LinkedChatID` (полный запрос) | да, `settings.linked_chat_id` | `chats.settings` | нет |
| запрет пересылки | `tg.Channel.Noforwards`, `tg.Chat.Noforwards` | да, `settings.no_forwards` | `chats.settings` | нет |
| число участников | `tg.Channel.ParticipantsCount`, иначе `tg.ChannelFull.ParticipantsCount`; для группы `ChatFull` | да, `peers.go:188,196,204` | `chats.members` | нет |
| когда загружено, ошибка | задача | нет | `chats.full_synced_at INTEGER`, `chats.full_error TEXT` | да |

### Контракт фазы 2

- Миграция `0008_chats_state.sql`: семь столбцов `chats` из таблицы выше; индекс `messages_chat_out_idx ON messages(chat_id, out) WHERE deleted_at IS NULL`; VIEW `chats_state`:
  - из `chats`: `chat_id`, `type`, `title`, `username`, `created_at`;
  - из `dialog_state` (`LEFT JOIN`): `muted` (`COALESCE(json_extract(data,'$.mute_until'),0) > CAST(strftime('%s','now') AS INTEGER)`), `mute_until`, `archived`, `pinned`, `unread`, `unread_mentions`, `unread_mark`, `state_synced_at`;
  - из `messages` без удалённых (`deleted_at IS NULL`): `messages`, `my_messages` (`SUM(out)`), первое и последнее сохранённое сообщение (`id`, `date`, `text`) по `date`, затем `id`;
  - из `sync_state`: `history_complete` (без него «первое сообщение» значит первое сохранённое, а не первое в чате).
- VIEW читается только через `store` с `ChatFilter`; Q6 А: команда `chat overview` (класс `read`, `--limit` 1..500, `--type`), JSONL по строке на чат, ответ без обращения к Telegram даже при работающем соединении.
- Флаги из `tg.Channel`/`tg.Chat` пишутся в `chats` при каждом сохранении чата (`chat list`, `chat info`, разрешение). `rpc.ChatInfo` получает поля `created_at`, `scam`, `fake`, `restricted`, `restriction_reason` (Q9 А, поле добавляется, версия схемы прежняя).
- Задача полных данных (Q8 А: `chat full start [<chat>] [--refresh]`, `chat full status`, `chat full cancel`): без чата берёт все разрешённые чаты типов `group`, `supergroup`, `channel` с пустым `full_synced_at` (с `--refresh` все); один рабочий, один запрос за раз (`channels.getFullChannel` или `messages.getFullChat` по сохранённому пиру, без разрешения имени), фоновая метка дросселя, пауза 2 с, `FLOOD_WAIT` переводит в `waiting_flood`; проверка доступа перед каждым чатом; запись `about`, `members`, `settings`, `full_synced_at`; ошибка вроде `CHANNEL_PRIVATE` пишется в `full_error`, чат пропускается, следующий `start` без `--refresh` его не повторяет. Прогресс: `total`, `done`, `failed`, `current_chat_id`, `state`, `wait_seconds`, `started_at`, `updated_at`. Задача в памяти; после перезапуска Daemon `start` продолжает с чатов без `full_synced_at`. `start` и `cancel` класса `local_write`, `status` класса `read`.

### Шаги фазы 2

- [ ] **2.1** (кодер, песочница) Проверить `json_extract` и частичный индекс в `modernc.org/sqlite v1.62.0` разовым тестом в `internal/store` (не коммитить, если он ничего не защищает). Проверка: вывод теста в отчёте; при отказе статус фазы `blocked`, вопрос владельцу.
- [ ] **2.2** (кодер, песочница) Миграция 0008: столбцы `chats` и индекс, без VIEW. Проверка: `go test ./internal/store`: новая база получает версию 8; база версии 7 из фикстуры обновляется, строки на месте.
- [ ] **2.3** (кодер, песочница) VIEW `chats_state` в той же миграции до её коммита. Проверка: `go test ./internal/store -run TestChatsStateView`: чат без `dialog_state` (поля `NULL`, `muted` 0), заглушённый навсегда (`mute_until` 2147483647), заглушка в прошлом, архив, удалённые сообщения не считаются, `my_messages`, первое и последнее, `history_complete`.
- [ ] **2.4** (кодер, песочница) Разовый замер: синтетическая база 100 000 сообщений в 500 чатах, `EXPLAIN QUERY PLAN` и время полного `SELECT * FROM chats_state`. Проверка: план использует `messages_chat_out_idx` и `messages_chat_date_idx`, время в отчёте; больше 500 мс: менеджер решает про переписывание VIEW.
- [ ] **2.5** (кодер, песочница) `store.ChatsOverview(ctx, filter ChatFilter, limit int, chatType string)` поверх VIEW. Проверка: `go test ./internal/store -run TestChatsOverview`: скрытый фильтром чат не возвращается.
- [ ] **2.6** (кодер) RPC `chat.overview`, обработчик Daemon, команда `chat overview` с аннотацией и разделом `docs/cli/` (блок флагов). Проверка песочницы: `go test ./internal/cli -run 'TestDocs|TestSchema|TestEveryCommand'`. Проверка менеджера: тест команды с `startDaemon`: ответ без вызовов поддельного Telegram.
- [ ] **2.7** (кодер, песочница) Тест-закрепление `folder list`: поддельный ответ `getDialogFilters` с правилами, `pinned`, `include`, `exclude`, чатлистом и скрытым чатом даёт ожидаемые строки `folders` и `folder_chats`. Если такой тест уже есть, сослаться на него и шаг закрыть. Проверка: `go test ./internal/store -run TestReplaceFolders` или имя существующего теста.
- [ ] **2.8** (кодер, песочница) Чтение флагов `date`, `scam`, `fake`, `restricted`, `restriction_reason` из `tg.Channel` и `tg.Chat` в разборе чата `internal/tgclient` (место по `peers.go` и разбору диалогов). Проверка: `go test ./internal/tgclient -run TestChatFlags` на поддельных объектах.
- [ ] **2.9** (кодер, песочница) Запись флагов в `chats` в `store` и `persist.go`. Проверка: `go test ./internal/store ./internal/daemon -run 'TestSaveChat'` без сокетов; запись флагов не добавляет строку в `chat_versions`.
- [ ] **2.10** (кодер) Поля флагов в `rpc.ChatInfo` и в ответе `chat info`; `tg schema` показывает их. Проверка песочницы: `go test ./internal/cli -run 'TestSchema|TestDocs'`; менеджер: тест `chat info` с `startDaemon`.
- [ ] **2.11** (кодер, песочница) `tgclient.ChatFull(ctx, chatID)`: запрос по сохранённому пиру без разрешения имени, контекст с фоновой меткой, разбор через те же функции, что `chat info` (`chat_settings.go`). Проверка: `go test ./internal/tgclient -run 'TestChatFull|TestThrottle'`: поддельный вызыватель видит один запрос с фоновой меткой.
- [ ] **2.12** (кодер, песочница) `store.ChatsForFullSync(filter, refresh)` и `store.SaveChatFull`. Проверка: `go test ./internal/store -run 'TestChatsForFullSync|TestSaveChatFull'`: повторная запись тех же значений не добавляет версию; `full_error` пишется и исключает чат без `refresh`.
- [ ] **2.13** (кодер, песочница) Менеджер задачи в `internal/daemon` по образцу `history_sync.go`: очередь, один рабочий, пауза, отмена, состояния, проверка доступа перед чатом. Проверка: `go test ./internal/daemon -run TestChatFullSync` на структуре без Daemon и сокета, с поддельным Telegram: три чата, один `CHANNEL_PRIVATE`, один скрыт доступом; итог `done=1`, `failed=1`, скрытый не запрошен; отмена посреди останавливает после текущего чата.
- [ ] **2.14** (кодер, песочница) `FLOOD_WAIT` в задаче: `waiting_flood`, ожидание, продолжение. Проверка: тот же тест, случай с поддельной ошибкой `FLOOD_WAIT_3`.
- [ ] **2.15** (кодер) RPC `chat_full.start|status|cancel`, регистрация в `daemon.go`, команды `chat full ...` с аннотациями и разделом `docs/cli/`. Проверка песочницы: `go test ./internal/cli -run 'TestDocs|TestSchema|TestEveryCommand'`. Проверка менеджера: тест с `startDaemon`: `start`, `status` до `done`, перезапуск Daemon, `start` пропускает загруженные чаты.
- [ ] **2.16** (`docwriter` и `glavred`) `docs/contracts.md` (VIEW, задача, флаги), `docs/cli/`, `docs/skill/tg-cli/` по-русски в ногу с `tg schema`, `FEATURES.md` (новые пункты, следующий свободный номер в разделе). Проверка: `go test ./internal/cli -run TestDocs`, `glavred` `pass`.
- [ ] **2.17** (менеджер) `just check` и `go test -race ./internal/daemon ./internal/store ./internal/tgclient`. Проверка: зелёные.
- [ ] **2.18** (менеджер) Журналы: changelog (`Добавлено`: `chat overview`, `chat full`; `Изменено`: поля `chat info`), файлы решений для строк Decision log фазы 2 и ответов Q6–Q9, строки в `_TOC.md`. Проверка: `just changelog list`.

## Verification

| Check | Command | Expected result | Кто | Status |
| --- | --- | --- | --- | --- |
| Формат и сборка | `gofmt -l cmd internal`, `go vet ./...` | пусто, код 0 | кодер | not_run |
| Пути и проверка имени | `go test ./internal/daemon -run 'TestResolvePaths|TestCheckFileNames'` | зелёный | кодер | not_run |
| Перенос | `go test ./internal/store ./internal/daemon -run 'TestMoveLegacyDatabase|TestCheckpoint|TestIntegrityCheck|TestDatabaseBinding'` | зелёный, случаи а-ж | кодер | not_run |
| Конфиг и шаблон | `go test ./internal/config/...`, `go test ./internal/cli -run 'TestConfigTemplateCoversSchema|TestConfigInit'` | зелёный | кодер | not_run |
| Документы и схема | `go test ./internal/cli -run 'TestDocs|TestSchema|TestEveryCommand'` | зелёный | кодер | not_run |
| Daemon на сокете | `go test ./internal/daemon -run 'TestFilePermissions|TestSecondDaemon|TestRunDatabaseFailureBeforeSocket'`, `go test ./internal/cli -run 'TestBackground|TestSessionTransfer|TestConfigShow'` | зелёный | менеджер | not_run |
| Полный прогон | `just check` | зелёный | менеджер | not_run |
| Тесты не трогают домашний конфиг | `ls -laR ~/.config/tg-cli` до и после `just check` | без изменений, нет `db/` от тестов | менеджер | not_run |
| Живой перенос | шаг 1.25 | база в новом месте, строки совпали, вход сохранён | менеджер с согласия владельца | not_run |
| VIEW и индекс | `go test ./internal/store -run 'TestChatsStateView|TestChatsOverview'`, замер 2.4 | зелёный, индекс в плане | кодер | not_run |
| Задача полных данных | `go test ./internal/daemon ./internal/tgclient -run 'TestChatFullSync|TestChatFull'` | зелёный | кодер | not_run |
| Команды фазы 2 на сокете | тесты шагов 2.6, 2.10, 2.15 со `startDaemon` | зелёный | менеджер | not_run |

Песочница кодера не создаёт Unix-сокеты, поэтому не может запустить: тесты с `start(t, …)` в `internal/daemon/daemon_test.go` и `idle_test.go`, `background_test.go`, `TestSocketPathLimitMatchesTheOS`; `startDaemon` в `internal/cli/cli_test.go` и все тесты, которые его вызывают (`background_cmd_test.go`, `schema_fields_test.go`, `note_cmd_test.go`, `output_cmd_test.go`, тесты команд фазы 2); `internal/rpc/conn_lifecycle_test.go`; `just check` целиком. Их гоняет менеджер после каждого шага с пометкой «менеджер», кодер перечисляет в отчёте, что не запускал.

## Deploy / rollback

- [ ] Выкатка фазы 1: шаг 1.25. Работающий Daemon старого бинаря останавливается первым.
- [ ] Откат фазы 1: `tg daemon stop`; `db/default/store.db-wal` нет или пуст; `mv ~/.config/tg-cli/db/default/store.db "$HOME/Library/Application Support/tg-cli/default/store.db"`; удалить `db/default/data-dir`; прежний бинарь; `tg daemon start`. Файл возвращается целиком, история после переноса сохраняется. Повреждённый файл заменяется копией из `.db-move-backup-*` (теряются записи после копии).
- [ ] Порядок важен: прежний бинарь, запущенный до возврата файла, создаст пустую `<профиль>/store.db`, и новый бинарь откажет кодом 2. Пустой файл удаляется руками после проверки, что в нём нет строк.
- [ ] Откат фазы 2: прежний бинарь откажет открыть базу версии 8 (`store.go:67-69`). Отката схемы нет; возврат только из резервной копии до первого запуска нового бинаря. Поэтому перед первым запуском бинаря с фазой 2 на живом профиле менеджер делает такую же копию, как в шаге 1.25 (1), с согласия владельца.
- [ ] Сессии: `session.json` не трогается, повторный вход не нужен.

## Cleanup

- [ ] Временные файлы только в `var/agents/plans/20261116_0136_db_next_to_config/` (снимок `profile-before.txt`, числа строк, замер 2.4, отчёты кодера).
- [ ] Временные файлы не в git.
- [ ] Резервные копии `.db-move-backup-*` остаются у владельца.

## Definition of done

- [ ] Код изменён только в пределах Scope.
- [ ] Main invariant соблюдён, `just check` зелёный у менеджера.
- [ ] Живой перенос прошёл или отложен с записанной причиной.
- [ ] Документы и навык в ногу с `tg schema`, `glavred` `pass`.
- [ ] Записи changelog и файлы решений есть.
- [ ] Нет шагов `- [0]`, нет временных файлов в `.agents/plans`.

## Known facts

- `os.UserConfigDir()` на macOS даёт `~/Library/Application Support`, на Linux `$XDG_CONFIG_HOME` или `~/.config` (документация Go, не запускалось). Путь конфига в коде всегда `~/.config/tg-cli/config.toml` (`internal/cli/cli.go:153-160`).
- В коде нет обращений к подкаталогам `~/.config/tg-cli`, кроме `config.toml`, `config.lock` и временных `.config-*.tmp` (`internal/config/write.go:42-55,162-186`).
- Нет помощников для `wal_checkpoint`, `integrity_check`, копирования файлов и `fsync` каталога. `writeAtomic` и `WriteSession` делают `CreateTemp`, `Sync`, `Rename` без синхронизации каталога.
- `store.Open` не создаёт родительский каталог и создаёт файл с `O_EXCL` (`internal/store/store.go:40-57`).
- `tgclient.New...` только сохраняет `StateKV` (`internal/tgclient/client.go:90`); чтение курсоров начинается в `tg.Run` (по разведке, шаг 1.2).
- `tg config show` печатает значения файла, а не вычисленные пути (`internal/cli/config_cmd.go:59-91`).
- Профиль `db` сегодня допустим (`internal/daemon/paths.go:19`, `internal/config/config.go:83`). `DuplicateProfiles` создаёт `daemon.lock` в найденных каталогах (`session_file.go:68`).
- Ответ команды в stdout всегда один JSON-конверт, поэтому `tg config init` пишет файл, а не печатает шаблон.
- Образец шаблона владельца написан до переноса базы: в нём `home` описан как место базы, а `[files]` как «inside `<home>/<profile>/`»; шаблон в бинаре это исправляет.
- `go.mod`: `modernc.org/sqlite v1.62.0`, `github.com/gotd/td v0.166.0`. `json_extract` в репозитории не используется, `json_valid` используется в `CHECK`.
- Миграции схемы: `internal/store/migrations/0001`…`0007`, номера подряд, отката нет; база новее кода не открывается (`store.go:67-69`). Ни одного `CREATE VIEW`.
- `chats.raw` не заполняется (разведка, `rg` по `persist.go`).
- В плане 0020 номера строк `daemon.go:107` и `:131` устарели: сейчас `:128` и `:152`.

## Open questions

| # | Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| Q1 | Как не дать двум каталогам данных с одним профилем открыть одну базу | шаги 1.14-1.15 | А. `data-dir` и `db.lock`. Б. Только `db.lock`. В. Только документы | А | decided 2026-11-16: А |
| Q2 | Авария после `rename` и до удаления `old` | шаг 1.11 | А. Доводить при одинаковых `sha256`. Б. Код 2 по букве решения | А | decided 2026-11-16: А |
| Q3 | Профиль `db` на Linux | шаги 1.5-1.6 | А. Запретить и пропускать. Б. Только пропускать. В. Ничего | А | decided 2026-11-16: А |
| Q4 | `tg config init` и шаблон со всеми ключами | шаги 1.19-1.22 | А. Команда, шаблон в бинаре, тест против `tg schema`. Б. Шаблон в документе. В. Не делать | А | decided 2026-11-16: А, подтверждено владельцем |
| Q5 | Путь базы в `tg config show` | шаг 1.18 | А. Поле `db_dir`. Б. Не показывать | А | decided 2026-11-16: А |
| Q6 | Кто читает VIEW `chats_state` | шаги 2.5-2.6; агенты не открывают базу сами, без команды VIEW доступна только владельцу через `sqlite3` | А. Новая команда `chat overview` (локальное чтение через `ChatFilter`). Цена: команда, RPC, раздел `docs/cli/`, около 150 строк. Б. Только VIEW. Цена: агенты её не видят, списки доступа не применяются при ручном SQL. В. Поля добавить в `chat state --local`. Цена: меняется смысл ответа существующей команды (сегодня это состояние диалога из Telegram) | А | open |
| Q7 | Считать ли состав папок по правилам (`contacts`, `groups`, `exclude_muted` и другие), а не только явные списки | шаг 2.7; правила Telegram не возвращает списком чатов | А. Только явные `pinned`, `include`, `exclude`, как сейчас; правила лежат в `folders.rules`. Цена: «все группы» в папке не видны как строки. Б. Вычислять по правилам из `chats`, `people`, `dialog_state`. Цена: около 150 строк, результат неточен при устаревшем `dialog_state` | А | open |
| Q8 | Имена и охват команд задачи полных данных | шаги 2.13-2.15, контракт `tg schema` | А. `chat full start [<chat>] [--refresh]`, `chat full status`, `chat full cancel`; без чата все разрешённые группы и каналы без `full_synced_at`. Цена: новая группа команд. Б. Флаг `chat info --all` с синхронным обходом. Цена: команда висит минуты, нет отмены и прогресса. В. Встроить в `chat sync`. Цена: смешение двух задач с разными состояниями | А | open |
| Q9 | Показывать ли новые флаги в ответе `chat info` | шаг 2.10 | А. Добавить поля `created_at`, `scam`, `fake`, `restricted`, `restriction_reason`. Цена: правка `rpc.ChatInfo`, документов, навыка. Б. Только в базе и `chat overview`. Цена: агент не видит `scam` и `fake` у одного чата без обзора всех | А | open |
