# Пошаговый план: ежемесячный экспорт с манифестом SHA-256 и отказ от Takeout

ID: 20261115_1747_history_export
Created: 2026-11-15
Owner: Мязин Евгений
Branch: feature/20261115_1747_history_export
PR: —

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: draft
- Last update: 2026-11-15
- Next safe step: все вопросы решены (Q1 = C, Q2 до Q4 сняты, Q5 до Q10 = A); менеджер ставит `approved` и вносит тег `[Wont]` для 14.2 в `FEATURES.md` (текст в Decision log); затем шаг 1 (аудит, Codex Terra в worktree `../tg-cli-history_export`).

## Context and sources

- Read: `AGENTS.md`, `INTENT.md`, `FEATURES.md` (13.4, 14.1, 14.2, 10.4, 10.12, строки статуса и открытые вопросы), `.agents/rules/plans.md`, решения `20261115_1800_remaining_could_features_are_split_into_take_wont_and_defer.md`, `20261114_0310_throttle_one_account_gate_60_percent_three_levels.md`, `20261114_1353_deep_history_sync_is_a_background_job_behind_the_shared_throttle.md`; план `20261115_0439_media_export.md` как образец; `docs/contracts.md` (deep history load, local export, media downloads).
- Код (отчёт разведчика `pathfinder` и чтение): `internal/cli/export_cmd.go:12-126` (`message export --local`, только stdout), `internal/daemon/export_local.go:19-95`, `internal/store/export.go:12-81` (`ExportMessages`, `Since`, `Until`, страница 500), `internal/daemon/read.go:20` (`maxPageBytes = 768 KiB`), `internal/daemon/history_sync.go` (один рабочий, пауза 2 с, задачи в памяти), `internal/store/sync_state.go`, `internal/store/migrations/0002_schema.sql:94-100` (`sync_state`), `internal/access/access.go:96-104` (`DownloadRoots`, `DownloadsAllowed`), `internal/fileroot/create.go:12-103` (`OpenDownloadDir`, `DownloadDir.Write`: `os.Root`, временный `.<имя>.part`, 0600, `Link` без перезаписи), `internal/daemon/download.go:34-37`, `internal/daemon/account_guard.go:61` (`accountMethod`: `message.*` проверяется), `internal/daemon/daemon.go:306-379` (`register`), `internal/daemon/access_test.go:63`, `internal/store/store.go:132-190` (миграции, последняя `0006_chat_versions.sql`), `internal/exitcode/exitcode.go`, `internal/cli/read_cmd.go:106` (подкоманды `message`).
- `tg schema` (бинарь собран из этого worktree в scratchpad): `message export` (`read`, флаги `local`, `since`, `until`, `include-deleted`, `limit`, `after`), `chat sync start|status|cancel`, `message download` (`local_write`, `--output`, `--dry-run`), `chat archive` занято (класс `account_write`, архивная папка Telegram). Команд ежемесячного экспорта и проверки целостности нет.
- Takeout (для отказа по Q1): gotd v0.166.0 содержит пакет `telegram/takeout` (`Init` с ожиданием `TAKEOUT_INIT_DELAY`, `NewClient`, `Finish`) и типы `InvokeWithTakeoutRequest`, `InvokeWithMessagesRangeRequest`, `MessagesGetSplitRangesRequest`. Документация Telegram, проверено 2026-11-15: [Takeout API](https://core.telegram.org/api/takeout), [account.initTakeoutSession](https://core.telegram.org/method/account.initTakeoutSession) (ошибка 420 `TAKEOUT_INIT_DELAY_%d`: «We have notified all your devices about the export request to make sure it's authorized and to give you time to react if it's not»). Числовых лимитов Takeout и обещания их повышения в документации нет.
- Not verified: `just check` в этом worktree не запускался (шаг 1). Откуда Daemon берёт `account_id` без запроса к Telegram, уточняет шаг 1 (кандидат: кэш пользователя в `internal/daemon/account_guard.go:21-35`).

## Goal

Агент раз в месяц получает в разрешённом владельцем каталоге архив прошедшего месяца: JSONL по чату, `SHA256SUMS` и `manifest.json`. Повторный вызов не портит готовый архив, а целостность проверяется одной командой (13.4). Пункт 14.2 (Takeout) не делается: решение записано, тег для `FEATURES.md` предложен.

## Problem and contract

Тип плана: product (две новые команды, без новых ключей `access.json` и без миграции).

### Что уже есть (по коду и `tg schema` на 2026-11-15)

| Пункт | Состояние | Где |
| --- | --- | --- |
| Выгрузка сохранённых сообщений чата | уже есть: `message export <чат> --local`, JSONL в stdout, `--since`, `--until`, `--include-deleted`, `--after`, страницы 500 и 768 КиБ | `internal/cli/export_cmd.go`, `internal/daemon/export_local.go`, `internal/store/export.go` |
| Полная история чата в базу | уже есть: `chat sync start|status|cancel`, прогресс в `sync_state` | `internal/daemon/history_sync.go`, `internal/store/sync_state.go` |
| Запись файлов в каталоги владельца | уже есть: ключ `downloads`, `OpenDownloadDir`, запись через `os.Root`, 0600, без перезаписи | `internal/access/access.go:96-104`, `internal/fileroot/create.go` |
| SHA-256 | уже есть в других местах (пресет, отправка, идемпотентность), выгрузку не хеширует | `internal/clientpreset/clientpreset.go:160`, `internal/daemon/send_media.go:135` |
| Экспорт в файлы и манифест (13.4) | нет: выгрузка только в stdout | |
| Расписание | нет и не нужно (Q6 = A) | |
| Takeout (14.2) | нет и не делается (Q1 = C) | |

### Контракт 13.4

- Команды (Q5 = A): `tg message export-month <чат>... --month YYYY-MM --output <каталог> [--include-deleted]` и `tg message export-verify <каталог архива>`. Методы JSON-RPC `message.export_month` и `message.export_verify` (префикс `message.` даёт проверку `--expect-user` без правки `account_guard.go`). Класс `local_write` у первой, `read` у второй. Telegram не вызывается, вход не нужен, Daemon должен работать (без него код 3).
- Расписания в утилите нет (Q6 = A): агент или `cron`/`launchd` владельца вызывает команду раз в месяц. Справочник и навык дают пример строки `cron`.
- Месяц (Q7 = A): календарный месяц UTC, полуоткрытый интервал `[YYYY-MM-01T00:00:00Z, начало следующего месяца)`. Разрешён только закончившийся месяц: текущий и будущий дают код 2. Внутри Daemon `ExportMessages` вызывается с `Since` = начало месяца и `Until` = начало следующего месяца минус 1 секунда (границы `ExportQuery` включены, даты в базе в секундах).
- Где пишется: только внутри корней `downloads` из `access.json`. Без `downloads` код 9 до чтения базы; `--output` проверяет `fileroot.OpenDownloadDir` (абсолютный, существующий каталог внутри корня после разрешения ссылок, иначе код 2). Внутри `--output` Daemon создаёт один каталог архива `tg-export-<account_id>-<YYYY-MM>` с правами 0700 через `os.Root.Mkdir`; другие каталоги не создаются.
- Состав каталога архива (Q8 = A): `<chat_id>.jsonl` на каждый чат (строки байт в байт как у `message export --local` с теми же границами и флагами), затем `SHA256SUMS` (формат `shasum -a 256 -c`: `<hex>  <имя>`, строки по имени файла), последним `manifest.json`. Наличие `manifest.json` означает «архив дописан». Каждый файл пишется через временное имя и `Link` без перезаписи, права 0600.
- `manifest.json` (версия 1, поля в фиксированном порядке, `json.MarshalIndent` с двумя пробелами):

```json
{
  "format": "tg-cli.export-month",
  "version": 1,
  "account_id": 123456789,
  "month": "2026-10",
  "since": "2026-10-01T00:00:00Z",
  "until": "2026-11-01T00:00:00Z",
  "include_deleted": false,
  "created_at": "2026-11-01T03:00:12Z",
  "tool_version": "0.x.y",
  "files": [
    {
      "name": "-1001234567890.jsonl",
      "chat_id": -1001234567890,
      "messages": 3097,
      "bytes": 1843200,
      "sha256": "<64 hex>",
      "first_id": 1084000,
      "last_id": 1087030,
      "sync": {"complete": false, "oldest_id": 1000001, "newest_id": 1087030, "month_start_covered": true}
    }
  ]
}
```

  `sync` берётся из `sync_state` на момент записи: `month_start_covered` истинно, если `complete` или самое старое загруженное глубокой загрузкой сообщение старше начала месяца. Пробелы после `newest_id` (сообщения, которые Daemon не видел) манифест не доказывает; справочник это говорит прямо. Пустой месяц чата даёт пустой файл с `messages: 0` и SHA-256 пустой строки. Названия, username и тексты в манифест не попадают: только числа.
- Пределы (Q9 = A): от 1 до 50 чатов за вызов без повторов; сумма файлов не больше 1 GiB; строка больше 768 КиБ останавливает вызов кодом 1 с `chat_id` и `id` в тексте (то же правило, что у страницы `message export`). Превышение суммы останавливает запись кодом 1.
- Повторяемость (Q10 = A): для одной и той же базы `<chat_id>.jsonl` и `SHA256SUMS` совпадают байт в байт (порядок `id` по возрастанию, кодирование `rpc.ExportMessage`); `manifest.json` отличается только `created_at`. Если каталог архива уже есть и в нём `manifest.json`: Daemon проверяет его так же, как `export-verify`, ничего не пишет и возвращает прежний манифест с `existing: true` (код 0, если проверка прошла, иначе код 1 с расхождениями). Если каталог есть, а `manifest.json` нет: код 1 `incomplete archive`, Daemon ничего не трогает, подсказка удалить каталог вручную. Сбой внутри своего вызова Daemon убирает сам: удаляет созданный этим вызовом каталог (`os.Root.RemoveAll` только на нём).
- Ответ `export-month`: `{dir, month, existing, manifest_sha256, files: [{name, chat_id, messages, bytes, sha256, month_start_covered}]}`; `--format jsonl` печатает по файлу в строке (`ItemPath: files`). `manifest_sha256` даёт агенту запомнить отпечаток самого манифеста.
- Проверка `export-verify <каталог>`: путь внутри корней `downloads` (иначе код 2; без `downloads` код 9); Daemon читает `manifest.json`, проверяет `format` и `version`, пересчитывает SHA-256 и размер каждого файла через `os.Root` (символическая ссылка внутри каталога архива не открывается), сверяет `SHA256SUMS` с манифестом, ищет лишние файлы. Ответ `{dir, month, ok, files: [{name, ok, expected_sha256, actual_sha256, bytes}], missing, extra}`. Расхождение даёт `ok: false` и код 1, как частичный успех пачки.
- Закрытый списками доступа или неизвестный чат даёт `not_found` (код 7) до создания каталога архива: все чаты проверяются до записи. Чат, закрытый посреди записи, останавливает вызов `not_found` и убирает каталог.
- Методы `message.export_month` и `message.export_verify` получают тайм-аут 600 с (`SetMethodTimeout`), как `message.download`. Журнал Daemon пишет метод, длительность и код, без путей.
- Миграция не нужна: архив читает существующие таблицы (`messages`, `sync_state`) только на чтение, а его состояние живёт в самом каталоге (`manifest.json`). Номер 0007 остаётся свободным для другого потока.

### 14.2: отказ

Takeout технически возможен (gotd и документация Telegram), но не делается (Q1 = C): выигрыш в скорости не подтверждён документацией и не может быть измерен без живых проверок, а цена видна сразу: ожидание подтверждения до часов и уведомление на всех устройствах владельца. Полная история чата и сейчас грузится `chat sync`. План создаёт решение об отказе (шаг 6) и предлагает менеджеру тег для `FEATURES.md` (текст в Decision log); саму правку `FEATURES.md` по 14.2 вносит менеджер.

**Релиз и откат.** Один слой; откат ветки. Схема базы и `access.json` не меняются. Архивы остаются в каталогах владельца.

## Файлы потока

Новые файлы, которыми владеет поток (другие потоки их не трогают):

- `internal/fileroot/archive.go`, `internal/fileroot/archive_test.go`: `DownloadDir.CreateArchive(name)` (каталог 0700 через `os.Root.Mkdir`, вложенный `os.Root`), запись файла с подсчётом SHA-256 и байт (`io.MultiWriter` поверх существующего `Write`), удаление только своего каталога, чтение файла для проверки без перехода по ссылкам.
- `internal/rpc/export_month.go`, `internal/rpc/export_month_test.go`: `ExportMonthParams`, `ExportMonthResult`, `ExportMonthFile`, `ExportVerifyParams`, `ExportVerifyResult`, `Manifest`, проверка параметров (месяц, 1 до 50 чатов).
- `internal/daemon/export_month.go`, `internal/daemon/export_month_test.go`: обработчики `message.export_month` и `message.export_verify`, манифест, пределы.
- `internal/cli/export_month_cmd.go`, `internal/cli/export_month_cmd_test.go`: команды `message export-month` и `message export-verify`, аннотации схемы.
- `.agents/decisions/20261115_HHMM_monthly_export_writes_jsonl_and_sha256_manifest_into_downloads.md` и `.agents/decisions/20261115_HHMM_takeout_is_not_built.md` (имена уточняются в шаге 6).

Общие файлы, каждая правка одной локальной вставкой (новые строки рядом, без переформатирования соседних):

| Файл | Вставка |
| --- | --- |
| `internal/rpc/api.go` | константы `MethodMessageExportMonth`, `MethodMessageExportVerify` в конце `const` |
| `internal/daemon/daemon.go` | две строки `d.handle(...)` и две `SetMethodTimeout(..., 600*time.Second)` в конце `register()` |
| `internal/daemon/access_test.go` | две строки в карте `classes` |
| `internal/cli/read_cmd.go` | `a.messageExportMonthCommand(), a.messageExportVerifyCommand()` в `cmd.AddCommand` после `a.messageExportCommand()` |
| `docs/cli/messages-read.md`, `docs/cli/overview.md` | свой раздел в шаге 6; строка в таблице команд, если она не генерируется |
| `docs/contracts.md`, `FEATURES.md` (только 13.4), `README.md`, `AGENTS.md`, `docs/skill/tg-cli/SKILL.md`, `.agents/decisions/_TOC.md` | своя строка или свой абзац в шаге 6 |

Изменения общих реестров (полный список):

1. Методы JSON-RPC: `message.export_month`, `message.export_verify`, тайм-аут 600 с у обоих.
2. Интерфейс `daemon.Telegram`: не меняется.
3. Классы доступа `TestRegisteredMethodsHaveAccessClass`: `message.export_month`, `message.export_verify`.
4. Дроссель и `readFiles`: не меняются (Telegram не вызывается).
5. Команды CLI и схема: `message export-month` (`local_write`), `message export-verify` (`read`).
6. `access.json`: без новых ключей, используется существующий `downloads`.
7. Миграции: нет.

## Scope

- В плане: 13.4 (экспорт месяца и проверка), решение об отказе от 14.2, схема, тесты на временной базе и временных каталогах без Telegram, документы, справочник, статус 13.4 в `FEATURES.md`.
- Вне плана: Takeout (14.2, отказ), расписание внутри Daemon, скачивание медиа в архив месяца, шифрование и сжатие архива, удаление старых архивов, выгрузка чатов, которых нет в базе, живые проверки (отменены владельцем), правка `access.json` агентом, правка строки 14.2 в `FEATURES.md` (вносит менеджер).
- Затрагиваются: файлы из раздела «Файлы потока».

## Main invariant

1. Архив пишется только внутри корня `downloads`: ни `..`, ни символическая ссылка, ни подмена каталога не выводят запись наружу; существующий файл и чужой каталог не перезаписываются и не удаляются; права файлов 0600, каталога 0700.
2. Строки `<chat_id>.jsonl` байт в байт равны выводу `message export <чат> --local --since … --until … [--include-deleted]` для той же базы.
3. `export-month` и `export-verify` не обращаются к Telegram (счётчик вызовов подмены равен нулю).
4. Закрытый списками доступа чат отвечает как неизвестный и в архив не попадает.
5. Остальные команды, ответы и схема базы не меняются; `internal/cli` не импортирует `tgclient`, gotd, `store`.
6. Ни тест, ни проверка не обращаются к живому Telegram; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: архив большого чата не успевает за 600 с. Стратегия: потоковая запись страницами по 500 без загрузки месяца в память; тест на 100 000 синтетических сообщений меряет время; при нехватке отдельный вопрос, а не молчаливое увеличение.
- Риск: заполнение диска. Стратегия: предел 1 GiB на вызов, счётчик байт, удаление своего каталога при сбое.
- Риск: агент считает архив полной историей месяца, хотя база неполна. Стратегия: поле `sync.month_start_covered` и прямое предупреждение в справочнике и навыке; рецепт «`chat sync start`, дождаться `done`, затем `export-month`».
- Риск: сообщения первой ночи по местному времени попадают в прошлый месяц (UTC). Стратегия: справочник называет UTC явно, `since` и `until` записаны в манифест.
- Риск: недописанный каталог от упавшего процесса блокирует месяц. Стратегия: код 1 с подсказкой; Daemon убирает свой каталог сам при любом сбое внутри вызова, поэтому такой каталог остаётся только после падения процесса.
- Риск: пересечение с другим потоком волны 4 в `api.go`, `daemon.go`, `access_test.go`, `read_cmd.go`. Стратегия: вставки отдельными строками; при конфликте сохраняются обе стороны.
- Риск: `account_id` неизвестен, если аккаунт не вошёл. Стратегия: шаг 1 находит источник без запроса к Telegram; без него команда даёт код 4 до записи.

## Decision log

| Date | Decision | Reason | Confirmed |
| --- | --- | --- | --- |
| 2026-11-15 | Q1. Объём 14.2 (Takeout): вариант C, не делать. Предложенный тег для `FEATURES.md` 14.2: `[Wont: Takeout не даёт доказанного выигрыша, решение 20261115_HHMM_takeout_is_not_built]` (номер времени ставит шаг 6) | Менеджер отклонил рекомендацию автора ради отказа от непроверяемой функции; подтверждено владельцем в диалоге с планировщиком 2026-11-15. Цена: официального канала экспорта нет, полную историю по-прежнему грузит `chat sync` | yes |
| 2026-11-15 | Q2, Q3, Q4 (разрешение, темп, хранение сессии Takeout) сняты | Следствие Q1 = C | yes |
| 2026-11-15 | Q5. Имена команд 13.4: вариант A, `message export-month` и `message export-verify` | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана; подтверждено владельцем в диалоге с планировщиком 2026-11-15 | yes |
| 2026-11-15 | Q6. Запуск раз в месяц: вариант A, агент или `cron` владельца, планировщика в Daemon нет | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана; подтверждено владельцем в диалоге с планировщиком 2026-11-15 | yes |
| 2026-11-15 | Q7. Границы месяца: вариант A, месяц UTC, только закончившийся | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана; подтверждено владельцем в диалоге с планировщиком 2026-11-15 | yes |
| 2026-11-15 | Q8. Формат манифеста: вариант A, `manifest.json` и `SHA256SUMS` | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана; подтверждено владельцем в диалоге с планировщиком 2026-11-15 | yes |
| 2026-11-15 | Q9. Пределы: вариант A, до 50 чатов, до 1 GiB, строка до 768 КиБ | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана; подтверждено владельцем в диалоге с планировщиком 2026-11-15 | yes |
| 2026-11-15 | Q10. Повторный вызов: вариант A, готовый архив проверяется и возвращается с `existing: true`, недописанный даёт код 1 и не трогается | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана; подтверждено владельцем в диалоге с планировщиком 2026-11-15 | yes |
| 2026-11-15 | Миграция не нужна, номер 0007 не занимается | Архив только читает `messages` и `sync_state`, его состояние живёт в `manifest.json`; таблица сессий Takeout ушла вместе с Q1 | no |
| 2026-11-15 | Проверка архива при расхождении даёт `ok: false` и код 1, новый код выхода не вводится | Так уже устроен частичный успех пачки; таблица кодов не меняется | no |
| 2026-11-15 | Имя `message archive` не используется | `chat archive` уже означает архивную папку Telegram | no |
| 2026-11-15 | Манифест хранит только числа (id аккаунта, чатов, сообщений, размеры, хеши) | Чужой текст и имена не выходят из JSONL; файл можно показывать без пометок `untrusted` | no |

## Step 0. Fix decisions before changes

- [x] Ответы на Q1 до Q10 записаны в Decision log, контракт поправлен под ответы (2026-11-15).
- [x] По Q1 = C из плана убраны шаги Takeout, ключ `takeout`, состояние `waiting_takeout`, таблица сессий и миграция 0007.
- [ ] Менеджер внёс тег `[Wont]` для 14.2 в `FEATURES.md` по тексту из Decision log.
- [ ] Исполнитель Go-кода (Codex Terra) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [x] Ветка `feature/20261115_1747_history_export` создана от `main` (`82d4c6a`) в worktree `../tg-cli-history_export`.

## Step 1. Audit and baseline (исполнитель Codex Terra)

Отчёт в `var/agents/plans/20261115_1747_history_export/audit.md` (в git не входит), итог в «Known facts».

- [ ] `just check` и `just build` на чистом дереве ветки; результат в Known facts.
- [ ] Подтвердить таблицу «Что уже есть»; убедиться, что команд `export-month` и `export-verify` нет в `tg schema`.
- [ ] `ExportMessages` с `Since` и `Until`: границы, индексы для выборки по `chat_id` и `date`, время на 100 000 сообщений; можно ли переиспользовать кодирование строк `exportLocalPage` без копии.
- [ ] `os.Root` в Go 1.27: `Mkdir`, `OpenRoot` вложенного каталога, `RemoveAll`, `Lstat`; поведение на macOS и Linux.
- [ ] Источник `account_id` в Daemon без запроса к Telegram и поведение, если аккаунт не вошёл.
- [ ] Как `tool_version` попадает в Daemon (версия сборки).
- [ ] Пересечения общих файлов с другим потоком волны 4 (`git worktree list`, ветки `feature/20261115_*`).
- [ ] Записать итоги в Known facts, непокрытое в Risks и Open questions.

## Implementation steps

Каждый шаг кода выполняет Codex Terra, проверяет Claude (тесты, дифф, `just check`), коммитит Claude.

- [ ] 2. `fileroot/archive.go`: каталог архива 0700 без перезаписи, запись файла с SHA-256 и счётчиком байт поверх `Write`, удаление только своего каталога, чтение для проверки без перехода по ссылкам. Тесты: каталог уже есть; ссылка вместо каталога архива; ссылка внутри архива при проверке; превышение предела посреди файла (временный файл и каталог удалены); права 0700 и 0600; хеш совпадает с `crypto/sha256` по тем же байтам; отмена контекста.
- [ ] 3. `rpc/export_month.go` и константы в `api.go`. Тесты: месяц `2026-1`, `2026-13`, текущий и будущий месяц (часы подменены) код 2; 0 и 51 чат, повтор чата код 2; разбор и кодирование `Manifest` с фиксированным порядком полей.
- [ ] 4. Daemon: `export_month.go`, регистрация и тайм-ауты в `daemon.go`, строки в `access_test.go`. Тесты без сокетов на временной базе и временном корне: без `downloads` код 9; `--output` вне корня код 2; закрытый чат `not_found` и каталог не создан; два чата, строки файла равны выводу `exportLocalPage` для тех же границ; пустой месяц; `--include-deleted`; повторный вызов `existing: true` и файлы не тронуты; каталог без манифеста код 1 и не тронут; порча байта в файле, лишний файл, пропавший файл дают `ok: false`; превышение 1 GiB (предел подменён в тесте) удаляет каталог; строка больше 768 КиБ код 1; `month_start_covered` для полного, частичного и пустого `sync_state`; счётчик вызовов подменного Telegram равен нулю; 100 000 сообщений укладываются во время (замер в отчёте).
- [ ] 5. CLI: `export_month_cmd.go`, две команды в `read_cmd.go`, аннотации (`local_write` и `read`, `ItemPath: files`); заготовки разделов в `docs/cli/messages-read.md`, чтобы тест полноты был зелёным. Тесты по образцу `download_cmd_test.go`: флаги, форматы, `--format jsonl`, код 1 при `ok: false`.
- [ ] 6. Документы через `docwriter` и `glavred` по `docs_chain.md`: `docs/cli/messages-read.md` (`export-month`, `export-verify`, формат манифеста, пример `cron`, UTC, что манифест не доказывает), `docs/contracts.md` (абзац «Monthly export»); таблицы пересобрать `go test ./internal/cli -run TestDocsGenerated -update-docs`; `FEATURES.md` только 13.4: подпункты 13.4.x, строка статуса `[0]`, в список «не проверено на живом аккаунте» внести время архива на настоящем большом чате и проверку `shasum -a 256 -c` на архиве с живой базы; `README.md` (только проверенное тестами); `docs/skill/tg-cli/SKILL.md` (рецепт: `chat sync start`, дождаться `done`, `export-month` раз в месяц, `export-verify`); `AGENTS.md` (Security: архив пишется только в `downloads`); два решения в `.agents/decisions/` (ежемесячный экспорт; отказ от Takeout с причиной и ценой из Q1) и строки в `_TOC.md`.
- [ ] 7. Итоговые проверки: таблица «Verification», `git diff --stat main`, нет файлов вне раздела «Файлы потока».

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/fileroot/... ./internal/rpc/... ./internal/daemon/... ./internal/cli/...` | зелёный | not_run |
| Запись только в корень | тесты `internal/fileroot`, `internal/daemon` (`export_month`) | ссылки, `..`, чужой каталог отклонены, вне корня файлов нет | not_run |
| Строки архива равны выгрузке | тест `internal/daemon` | байт в байт | not_run |
| Повторяемость | тест `internal/daemon` | два архива одной базы: равны `*.jsonl` и `SHA256SUMS` | not_run |
| Проверка целостности | тест `internal/daemon` | порча, лишний, пропавший файл дают `ok: false` и код 1 | not_run |
| Без Telegram | тесты `internal/daemon` | счётчик вызовов подмены 0 у `export_month` и `export_verify` | not_run |
| Полнота схемы, справочника и классов | `go test ./internal/cli`, `TestRegisteredMethodsHaveAccessClass` | разделы для `message export-month`, `message export-verify`; классы у двух методов | not_run |
| Таблицы справочника | `go test ./internal/cli -run TestDocsGenerated -update-docs`, затем `go test ./internal/cli -run TestDocsGenerated` | таблицы пересобраны, повторный запуск зелёный | not_run |
| Миграций нет | `git diff --stat main -- internal/store/migrations` | пусто | not_run |

## Deploy / rollback

- [ ] Выкладки нет. После слияния перезапустить Daemon (`tg daemon stop`, `tg daemon start`).
- [ ] Откат: откат ветки. Схема базы и `access.json` не меняются.
- [ ] Архивы остаются в каталогах владельца, Daemon их не удаляет.

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261115_1747_history_export/`.
- [ ] Временные файлы не в git (`/var/` в `.gitignore`).

## Definition of done

- [ ] Код изменён только в рамках плана и раздела «Файлы потока».
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.
- [ ] Решения записаны в `.agents/decisions/` и `_TOC.md`; `FEATURES.md` (13.4, «не проверено на живом аккаунте»), справочник, навык, `AGENTS.md` обновлены; тег 14.2 внесён менеджером.

## Known facts

- Выгрузка `message export --local` пишет только в stdout; единственный `--output` в CLI у `message download` (`internal/cli/download_cmd.go:14`).
- `ExportQuery` принимает `Since` и `Until` с включёнными границами (`internal/store/export.go:15-55`), порядок `id ASC`.
- `accountMethod` проверяет `message.*` (`internal/daemon/account_guard.go:61`): новые методы правки не требуют.
- Миграции нумеруются подряд от 1, последняя `0006_chat_versions.sql`; план новую не добавляет.
- Коды выхода: 1 общий и частичный успех, 2 параметры, 3 нет Daemon, 4 нет входа или не тот аккаунт, 7 `not_found`, 9 нет разрешения владельца.
- Takeout: gotd v0.166.0 поддерживает его полностью; документация Telegram не называет числовых лимитов Takeout и не обещает их повышения; при первом запросе возможна ошибка `TAKEOUT_INIT_DELAY_X` и уведомление на всех устройствах.

## Open questions

Каждый вопрос задавался отдельно. Рекомендуемый вариант первый.

| # | Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| Q1 | Какой объём у 14.2 (Takeout) | Определяет миграцию, новые запросы к Telegram и половину плана | A: режим `chat sync start --takeout`; B: отдельная группа `tg takeout` с выгрузкой в файлы; C: не делать, 14.2 `[Wont]` | A. Цена: миграция, новый ключ `access.json`, уведомления на устройствах, выигрыш не доказан. C дешевле и честен, если ценность в одной скорости | решён: C |
| Q2 | Какое разрешение нужно для Takeout | Takeout будит уведомления на устройствах | A: ключ `takeout: true`; B: только видимость чата; C: ключ `sessions` | A | снят |
| Q3 | Темп загрузки под Takeout | Повышенные лимиты не подтверждены | A: как у обычной загрузки; B: пауза 1 с; C: переменная окружения | A | снят |
| Q4 | Где хранить id сессии Takeout | Перезапуск требует нового подтверждения | A: таблица, миграция 0007; B: только в памяти | A | снят |
| Q5 | Как назвать команды 13.4 | Имена попадают в схему, справочник и навык | A: `message export-month` и `message export-verify`; B: группа `tg backup month|verify`; C: флаги `--month --output` у `message export` | A. Цена: две новые подкоманды. B требует правки `account_guard.go`. C смешивает stdout и запись файлов | решён: A |
| Q6 | Кто запускает экспорт раз в месяц | Нужен ли планировщик в Daemon | A: агент или `cron`/`launchd` владельца; B: планировщик в Daemon | A. Цена: внешняя строка `cron`. B пишет на диск без запроса агента | решён: A |
| Q7 | Границы месяца | Повторяемость и содержимое | A: UTC, только закончившийся; B: часовой пояс машины; C: любой месяц | A. Цена: сообщения первой ночи по UTC+3 попадают в прошлый месяц | решён: A |
| Q8 | Формат манифеста | Проверка без утилиты | A: `manifest.json` и `SHA256SUMS`; B: только `manifest.json`; C: только `SHA256SUMS` | A. Цена: два файла с одинаковыми хешами | решён: A |
| Q9 | Пределы одного вызова | Диск и тайм-аут 600 с | A: до 50 чатов, 1 GiB, строка до 768 КиБ; B: 200 чатов, 4 GiB; C: без пределов | A. Цена: сотня чатов требует двух вызовов | решён: A |
| Q10 | Что делать, если архив месяца уже есть | Повторный `cron` не должен портить готовое | A: проверить и вернуть с `existing: true`, недописанный код 1; B: перезаписать; C: новый каталог с суффиксом | A. Цена: недописанный каталог владелец удаляет вручную | решён: A |
