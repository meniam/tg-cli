# Пошаговый план: частичный успех пакетных команд, подсказки команд и ошибки без утечек

ID: 20261115_0241_agent_contract
Created: 2026-11-15
Owner: Мязин Евгений
Branch: feature/20261115_0241_agent_contract
PR: —

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: draft
- Last update: 2026-11-15
- Next safe step: менеджер принимает или меняет ответы на Q1 до Q11 из «Open questions» (владелец делегировал решения менеджеру, рекомендуемый вариант идёт первым), записывает их в Decision log; затем статус `approved` и шаг 1 (аудит, Codex Terra в worktree `../tg-cli-agent_contract`). Q10 можно отложить до шага 8.

## Context and sources

- Read: `AGENTS.md`, `INTENT.md`, `FEATURES.md` (пункты 3.1 до 3.9, 9.1 до 9.6, сценарии 3.6, 3.8, 9.5 и строки статуса `[~] 3.8`, `[~] 9.5`, «Не сделано, Must: 3: 3.6»), `.agents/rules/plans.md`, план-образец `../tg-cli-users_contacts/.agents/plans/20261114_2244_users_contacts.md`, прежний план `20261114_0050_agent_contract.md` (в нём 3.6 отложен: «пока нет пакетных команд»).
- Код: `internal/cli/cli.go` (`cmdError`, `Execute`, `withIDHint`, `rootCommand`), `internal/cli/output_cmd.go` (`emit`, `success`, `failure`), `internal/cli/client.go` (`translate`, `exitForRPC`), `internal/cli/spec.go`, `internal/cli/schema_cmd.go`, `internal/cli/write_cmd.go` (`messageIDs`, `writeCall`), `internal/cli/daemon_cmd.go`, `internal/exitcode/exitcode.go`, `internal/output/output.go`, `internal/rpc/server.go` (`toError`), `internal/rpc/write.go` (`DeleteResult`, `ForwardResult`), `internal/daemon/write.go` (`messageDelete`, `messageForward`, `writeOutcome`), `internal/tgclient/write.go` (`deleteMessages`, `forwardMessages`), `internal/tgclient/errors.go` (`mapError`), `internal/tgclient/throttle.go` (`writeRequest`), `internal/daemon/login.go`, `internal/chatref/chatref.go`, `internal/schema/schema.go`.
- gotd v0.166.0: `telegram/message/delete.go` (`DeleteBuilder.Messages`, `RevokeBuilder.Messages`) шлёт все id одним запросом, без разбиения на части.
- Проверено командами: `go build ./cmd/tg-cli` и `tg schema` на `main` (ea157b2): 53 команды, `schema_version: 1`, документ 78 187 байт; `tg mesage`, `tg message send`, `tg chat`, `tg message history -100123` (поведение ниже в Known facts); `rg` по `Variadic: true`, `StringArray`, `partial`, `NewError(... + err.Error())` во всех открытых worktree.
- Другие потоки: `git worktree list` (волна 2: `status_audit`, `daemon_lifecycle`, `net_transport`, `read_unread_chats`, этот поток; не слиты из волны 1: `users_contacts`, `folders_account_state`, `sched`). Планы волны 2 на момент написания ещё не закоммичены, их охват известен только по именам.
- Not verified: тесты и `just check` не запускались (это шаг 1). Не проверены по документации Telegram предел числа id в `messages.deleteMessages`, `channels.deleteMessages` и `messages.forwardMessages` (по памяти 100), смысл `pts_count` в ответе удаления и поведение `forwardMessages`, когда часть id не существует (ошибка или неполный ответ).

## Goal

1. Пакетная команда, у которой часть элементов не выполнилась, сохраняет успешные, перечисляет ошибки в `data.errors` с полем `code`, ставит `data.partial: true` и завершается кодом 1 (3.6).
2. Утилита подсказывает себя сама: схема закрывает оставшиеся пробелы (3.8), группа без подкоманды и ошибка использования отвечают JSON-конвертом с подсказкой (по ответу Q6).
3. Ни одна ошибка в конверте, stderr и записи причины в `daemon.log` не содержит кода входа, пароля, номера телефона, ключей приложения, строки сессии и секрета прокси (9.5.1); неожиданная ошибка Daemon остаётся `internal error` (9.5.2, уже есть).

## Problem and contract

Тип плана: product (меняется контракт ответа, кода выхода и текстов ошибок).

### Что уже есть (не планируется заново)

| Пункт | Что сделано | Где |
| --- | --- | --- |
| 3.8.1 | `tg schema` без Daemon и сети, одинаковый документ: версия схемы, имя, версия и ревизия, глобальные флаги, команды с аргументами, флагами, умолчаниями и допустимыми значениями (у флагов команд), коды выхода, форматы | `internal/cli/schema_cmd.go` (`schemaDocFor`), тест `TestSchemaNeedsNoDaemonAndIsDeterministic` (`internal/cli/schema_cmd_test.go:94`) |
| 3.8.2 | класс безопасности и `dangerous`, заданные явно; команда без аннотации роняет тест | `internal/cli/spec.go` (`annotate`, `risk`), `TestEveryCommandIsAnnotated` (`schema_cmd_test.go:29`) |
| 3.8.3 | поля ответа из Go-типа результата с признаком необязательности; вложенные структуры раскрываются | `internal/schema/schema.go`, `TestSchemaFieldsMatchRealResponses` (`schema_fields_test.go:123`) |
| 3.8.4 | `tg version` с версией и ревизией | `internal/buildinfo`, команда `version` |
| 9.5.2 | любая ошибка обработчика не типа `*rpc.Error` уходит клиенту как `internal error`, причина только в журнале; тайм-аут и отмена своими текстами | `internal/rpc/server.go:353` (`toError`) |
| 9.5.1 частично | код, пароль и номер не попадают в журнал и вывод входа | `TestSecretsAreNeverLogged` (`internal/daemon/daemon_test.go:666`), `TestStringHidesSecrets` (`internal/chatref/chatref_test.go:77`) |
| 3.6 | не сделано | пакетных ответов с `partial` и `errors` нет нигде (`rg partial` по `internal`) |

### Оставшиеся пробелы (по коду и `tg schema`)

- 3.6: `message forward` при неполном ответе Telegram возвращает ошибку `telegram_error` (код 5) с текстом «Telegram did not return all forwarded message ids» (`internal/tgclient/write.go:314`), хотя часть сообщений уже переслана. Агент, повторив команду, создаст дубли. `message delete` шлёт все id одним запросом и отвечает `deleted` равным запрошенным id, даже если Telegram молча пропустил несуществующие. Предела числа id в CLI и Daemon нет; больше предела Telegram даёт ошибку всего запроса.
- 3.8: у глобального `--format` нет `values` в схеме; у `daemon start --client` нет `values`, хотя имена пресетов фиксированы (`internal/clientpreset/clientpreset.go:50`); ответ `daemon start --foreground` (`stopped`) в схеме не описан (строка статуса `[~] 3.8`); `tg chat`, `tg message` и другие группы без подкоманды печатают текст справки cobra в stdout с кодом 0, то есть не JSON-конверт (нарушение 3.1.1 для агента); ошибка числа аргументов говорит «accepts 1 arg(s), received 0» без синтаксиса команды; схема печатается только целиком (78 КБ).
- 9.5.1: `mapError` для ошибки не из Telegram (сеть, транспорт, прокси) склеивает её текст в ответ: `"telegram request failed: "+err.Error()` (`internal/tgclient/errors.go:40`). Поток `net_transport` добавит прокси (1.9): адрес с логином и паролем SOCKS или секрет MTProxy может попасть в такой текст. На стороне CLI около 30 вызовов `fail(..., "%v", err)` (`daemon_cmd.go`, `client.go`, `daemon_logs.go`) передают текст причины без фильтра. Запись причины в `daemon.log` (`server.go:364`) тоже без фильтра. Тексты ошибок клиентской библиотеки на утечки не проверялись (строка статуса `[~] 9.5`).

### Контракт после плана (для рекомендованных ответов Q1 до Q11; другой ответ меняет отмеченные места)

Частичный успех (3.6):

- Общий тип в `internal/rpc/batch.go`: `Batch{Partial bool "partial,omitempty"; Errors []ItemError "errors,omitempty"}` встраивается в результат пакетной команды; `ItemError{ID int "id,omitempty"; Code string "code"; Message string "message"; RetryAfterSeconds int "retry_after_seconds,omitempty"}`. `code` это имя кода выхода из `internal/exitcode` (`not_found`, `telegram_error`, `flood_wait`, `not_authorized`, `failure`), то есть тот код, который дал бы одиночный вызов. `message` проходит те же правила, что текст ошибки (9.5).
- Пакетные команды на `main`: `message delete <chat> <id>...` и `message forward <from> <to> <id>...`. В ветке `sched` (не слита): `message scheduled delete|send-now <chat> <id>...`, переходит на `Batch` в шаге 5 или при слиянии `sched` (см. «Как не сломать остальные потоки»).
- Конверт (Q1): `{"ok":true,"data":{...,"partial":true,"errors":[{"id":4103,"code":"not_found","message":"..."}]}}`, код выхода 1. При полном успехе `partial` и `errors` нет, ответ байт в байт прежний, код 0.
- Все элементы не выполнились (Q2): обычный конверт ошибки с кодом первой ошибки и её кодом выхода, как сегодня у одиночной ошибки.
- Проверки до Telegram (неверный id, закрытый чат, чат вне `write`, `--yes`, `--dry-run`) остаются для всего вызова целиком: частичного успеха до первого запроса записи не бывает.
- Разбиение (Q3): Daemon режет id на части по пределу Telegram (100, уточняет шаг 1) и шлёт их по очереди, не больше 1000 id за вызов (больше даёт код 2 до Daemon). После `FLOOD_WAIT` или `not_authorized` оставшиеся части не отправляются и попадают в `errors` с тем же кодом и `retry_after_seconds`; другая ошибка части не останавливает следующие. Запись не повторяется (правило дросселя).
- `message forward`: `ForwardedMessage` получает поле `source_id` (id исходного сообщения); id, которого нет в ответе Telegram, идёт в `errors` с `not_found`. С `--idempotency-key` частичный результат сохраняется под ключом, повтор возвращает его с `replayed: true` и кодом 1 (Q4).
- `message delete`: журнал `deletions.jsonl` пишет пару `intent` и `result` на каждую часть; сбой записи `intent` останавливает эту и следующие части, они идут в `errors` с `failure`. `deleted` перечисляет id отправленных без ошибки частей; база помечает удалёнными только их.
- `--fields` (Q5): `partial` и `errors` остаются в `data` при любом `--fields`, чтобы код 1 всегда сопровождался причиной.
- `--human`: данные, затем строка `partial: N of M failed` и строки ошибок; `toon` и `json` без особого вида.
- RPC: тот же `result` с `partial` и `errors`; JSON-RPC ошибки нет. Клиент сокета без CLI видит частичный успех по `result.partial`.
- Описание кода выхода 1 в `internal/exitcode`: «a generic error, or a partial success of a batch command (`data.partial`)». Смысл кода не меняется, таблица в `docs/cli/overview.md` пересобирается.

Подсказки команд (3.8):

- Схема: `values` у глобального `--format` (`json`, `jsonl`, `toon`, `human`) и у `daemon start --client` (имена пресетов); `daemon start` получает второй вариант ответа для `--foreground` через общий механизм вариантов ответа в `spec.go` (`ResultVariants: [{flag, result}]`, в схеме `result_variants`), Q11.
- Группа без подкоманды (Q6): `tg chat` печатает конверт ошибки `usage` с текстом `choose a subcommand: tg chat list|info|...; run tg schema for details`, код 2. `--help` остаётся текстом для человека.
- Ошибка использования (Q6): к тексту cobra добавляется `usage: tg message send <chat> [flags]` (из `UseLine`). Подсказка про `--` перед отрицательным id (`withIDHint`) остаётся.
- `tg schema [command...]` (Q7): необязательный путь команды или группы печатает тот же документ, где `commands` содержит только совпавшие команды (`tg schema chat` даёт все `chat ...`); неизвестный путь даёт код 2. Без аргумента документ прежний.
- `schema_version` остаётся 1: добавляются поля, смысл существующих не меняется (решение в Decision log).

Ошибки без утечек (9.5):

- Белый список на границе Telegram (Q8): ошибка Telegram передаёт только тип TL (`PEER_ID_INVALID`), как сейчас; любая другая ошибка клиента становится фиксированным текстом по категории (`telegram request failed: network error`, `... timeout`, `... unexpected response`), исходный текст уходит только в `daemon.log` после фильтра.
- Новый пакет `internal/redact`: `redact.Text(s)` заменяет на `[redacted]` номер в международном формате (`+` и 7 до 15 цифр с разделителями), логин и пароль в URL (`user:pass@`), шестнадцатеричные строки от 32 знаков (`api_hash`, секрет MTProxy), base64 и base64url от 64 знаков (строка сессии), значения после `api_hash=`, `password=`, `secret=`, `phone=`. Код входа (5 цифр) шаблоном не ловится: его защищает правило «вход никогда не повторяет введённое в ошибке», закреплённое тестом.
- Фильтр стоит в трёх точках, а не у каждого вызова: `rpc.Server.toError` (текст любой `*rpc.Error` и запись причины в журнал), `cli.app.failure` (любой конверт ошибки CLI, включая ошибки `daemon start`) и `cli.app.warn` (подсказки в stderr). Так правки потоков `daemon_lifecycle` и `net_transport` защищены без изменений их файлов.
- Тест-ловушка: подставные ошибки с номером, кодом, паролем, `api_hash`, строкой сессии и `socks5://user:secret@host` проходят через `mapError`, `toError`, `translate` и `failure`; ни одна строка вывода и журнала их не содержит.
- Статический тест (Q9) в `internal/redact`: разбор `go/ast` по `internal/` запрещает `rpc.NewError` и `rpc.Error{Message: ...}` с `err.Error()` или `%v`/`%w` от значения типа `error` в тексте. Исключения перечислены в тесте с причиной.

## Файлы потока

Новые файлы, которыми владеет поток (другие потоки их не трогают):

- `internal/rpc/batch.go`, `internal/rpc/batch_test.go`: `Batch`, `ItemError`, `(Batch).IsPartial()`, `ItemErrorFrom(id int, err error)` (код из `*rpc.Error`, иначе `failure` с текстом `internal error`), `Chunks(ids []int, size int)`.
- `internal/redact/redact.go`, `internal/redact/redact_test.go`, `internal/redact/guard_test.go`: фильтр текста и статический тест.
- `internal/cli/partial.go`, `internal/cli/partial_test.go`: распознавание частичного результата, код 1 без второго конверта, вывод `--human`, сохранение `partial` и `errors` при `--fields`.
- `internal/cli/hints.go`, `internal/cli/hints_test.go`: ответ группы без подкоманды, синтаксис команды в ошибке использования, отбор схемы по пути.
- `internal/tgclient/batch_write.go`, `internal/tgclient/batch_write_test.go`: разбиение удаления и пересылки на части, сопоставление `random_id` и исходных id, сбор `ItemError`.
- `internal/daemon/batch_write_test.go`: тесты частичного удаления и пересылки через подмену Telegram (заглушки в своём файле, `daemon_test.go` не трогается).
- `.agents/decisions/20261115_HHMM_batch_commands_report_partial_success_and_errors_are_whitelisted.md` (имя уточняется в шаге 7).

Общие файлы, каждая правка одной локальной вставкой (новые строки рядом, без переформатирования соседних):

| Файл | Вставка |
| --- | --- |
| `internal/rpc/write.go` | строка `Batch` (встраивание) в `DeleteResult` и `ForwardResult`; поле `SourceID int "source_id"` в `ForwardedMessage` |
| `internal/rpc/server.go` | в `toError`: `redact.Text` для текста `*rpc.Error` и для записи причины в журнал (две строки) |
| `internal/tgclient/errors.go` | замена последней строки `mapError` (строка 40) на вызов `safeClientError(err)` из `batch_write.go` или нового соседнего файла |
| `internal/tgclient/write.go` | `deleteMessages` и `forwardMessages` вызывают функции из `batch_write.go`; проверка `len(messages) != len(p.IDs)` уходит |
| `internal/daemon/write.go` | `messageDelete`: цикл частей с парой записей журнала на часть и пометкой удалёнными только успешных id; `messageForward`: частичный результат идёт в `session.complete` |
| `internal/cli/cli.go` | поле `printed bool` в `cmdError`; в `Execute` конверт ошибки не печатается при `printed`; одна строка `a.addGroupHints(root)` в конце `rootCommand`; замена `withIDHint` на `withUsageHint` (подсказка `--` сохраняется) |
| `internal/cli/output_cmd.go` | в `success`: одна строка `return a.partialExit(data)` после печати; в `failure` и `warn`: `redact.Text(message)` |
| `internal/cli/write_cmd.go` | в `messageIDs`: предел 1000 id, больше даёт код 2 |
| `internal/cli/spec.go` | поле `ResultVariants []resultVariant` в `commandSpec` и тип `resultVariant` |
| `internal/cli/schema_cmd.go` | `result_variants` в `commandDoc`; `values` у глобальных флагов; аргумент `[command...]` у `schema` |
| `internal/cli/daemon_cmd.go` | одна правка аннотации `daemon start`: `Choices` для `--client` и `ResultVariants` для `--foreground` (Q11) |
| `internal/exitcode/exitcode.go` | текст `notes[Failure]` |
| `internal/output/fields.go` | `partial` и `errors` проходят фильтр `--fields` всегда |
| `internal/output/human.go` | строка сводки `partial` в `--human` |
| `docs/cli/overview.md`, `docs/cli/messages-write.md`, `docs/cli/daemon-and-auth.md`, `docs/cli/notes-and-search.md`, `docs/contracts.md`, `docs/skill/tg-cli/SKILL.md`, `FEATURES.md` (только 3.6, 3.8, 9.5 и их сценарии и строки статуса), `AGENTS.md` (слово `redact` в Layout), `.agents/decisions/_TOC.md` | свой абзац или своя строка в шаге 7 |

Изменения общих реестров (полный список):

1. Методы JSON-RPC: новых нет; результаты `message.delete` и `message.forward` получают необязательные `partial`, `errors`, `messages[].source_id`.
2. Интерфейс `daemon.Telegram`: без изменений сигнатур.
3. Классы доступа (`TestRegisteredMethodsHaveAccessClass`): без изменений.
4. `writeRequest` дросселя: без изменений (те же запросы, только чаще).
5. `readFiles` (`readonly_test.go`): без изменений.
6. Команды CLI и схема: новых команд и флагов нет; у `schema` необязательный аргумент (Q7); новые ключи схемы `result_variants` и `values` у глобальных флагов.
7. Коды выхода: новых нет, у кода 1 новый текст описания.
8. Хранилище и миграции: не меняются.

## Scope

- В плане: 3.6 для `message delete` и `message forward` (и `message scheduled delete|send-now`, если `sched` слит раньше шага 5); пробелы 3.8 выше; 9.5.1 по всем путям ошибок; тесты; документы; живая проверка чтением и, по Q10, одной пересылкой в «Браин Тест».
- Вне плана: коды выхода, которые может вернуть каждая команда, в схеме (сейчас они на страницах `docs/cli/`); машинный вывод `--help`; фильтр секретов во всём `daemon.log` за пределами записи причины (12.2 уже запрещает параметры и тексты); пакетные команды, которых нет на `main` и в слитых ветках; поле `affected_count` у удаления, если шаг 1 не подтвердит смысл `pts_count`.
- Затрагиваются: файлы из раздела «Файлы потока».

## Main invariant

1. Ответ любой существующей команды при полном успехе байт в байт прежний: `partial` и `errors` в нём отсутствуют. Единственное изменение успешного ответа: поле `source_id` в `messages[]` пересылки, оно отражено в схеме.
2. Код выхода и конверт ошибки любой непакетной команды прежние, кроме двух намеренных изменений: группа без подкоманды даёт код 2 вместо 0 (Q6) и `message forward` с неполным ответом даёт код 1 с данными вместо кода 5.
3. В Telegram не уходит ни одного запроса записи сверх нынешних: разбиение делит те же id, записи не повторяются, `intent` журнала пишется до каждой части.
4. Имена кодов выхода, формы конвертов `{"ok":true,"data"}` и `{"ok":false,"error"}` и `schema_version` не меняются.
5. `internal/cli` не импортирует `tgclient`, gotd, `store`; `internal/redact` не импортирует ничего из проекта.
6. Ни тест, ни проверка по умолчанию не обращаются к живому Telegram; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: агент смотрит только на `ok` и не видит частичный отказ при `ok: true`. Стратегия: код 1, `data.partial`, сводка в `--human`, правило в навыке «код 1 и `partial` значит прочитать `errors` и повторить только их»; альтернативы в Q1.
- Риск: разбиение удаления делает опасную команду длиннее и рискует `FLOOD_WAIT` посреди пакета. Стратегия: предел 1000 id, остановка после `FLOOD_WAIT`, журнал на каждую часть, `--dry-run` показывает число частей.
- Риск: Telegram молча пропускает несуществующие id при удалении, а ответ не говорит какие. Стратегия: в `deleted` только id успешно отправленных частей (как сейчас по смыслу); шаг 1 выясняет смысл `pts_count`; ложный `partial` по счётчику не вводится.
- Риск: фильтр `redact` портит законный текст (id чата `-1001234567890` похож на номер). Стратегия: шаблон номера требует `+`; таблица тестов с id чатов, id сообщений, курсорами и датами, которые фильтр не трогает.
- Риск: статический тест (Q9) и тест «у каждой пакетной команды результат с `Batch`» падают на коде других потоков при слиянии. Стратегия: оба теста дают понятный текст с путём и строкой; список исключений в тесте; потоки предупреждены в разделе ниже.
- Риск: конфликты слияния в `cli.go`, `output_cmd.go`, `daemon_cmd.go`, `FEATURES.md`. Стратегия: вставки отдельными строками по таблице выше; `FEATURES.md` правится только в строках 3.6, 3.8, 9.5 и их сценариях; строки статуса после слияния `status_audit`.

## Как не сломать остальные потоки

| Поток | Пересечение | Правило |
| --- | --- | --- |
| `status_audit` (волна 2) | переписывает блок «Статус реализации» в `FEATURES.md` | этот поток правит только требования 3.6, 3.8, 9.5 и их сценарии; три строки статуса вносит после слияния `status_audit` в `main`, при конфликте сохраняются обе стороны |
| `daemon_lifecycle` (волна 2) | `internal/cli/daemon_cmd.go`, ответы `daemon start` | правка одной аннотации (Q11); тексты ошибок `daemon_cmd.go` не трогаются, их закрывает фильтр в `app.failure`; если `daemon_lifecycle` меняет ответ `--foreground`, тип варианта берётся из его ветки при слиянии |
| `net_transport` (волна 2) | прокси SOCKS и MTProxy, ошибки соединения | `redact` ловит `user:pass@` и шестнадцатеричные секреты без участия потока; `mapError` больше не склеивает текст ошибки клиента; статический тест (Q9) поймает новую склейку `err.Error()` в `rpc.NewError` |
| `read_unread_chats` (волна 2) | возможная отметка прочитанного для нескольких чатов | если поток добавит команду со списком элементов, её результат встраивает `rpc.Batch`; до слияния этого потока такая команда отвечает как сейчас, тест полноты добавляется в шаге 5 и проверяет только команды на ветке |
| `sched` (волна 1, не слит) | `message scheduled delete|send-now <chat> <id>...` | кто сливается вторым, тот встраивает `rpc.Batch` в `ScheduledResult` и проходит тест полноты пакетных команд |
| `users_contacts` (волна 1, не слит) | номера телефонов в ответах и ошибках (Q8 того плана) | номер в данных ответа разрешён, в тексте ошибки нет; фильтр `redact` и статический тест действуют и на его код |
| `folders_account_state`, `chat_admin` (слит) | нет пакетных команд | ничего; `chat kick` с частичным сбоем уже даёт ошибку `telegram_error` с `still_banned` (`internal/tgclient/chat_admin.go:271`), это одиночная команда, не пакет |

Общие правила для потоков после слияния этого плана (войдут в `docs/contracts.md` и навык):

1. Команда, которая принимает список элементов и пишет в Telegram, встраивает `rpc.Batch` и проходит тест полноты.
2. Текст `rpc.NewError` не содержит `err.Error()` и введённых пользователем секретов.
3. Новые группы команд получают ответ «выберите подкоманду» автоматически, правок не нужно.

## Decision log

| Date | Decision | Reason | Confirmed |
| --- | --- | --- | --- |
| 2026-11-15 | Миграция не нужна | Схема базы не меняется (было: предложено планировщиком) | no |
| 2026-11-15 | `schema_version` остаётся 1 | Добавляются поля, смысл существующих не меняется; версия растёт только при смене смысла (`schema_cmd.go`) (было: предложено планировщиком) | no |
| 2026-11-15 | `errors[].code` это имя кода выхода, а не код JSON-RPC | Агент уже сопоставляет имена из `tg schema`; одна таблица значений (было: предложено планировщиком) | no |
| 2026-11-15 | Фильтр секретов в трёх общих точках (`toError`, `failure`, `warn`), а не у каждого вызова | Меньше правок в чужих файлах, защита кода других потоков без их участия (было: предложено планировщиком) | no |
| 2026-11-15 | Журнал удалений пишет пару `intent` и `result` на каждую часть | Правило 9.3.2 «сбой `intent` отменяет удаление» остаётся простым на уровне части (было: предложено планировщиком) | no |
| 2026-11-15 | Поле `source_id` у пересланного сообщения | Без него агент не знает, какой исходный id переслан, а какой ушёл в `errors` (было: предложено планировщиком) | no |
| 2026-11-15 | Q1. Какой конверт у частичного успеха: вариант A | A. Совпадает с 3.6 («в ответе `data.partial`»), форма конвертов не меняется. Цена: агент, который смотрит только на `ok`, примет частичный успех за полный; защита: код 1 и правило в навыке (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Q2. Что отвечать, если не выполнился ни один элемент: вариант A | A. Агент получает тот же код, что у одиночного вызова (например 6 и паузу при `FLOOD_WAIT`). Цена: ошибки остальных элементов в ответ не попадают (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Q3. Как обрабатывать больше id, чем Telegram принимает за запрос (по памяти 100, уточняет шаг 1): вариант A | A. Цена: до 10 запросов записи за вызов и риск `FLOOD_WAIT` посреди пакета, отражённый в `errors`; число 1000 выбрано планировщиком как разумный потолок, не требование Telegram (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Q4. Что делать с ключом идемпотентности при частичной пересылке: вариант A | A. Дублей нет при любом поведении Telegram. Цена: повтор того же вызова не дожмёт неудавшиеся id (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Q5. Действует ли `--fields` на `partial` и `errors`: вариант A | A. Цена: `--fields` у пакетной команды перестаёт быть точным списком полей, это исключение описывается в справочнике (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Q6. Подсказывать ли синтаксис в ошибках и что отвечает группа без подкоманды: вариант A | A. Цена: `tg chat` меняет код 0 на 2 (намеренное изменение инварианта 2); человек, набравший группу, видит JSON вместо справки и пишет `--help` (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Q7. Добавить ли отбор схемы по команде `tg schema [command...]`: вариант A | A. Цена: новый аргумент команды `schema`, раздел в справочнике и навыке (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Q8. Какой текст отдавать для ошибки клиента Telegram, которая не является ошибкой TL: вариант A | A. Цена: в ответе меньше подробностей, для разбора нужен `tg daemon logs` (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Q9. Вводить ли статический тест, запрещающий `err.Error()` и `%v` от ошибки в тексте `rpc.NewError`: вариант A | A. Цена: потоки, которые склеивают текст ошибки, правят код при слиянии (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Q10. Разрешена ли запись в живой проверке шага 8: вариант A | A. Задать перед шагом 8. Цена B: частичный успех и `source_id` не проверены на живом аккаунте (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Q11. Кто закрывает пробел схемы «ответ `daemon start --foreground` не описан»: вариант A | A. Механизм схемы принадлежит этому потоку. Цена: одна строка конфликта в `daemon_cmd.go` при слиянии (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |

## Step 0. Fix decisions before changes

- [ ] Ответы на Q1 до Q9 и Q11 записаны в Decision log, контракт поправлен под ответы.
- [ ] Исполнитель Go-кода (Codex Terra) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [x] Ветка `feature/20261115_0241_agent_contract` создана от `main` (ea157b2) в worktree `../tg-cli-agent_contract`.

## Step 1. Audit and baseline (исполнитель Codex Terra)

Отчёт в `var/agents/plans/20261115_0241_agent_contract/audit.md` (в git не входит), итог в «Known facts».

- [ ] `just check` и `just build` на чистом дереве ветки; результат в Known facts.
- [ ] Пределы Telegram: число id в `messages.deleteMessages`, `channels.deleteMessages`, `messages.forwardMessages` (схема TL, исходники tdlib или Telegram Desktop через `gh api`, не по памяти); что отвечает `forwardMessages`, если часть id не существует или защищена от пересылки (ошибка всего запроса или неполный `Updates`); смысл `pts_count` в `messages.affectedMessages` для `revoke` и без него.
- [ ] Как `idempotentAPI` задаёт `random_id` пересылки и в каком порядке; можно ли по `UpdateMessageID.RandomID` восстановить исходный id; что возвращает `writtenMessages` (`internal/tgclient/write.go:332`).
- [ ] Пакетные команды на `main` и во всех открытых worktree (`rg 'Variadic: true'`, `StringArray`, списки в параметрах RPC); итоговая таблица «команда, ветка, метод Telegram, предел».
- [ ] Перечень путей ошибок для 9.5: каждый `rpc.NewError`, `rpc.Error{...}`, `fail(...)` и запись журнала, где текст собирается из `err.Error()`, `%v`/`%w` от ошибки или ввода пользователя; таблица «файл:строка, что может утечь, закрывает ли фильтр в общей точке». Отдельно: ошибки `auth login` (номер, код, пароль), `daemon start` (`TG_API_HASH`, пресет), `rpc.Client` (`translate`), `fileroot` и загрузки медиа.
- [ ] Поведение cobra: группа без подкоманды, `--help`, ошибки числа аргументов и неизвестного флага; какие из них сейчас дают код 0.
- [ ] Тесты, которые изменятся: ожидания кода 5 и текста «did not return all forwarded» у пересылки, ожидания `deleted`, текста `telegram request failed: ...`, кода 0 у группы без подкоманды; список с путями.
- [ ] Записать итоги в Known facts, непокрытое в Risks и Open questions.

## Implementation steps

Каждый шаг кода выполняет Codex Terra, проверяет Claude (тесты, дифф, `just check`), коммитит Claude.

- [ ] 2. Основа без изменения поведения: `internal/rpc/batch.go` (`Batch`, `ItemError`, `ItemErrorFrom`, `Chunks`) и `internal/redact` (`Text`, таблица шаблонов). Тесты: `ItemErrorFrom` для каждого кода `rpc` и для обычной ошибки (`failure`, `internal error`); `Chunks` на 0, 1, 100, 101, 1000 id; `redact.Text` на номерах с разделителями, URL с паролем, `api_hash`, строке сессии, а также на id чатов, id сообщений, курсорах и датах, которые не меняются. Схема и ответы не меняются: `go test ./internal/cli -run 'TestSchema|TestDocs'` зелёный.
- [ ] 3. CLI частичного успеха: `internal/cli/partial.go`, поле `printed` в `cmdError`, строка в `success`, `--fields` сохраняет `partial` и `errors`, сводка в `--human`, текст кода 1 в `exitcode`. Тесты на подменном сервере RPC (`DialRPC`): частичный результат даёт один конверт и код 1 во всех форматах (`json`, `toon`, `human`), `--fields messages.id` оставляет `partial` и `errors`, полный успех даёт код 0 и прежний вывод.
- [ ] 4. Daemon и `tgclient`: `batch_write.go` (части, сопоставление `random_id`, сбор `ItemError`, остановка после `FLOOD_WAIT` и `not_authorized`), правки `deleteMessages`, `forwardMessages`, `messageDelete`, `messageForward`, предел 1000 id в `messageIDs`. Тесты на подменном invoker и подмене Telegram: пересылка 3 id с ответом на 2 даёт `partial`, `source_id` и `not_found` для третьего; 250 id удаления дают три запроса по 100, 100 и 50; `FLOOD_WAIT` на второй части даёт `deleted` первой части и `flood_wait` с `retry_after_seconds` для остальных, третий запрос не отправлен; журнал удалений: пары `intent` и `result` на каждую часть, сбой `intent` второй части не отправляет её и следующие; база помечает удалёнными только успешные id; ключ идемпотентности сохраняет частичный результат, повтор возвращает его с `replayed: true` без запроса в Telegram; все id неудачны даёт обычную ошибку первого кода.
- [ ] 5. Ошибки без утечек (9.5): `safeClientError` вместо строки 40 `errors.go`; `redact.Text` в `toError`, `failure`, `warn`; правки путей из таблицы шага 1, которые фильтр не закрывает (вход никогда не повторяет введённое); тест-ловушка через `mapError`, `rpc.Server`, `translate`, `Execute` и `daemon.log`; статический тест `guard_test.go` (Q9); тест полноты «у команды `account_write` с вариативным аргументом id результат встраивает `rpc.Batch`». Если `sched` уже в `main`: `ScheduledResult` встраивает `Batch`, части и ошибки по образцу удаления.
- [ ] 6. Подсказки команд (3.8): `values` у `--format` и `--client`, `ResultVariants` и вариант `--foreground` у `daemon start` (Q11), ответ группы без подкоманды и синтаксис в ошибке использования (Q6), аргумент `schema [command...]` (Q7). Тесты: `TestEveryCommandIsAnnotated` и `TestSchemaNeedsNoDaemonAndIsDeterministic` зелёные; каждая группа без подкоманды даёт код 2 и конверт с перечнем подкоманд; `tg message send` без аргументов называет синтаксис; `tg schema chat` возвращает только команды `chat ...`, `tg schema nope` код 2; `TestSchemaDaemonStartDescribesDefaultResult` дополнен вариантом `--foreground`.
- [ ] 7. Документы через `docwriter` и `glavred` по `docs_chain.md`. `docs/cli/overview.md`: раздел «Partial success of batch commands» (конверт, код 1, `errors`, `--fields`, `--human`), текст про ошибки без секретов, группы без подкоманды, `tg schema [command...]`; `docs/cli/messages-write.md`: `message delete` и `message forward` (части по 100, предел 1000, журнал по частям, `source_id`, пример частичного ответа, коды выхода); `docs/cli/daemon-and-auth.md`: ответ `--foreground`; `docs/cli/notes-and-search.md`: раздел `tg schema` с аргументом; `docs/contracts.md`: абзац «Batch commands and partial success» с тремя правилами для потоков; `docs/skill/tg-cli/SKILL.md`: как агенту читать код 1 с `partial` и повторять только `errors`; `FEATURES.md`: формулировки 3.6, 3.8, 9.5 и их сценарии по ответам, строки статуса после слияния `status_audit`; `AGENTS.md`: `redact` в Layout; решение в `.agents/decisions/` и строка в `_TOC.md`. Таблицы флагов и кодов выхода пересобираются `go test ./internal/cli -run TestDocsGenerated -update-docs`, затем `go test ./internal/cli -run 'TestDocs'` зелёный (у каждой команды и флага есть раздел, ссылки целы).
- [ ] 8. Живая проверка Claude. Чтение (разрешено правилами): `tg schema` и `tg schema chat` без Daemon; `tg chat`, `tg message send` без аргументов; `message forward --dry-run` и `message delete --dry-run` на «Браин Тест» с 150 id (число частей в ответе); ошибка сети при остановленном Daemon не содержит пути к сессии и ключей. Запись только по Q10: одна пересылка в «Браин Тест» двух существующих сообщений и одного несуществующего id (ожидание: код 1, `partial`, `source_id` у двух, `not_found` у третьего), затем удаление двух пересланных копий с `--yes`. В отчёт идут только коды, id и числа, без текстов сообщений.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/rpc/... ./internal/redact/... ./internal/tgclient/... ./internal/daemon/... ./internal/cli/... ./internal/output/...` | зелёный | not_run |
| Полный успех не изменился | `go test ./internal/cli -run 'TestSchemaFieldsMatchRealResponses|TestEveryCommandResponseIsCompared'` и существующие тесты CLI | зелёные без правки ожиданий, кроме `source_id` и списка из шага 1 | not_run |
| Частичный успех | тесты шага 3 и 4 | один конверт, код 1, `partial`, `errors` с `code` | not_run |
| Нет лишних записей | тесты `internal/tgclient` (`batch_write_test.go`) | число запросов равно числу частей до первой остановки, повторов нет | not_run |
| Журнал удалений | тесты `internal/daemon` | пары `intent` и `result` на часть, сбой `intent` останавливает | not_run |
| Ловушка секретов | тест шага 5 | ни одна строка вывода и журнала не содержит подставленных секретов | not_run |
| Статический запрет склейки | `go test ./internal/redact -run TestNoErrorTextInRPCErrors` | зелёный, исключения перечислены | not_run |
| Схема | `go test ./internal/cli -run 'TestEveryCommandIsAnnotated|TestSchema'` | зелёный; `tg schema` одинаков от запуска к запуску | not_run |
| Справочник | `go test ./internal/cli -run TestDocsGenerated -update-docs`, затем `go test ./internal/cli -run TestDocs` | таблицы пересобраны, тесты зелёные | not_run |
| Миграций нет | `git diff --stat main -- internal/store/migrations` | пусто | not_run |
| Живая проверка | шаг 8 | коды и числа по ожиданиям | not_run |

## Deploy / rollback

- [ ] Выкладки нет. После слияния пересобрать `tg` (`just build`) и перезапустить Daemon (`tg daemon stop`, `tg daemon start`), иначе старый Daemon отвечает без `partial` и без фильтра.
- [ ] Откат: откат ветки. База и журнал удалений совместимы: формат строк журнала прежний, меняется только их число.

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261115_0241_agent_contract/`.
- [ ] Временные файлы не в git (`/var/` в `.gitignore`).
- [ ] Пересланные в живой проверке копии удалены.

## Definition of done

- [ ] Код изменён только в рамках плана и раздела «Файлы потока».
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.
- [ ] Решение записано в `.agents/decisions/` и `_TOC.md`; `FEATURES.md`, `AGENTS.md`, `docs/` обновлены.

## Known facts

- `tg schema` на `main`: 53 команды, 78 187 байт JSON, `--format toon` 80 195 байт; `schema_version: 1`.
- Пакетные команды на `main`: `message delete <chat> <id>...` (`dangerous`, `--yes`) и `message forward <from> <to> <id>...`; в `sched`: `message scheduled delete|send-now <chat> <id>...` с `ScheduledResult{chat_id, messages, deleted, dry_run}`. Остальные списки (`--file` альбома, `--option` опроса, `--tag`, `--chat` у `watch`, `--scope`) это параметры одного действия, а не пакет: альбом уходит одним `messages.sendMultiMedia`.
- gotd `DeleteBuilder.Messages` и `RevokeBuilder.Messages` шлют все id одним запросом; в проекте разбиения нет ни в CLI, ни в Daemon, ни в `tgclient`.
- Для чата не канала Daemon перед удалением проверяет по базе, что каждый id принадлежит этому чату (`internal/daemon/write.go`, цикл `ChatsOfMessages`), потому что id личных чатов и групп общие для аккаунта.
- `forwardMessages` при неполном ответе возвращает `CodeTelegram` «Telegram did not return all forwarded message ids» (`internal/tgclient/write.go:314`); `session.complete` не вызывается, ключ идемпотентности остаётся незавершённым.
- `writeOutcome` (`internal/daemon/write.go:474`) даёт `ok`, `failure`, `not_authorized`, `telegram_error` и другие коды для журнала удалений.
- CLI сейчас: `tg mesage` даёт код 2 с «Did you mean this? message»; `tg chat` и `tg message` печатают текст справки cobra и код 0; `tg message send` без аргументов даёт код 2 «accepts 1 arg(s), received 0»; `tg message history -100123` даёт код 2 с подсказкой про `--`.
- `schema.Fields` раскрывает встроенные структуры (`internal/schema/schema.go`, комментарий про embedded), поэтому `Batch` попадёт в поля схемы без правок пакета `schema`.
- `mapError` (`internal/tgclient/errors.go:16`) уже сводит ошибку Telegram к типу TL, `FLOOD_WAIT` к `retry_after_seconds`, неавторизованность и «не найдено» к своим кодам; утечка возможна только в последней строке.
- Несовпадение `--expect-user` называет ожидаемое значение, id и `username` аккаунта, но не номер (`internal/daemon/account_guard.go:56`); это задокументировано и не меняется.
- Справочник: таблицы флагов, глобальных флагов и кодов выхода между маркерами `<!-- schema:... -->` (54 таблицы флагов) пересобираются тестом `TestDocsGenerated`; поля ответов в таблицы не входят.

## Open questions

Каждый вопрос решается отдельно. Рекомендуемый вариант первый. Владелец делегировал решения менеджеру.

| # | Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| Q1 | Какой конверт у частичного успеха | Форма ответа для всех пакетных команд и для агентов, которые читают только `ok` | A: `{"ok":true,"data":{...,"partial":true,"errors":[...]}}`, код 1; B: `{"ok":false,"error":{"code":"failure","message":"2 of 5 items failed"},"data":{...}}`, код 1 (у конверта ошибки появляется `data`); C: новый код выхода 10 `partial` (противоречит 3.6.3) | A. Совпадает с 3.6 («в ответе `data.partial`»), форма конвертов не меняется. Цена: агент, который смотрит только на `ok`, примет частичный успех за полный; защита: код 1 и правило в навыке | принято |
| Q2 | Что отвечать, если не выполнился ни один элемент | Определяет код выхода при полном отказе пакета | A: обычный конверт ошибки с кодом и текстом первой ошибки, код выхода по ней (6, 7, 5); B: частичный конверт без успешных элементов и код 1 | A. Агент получает тот же код, что у одиночного вызова (например 6 и паузу при `FLOOD_WAIT`). Цена: ошибки остальных элементов в ответ не попадают | принято |
| Q3 | Как обрабатывать больше id, чем Telegram принимает за запрос (по памяти 100, уточняет шаг 1) | Без разбиения у удаления не бывает частичного успеха, а 101 id ломает весь вызов | A: Daemon режет на части по 100, не больше 1000 id за вызов (больше даёт код 2), после `FLOOD_WAIT` и `not_authorized` остальные части не шлются; B: больше 100 id даёт код 2, без частей (частичный успех только у пересылки); C: части без верхнего предела | A. Цена: до 10 запросов записи за вызов и риск `FLOOD_WAIT` посреди пакета, отражённый в `errors`; число 1000 выбрано планировщиком как разумный потолок, не требование Telegram | принято |
| Q4 | Что делать с ключом идемпотентности при частичной пересылке | Повтор с тем же ключом не должен дублировать уже пересланное | A: частичный результат сохраняется под ключом, повтор возвращает его с `replayed: true` и кодом 1; неудавшиеся id агент пересылает новым вызовом с новым ключом; B: частичный результат не сохраняется, повтор с ключом шлёт все id снова с теми же `random_id` | A. Дублей нет при любом поведении Telegram. Цена: повтор того же вызова не дожмёт неудавшиеся id | принято |
| Q5 | Действует ли `--fields` на `partial` и `errors` | Без них код 1 приходит без причины | A: `partial` и `errors` остаются всегда; B: фильтруются как остальные поля | A. Цена: `--fields` у пакетной команды перестаёт быть точным списком полей, это исключение описывается в справочнике | принято |
| Q6 | Подсказывать ли синтаксис в ошибках и что отвечает группа без подкоманды | Сейчас `tg chat` печатает текст справки и код 0, а ошибка аргументов не называет синтаксис | A: группа без подкоманды даёт конверт `usage` со списком подкоманд и код 2; ошибка использования дополняется строкой `usage: tg ... <args> [flags]`; `--help` остаётся текстом; B: оставить как есть; C: как A и вдобавок `--help` в JSON | A. Цена: `tg chat` меняет код 0 на 2 (намеренное изменение инварианта 2); человек, набравший группу, видит JSON вместо справки и пишет `--help` | принято |
| Q7 | Добавить ли отбор схемы по команде `tg schema [command...]` | Полная схема 78 КБ, агенту часто нужна одна команда | A: необязательный путь команды или группы, тот же документ с отобранными `commands`, неизвестный путь код 2; B: без отбора | A. Цена: новый аргумент команды `schema`, раздел в справочнике и навыке | принято |
| Q8 | Какой текст отдавать для ошибки клиента Telegram, которая не является ошибкой TL | Сейчас сюда склеивается сырой текст gotd, возможна утечка секрета прокси и внутренних адресов | A: белый список: тип TL как сейчас, остальное фиксированным текстом по категории (сеть, тайм-аут, неожиданный ответ), сырой текст только в `daemon.log` после фильтра; B: сырой текст через фильтр `redact` | A. Цена: в ответе меньше подробностей, для разбора нужен `tg daemon logs` | принято |
| Q9 | Вводить ли статический тест, запрещающий `err.Error()` и `%v` от ошибки в тексте `rpc.NewError` | Защищает код других потоков от новых утечек, но может уронить их ветки при слиянии | A: да, с явным списком исключений и понятным текстом падения; B: нет, только тест-ловушка | A. Цена: потоки, которые склеивают текст ошибки, правят код при слиянии | принято |
| Q10 | Разрешена ли запись в живой проверке шага 8 | Правила разрешают запись только по явному указанию в задаче | A: одна пересылка в «Браин Тест» двух существующих и одного несуществующего id, затем удаление двух копий с `--yes`; B: только `--dry-run` и тесты | A. Задать перед шагом 8. Цена B: частичный успех и `source_id` не проверены на живом аккаунте | принято |
| Q11 | Кто закрывает пробел схемы «ответ `daemon start --foreground` не описан» | Тот же файл `daemon_cmd.go` правит поток `daemon_lifecycle` | A: этот поток: общий механизм `ResultVariants` в `spec.go` и одна правка аннотации `daemon start`; B: поток `daemon_lifecycle` | A. Механизм схемы принадлежит этому потоку. Цена: одна строка конфликта в `daemon_cmd.go` при слиянии | принято |
