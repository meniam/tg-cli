# Пошаговый план: отложенные сообщения, черновики и индикатор набора

ID: 20261114_2232_scheduled_drafts_typing
Created: 2026-11-14
Owner: Мязин Евгений
Branch: feature/20261114_2232_scheduled_drafts_typing (создана заранее в worktree `../tg-cli-sched`, код не менялся)
PR: —

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: draft
- Last update: 2026-11-14
- Next safe step: менеджер задаёт владельцу открытые вопросы В1 до В9 по одному (раздел «Open questions»), ответы записываются в Decision log; после этого статус `approved` и шаг 1 (аудит Codex Terra, без кода). Код начинается с шага 2.
- Параллельная работа: поток один из пяти Codex-кодеров, каждый в своём worktree; границы файлов в разделе «Файлы потока», миграция базы зарезервирована под номером 0007.

## Context and sources

- Read: `AGENTS.md`, `INTENT.md`, `FEATURES.md` (5.1 до 5.8, 8.2, 9.6, 10.10.2, статус и открытые вопросы), `.agents/rules/plans.md`, `.agents/rules/project/contracts.md`, план-образец `20261114_1933_reactions_pins_polls.md`; код: `internal/daemon/write.go` (`writeReference`, обработчики записи, `--dry-run`), `internal/tgclient/write.go` (`sendPreparedText`, `writtenMessages`), `internal/tgclient/actions.go`, `internal/tgclient/throttle.go` (`classifyRequest`, `writeRequest`), `internal/tgclient/updates_events.go` (`updateDispatcher`), `internal/rpc/write.go` (`SendParams`), `internal/rpc/api.go` (`Message`, имена методов), `internal/daemon/read.go` (`parseTime`, RFC 3339), `internal/cli/read_cmd.go` (флаги `--since`, `--until`).
- Verified by commands: `go list -m github.com/gotd/td` даёт v0.166.0 в `/Users/eugene/go/pkg/mod/github.com/gotd/td@v0.166.0`; по сгенерированным файлам `tg/tl_messages_*_gen.go`, `tg/tl_draft_message_gen.go`, `tg/tl_send_message_action_gen.go`, `tg/tl_handlers_gen.go`, `tg/dispatcher_any.go`, `telegram/message/{builder,draft,scheduled,typing}.go`, `telegram/handle_updates.go` (итоги в Known facts); `rg` по `internal/`: слов `Scheduled`, `Draft`, `SetTyping` в коде нет.
- Not verified: поведение сервера (пределы отложенных сообщений, дата в прошлом, что приходит в ответе на отложенную отправку и на `sendScheduledMessages`, приходит ли сработавшее отложенное сообщение в поток обновлений, сколько секунд держится индикатор набора). Причина: живой Telegram в аудите не вызывался; это шаг 1 и живая проверка шага 6.

## Goal

Агент в чате из списка `write` планирует сообщение на заданное время, видит и отменяет запланированное, сохраняет, читает и очищает черновик и включает индикатор набора. Все действия проходят тот же барьер, что запись сообщений.

## Problem and contract

Тип плана: product (новые команды записи и чтения). FEATURES 5.6, 5.7 и 5.8 сегодня `[Must]` без реализации; `AGENTS.md` и `contracts.md` прямо перечисляют их как «Not done».

Контракт ниже это предложение автора плана. Пункты, помеченные «(В*n*)», зависят от ответа владельца на открытый вопрос с этим номером и меняются по ответу.

Общее для всех трёх функций:

- Отложенная отправка, удаление и немедленная отправка отложенного, сохранение и очистка черновика, индикатор набора это запись в аккаунт: чат должен быть в `write` (иначе код 9 `write_disabled`), закрытый для чтения чат даёт тот же `not_found`, что неизвестный, до обращения к Telegram; проверку делает существующий `writeReference(ctx, ref, true)`.
- Класс безопасности `account_write`, `dangerous` не задан, `--yes` не нужен (условие задания и правило записи из `AGENTS.md`, раздел Security). Каждая команда записи имеет `--dry-run`: все проверки Daemon, ответ с `dry_run: true`, Telegram не вызывается, база не меняется.
- Чтение (список отложенных, чтение черновика, список черновиков) имеет класс `read` и проходит фильтр чтения: закрытый чат `not_found`, у списка черновиков закрытые чаты отбрасываются до ответа.
- Записи не повторяются автоматически: новые запросы добавляются в `writeRequest` дросселя (`MessagesSaveDraftRequest`, `MessagesSetTypingRequest`, `MessagesDeleteScheduledMessagesRequest`, `MessagesSendScheduledMessagesRequest`); `MessagesSendMessageRequest` со `schedule_date` уже там. `MessagesSendScheduledMessagesRequest` дополнительно попадает в `classifyRequest` как отправка (корзина отправки по чату), потому что отправляет сообщения сразу.
- Журнал Daemon без параметров и текстов (действует `Server.call`); тексты черновиков и отложенных сообщений в журнал не попадают.

5.6 Отложенные сообщения:

- `tg message send <чат> --at ВРЕМЯ [прежние флаги]`: то же сообщение уходит в `messages.sendMessage` с `schedule_date`. `ВРЕМЯ` в RFC 3339 со смещением (`2026-11-15T09:00:00+03:00`), как `--since` у `message history` (В3 может добавить `--in 2h`). Время в прошлом и ближе 10 секунд от текущего дают код 2 до Telegram; верхняя граница по документации Telegram 365 дней, сервер отвечает `SCHEDULE_DATE_TOO_LATE` (код 2 с пояснением), превышение числа отложенных `SCHEDULE_TOO_MUCH` (код 5 с пояснением), `SCHEDULE_DATE_INVALID` код 2.
- Ответ `{chat_id, id, date, scheduled: true}`: `id` это идентификатор отложенного сообщения (у отложенных своя нумерация, она не совпадает с обычными id), `date` это запланированное время.
- Отложенное сообщение не пишется в таблицу `messages`: его id из другой нумерации перезаписал бы обычное сообщение с тем же id (В4). Когда сервер отправит его в срок, обычное сообщение придёт в поток событий и сохранится как любое новое (проверяет шаг 6).
- `tg message scheduled list <чат>` (`messages.getScheduledHistory`, класс `read`): список `{id, chat_id, date, text, media_type, reply_to}` по возрастанию даты; `text` помечен `untrusted`, как у `Message` (переиспользуется `rpc.Message` и `toMessage`).
- `tg message scheduled delete <чат> <id>... [--dry-run]` (`messages.deleteScheduledMessages`): отменяет отложенные; ответ `{chat_id, deleted: [id...]}`. В `deletions.jsonl` не пишется: сообщение никому не доставлено (предложение автора, владелец может вернуть журнал при утверждении плана).
- `tg message scheduled send-now <чат> <id>... [--dry-run]` (`messages.sendScheduledMessages`): отправляет сразу; ответ `{chat_id, messages: [{id, date}]}` с новыми обычными id; отправленные сообщения сохраняются в базу с `out: true` по объектам из ответа сервера, если ответ их несёт (шаг 1 проверяет, что именно приходит).
- Объём команд и то, какие ещё команды получают `--at`, решают В1 и В2.

5.7 Черновики:

- `tg chat draft get <чат>` (класс `read`): черновик из `messages.getPeerDialogs` (`Dialog.Draft`); ответ `{chat_id, text, date, reply_to, empty}`; пустой черновик это `empty: true` и код 0. Поле `text` помечено `untrusted` (В7).
- `tg chat draft set <чат> [--text ТЕКСТ] [--reply-to ID] [--dry-run]` (`messages.saveDraft`): текст берётся как у `message send` (`--text` или stdin, пустой и длиннее 4096 символов дают код 2); черновик синхронизируется на все устройства владельца и заменяет прежний. `--dry-run` дополнительно сообщает `replaces: true`, если у чата уже есть непустой черновик (читается без записи). Ответ `{chat_id, text_length}`. Форматирование решает В6.
- `tg chat draft clear <чат> [--dry-run]` (`messages.saveDraft` с пустым текстом, как `Builder.ClearDraft` в gotd); ответ `{chat_id, cleared: true}`.
- `tg chat draft list` (`messages.getAllDrafts`, класс `read`) и хранение черновиков в базе (10.10.2) решает В5.
- Черновики в базу не пишутся, если В5 не выберет иное.

5.8 Индикатор набора:

- `tg chat typing <чат> [--action ДЕЙСТВИЕ] [--topic N] [--cancel] [--dry-run]` (`messages.setTyping`): один запрос; по документации Telegram клиенты показывают индикатор около 5 секунд, поэтому для долгого набора агент повторяет вызов (В8). `--cancel` отправляет `sendMessageCancelAction`. Ответ `{chat_id, action}`.
- Набор действий решает В9; по умолчанию `typing`.
- Индикатор видит собеседник: это раскрывает, что аккаунт активен, поэтому команда под `write` наравне с отправкой.

Чтение, поток и база:

- Поток событий не меняется: `OnAnyNewMessage` в gotd обрабатывает только `UpdateNewMessage` и `UpdateNewChannelMessage`, поэтому `UpdateNewScheduledMessage`, `UpdateDeleteScheduledMessages` и `UpdateDraftMessage` в поток не попадают и сейчас (событий для них нет, вне плана).
- Релиз одним слоем; откат: откат ветки. Миграций базы нет, если В4 и В5 не выберут хранение; если выберут, это одна миграция с зарезервированным номером 0007 (см. «Файлы потока»).

## Scope

- В плане: `internal/rpc` (типы и методы), `internal/tgclient` (запросы, разбор ответов, перевод ошибок, дроссель), `internal/daemon` (обработчики, проверки, `--dry-run`, сохранение отправленного), `internal/cli` (команды, флаги, схема), тесты, документы, живая проверка в «Браин Тест».
- Вне плана: повторяющиеся отложенные сообщения (`schedule_repeat_period`), отправка «когда собеседник будет в сети» (`schedule_date = 0x7FFFFFFE`), очистка всех черновиков аккаунта (`messages.clearAllDrafts`), черновики с медиа и эффектами, события потока для отложенных и черновиков, фоновый повтор индикатора набора внутри Daemon (если В8 не выберет его), остальное, что исключат ответы В1, В2, В5.
- Затрагиваются: `internal/{rpc,tgclient,daemon,cli}`, `AGENTS.md`, `.agents/rules/project/contracts.md`, `FEATURES.md`, `README.md`, `docs/skill/tg-cli/`, `.agents/decisions/`.

## Файлы потока

Параллельно работают пять Codex-кодеров, каждый в своём worktree. Чтобы ветки сливались без конфликтов, поток создаёт свои файлы, а общие файлы правит минимальной локальной вставкой.

Новые файлы, которыми владеет поток (другие потоки их не трогают):

| Слой | Файлы | Что внутри |
| --- | --- | --- |
| `internal/rpc` | `scheduled.go`, `drafts.go`, `typing.go` и их `_test.go` | параметры и результаты методов, проверка параметров (`ValidateScheduleAt`, `ValidateTypingAction` и подобные), имена действий набора |
| `internal/tgclient` | `scheduled.go`, `drafts.go`, `typing.go` и их `_test.go` | клиентские методы, запросы gotd, разбор ответа с `UpdateNewScheduledMessage`, чтение `Dialog.Draft` и `getAllDrafts` |
| `internal/daemon` | `scheduled.go`, `drafts.go`, `typing.go` и их `_test.go` | обработчики методов, `writeReference`, `--dry-run`, сохранение отправленного `send-now` |
| `internal/cli` | `scheduled_cmd.go`, `draft_cmd.go`, `typing_cmd.go` и их `_test.go` | команды `message scheduled ...`, `chat draft ...`, `chat typing`, разбор `--at` (и `--in` по В3) |
| `internal/store` | `migrations/0007_<имя>.sql`, `scheduled.go` или `drafts.go` и тесты | только если В4 или В5 выберут хранение |
| решения | `.agents/decisions/20261114_HHMM_<имя>.md` | решение плана |
| временное | `var/agents/plans/20261114_2232_scheduled_drafts_typing/` | отчёт аудита, вне git |

Общие файлы. Поток вносит в них только локальную вставку рядом с соседними строками того же вида, без переупорядочивания, переименований и правки чужих строк:

| Файл | Вставка |
| --- | --- |
| `internal/rpc/api.go` | константы `MethodMessageScheduledList`, `MethodMessageScheduledDelete`, `MethodMessageScheduledSendNow`, `MethodChatDraftGet`, `MethodChatDraftSet`, `MethodChatDraftClear`, `MethodChatDraftList`, `MethodChatTyping` (по ответам В1, В5) |
| `internal/rpc/write.go` | поле `At` (и `In` по В3) в `SendParams`; поле `Scheduled` в `SendResult` |
| `internal/daemon/daemon.go` | строки `d.handle(...)` для новых методов |
| `internal/daemon/telegram.go` | методы новых функций в интерфейсе `Telegram` |
| `internal/daemon/daemon_test.go` | методы поддельного `Telegram` для новых функций |
| `internal/daemon/write.go` | передача `At` в `messageSend` и отказ от записи отложенного в `messages` |
| `internal/tgclient/write.go` | `schedule_date` в `sendPreparedText`; ветка для отложенного ответа рядом с `writtenMessages` |
| `internal/tgclient/throttle.go` | четыре запроса в `writeRequest`, `MessagesSendScheduledMessagesRequest` в `classifyRequest` |
| `internal/tgclient/errors.go` | строки `case` для `SCHEDULE_*`, `USER_IS_BLOCKED` и других ошибок шага 1 |
| `internal/cli/read_cmd.go` | `a.messageScheduledCommand()` в `AddCommand` группы `message` |
| `internal/cli/chat_cmd.go` | `a.chatDraftCommand()`, `a.chatTypingCommand()` в `AddCommand` группы `chat` |
| `internal/cli/write_cmd.go` | флаг `--at` (и `--in`) у `message send` |
| `internal/cli/cli_test.go` | поддельные ответы новых методов, если тест полноты схемы их требует |
| документы | `FEATURES.md` (строки 5.6, 5.7, 5.8, 10.10.2 и их статус), `README.md`, `AGENTS.md` (Running, Decisions made), `.agents/rules/project/contracts.md` (свой пункт), `docs/skill/tg-cli/`, `.agents/decisions/_TOC.md` (своя строка) |

Правила общих файлов:

- Вставка в общий файл идёт отдельным коммитом шага или вместе с кодом шага, но без форматирования чужих частей файла; `gofmt` меняет только вставку.
- Если общий файл в `main` изменил другой поток, ветка перебазируется на `main` до проверки шага, конфликт решается сохранением обеих вставок.
- Миграция: номер 0007 зарезервирован за этим потоком (0004 идемпотентность, 0005 контакты, 0006 папки и состояние); другие номера поток не занимает. `listMigrations` требует номера подряд с 1, поэтому файл 0007 появляется в ветке только после того, как 0004, 0005 и 0006 вошли в `main` и ветка перебазирована. Если В4 и В5 выберут вариант без хранения, миграция не пишется и менеджер освобождает номер.

## Main invariant

1. Без права `write` на чат и без видимости чата ни один запрос записи в Telegram не уходит; закрытый чат отвечает как неизвестный.
2. Повторов записи нет: FLOOD_WAIT на новых методах даёт код 6 без повторного запроса.
3. Существующие команды и их ответы не меняются; `message send` без `--at` отправляет тот же запрос, что раньше.
4. `--dry-run` не вызывает методы записи Telegram и не меняет базу.
5. Отложенные сообщения не попадают в таблицу `messages` под своими id.
6. `internal/cli` не импортирует `tgclient`, gotd, `store`.
7. Ни тест, ни проверка по умолчанию не обращаются к живому Telegram; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: id отложенного сообщения совпадёт с обычным и испортит локальную базу. Стратегия: отложенные не сохраняются в `messages`; тест на отсутствие записи в базу после `--at`.
- Риск: `writtenMessages` не находит id в ответе на отложенную отправку (в ответе `UpdateNewScheduledMessage`, а не `UpdateNewMessage`) и возвращает код 5 при успешной на деле записи. Агент повторит и создаст дубль. Стратегия: отдельный разбор ответа с `UpdateNewScheduledMessage` и `UpdateMessageID`; тест на подменённом ответе; шаг 1 фиксирует реальную форму ответа по исходникам gotd, шаг 6 по живому ответу.
- Риск: агент заменит черновик, который владелец набирает на другом устройстве. Стратегия: `write` на чат, `--dry-run` показывает `replaces`, предупреждение в навыке; `--yes` не вводится по условию задания (владелец может изменить при В5).
- Риск: частые вызовы индикатора тратят общую корзину дросселя (18 запросов в секунду при 60%) и раскрывают активность аккаунта. Стратегия: один запрос на вызов, без повторов на FLOOD_WAIT, в навыке рекомендация не чаще раза в 5 секунд.
- Риск: время без смещения или в другом поясе даёт сообщение не в тот час. Стратегия: только RFC 3339 со смещением, `Z` допустим; без смещения код 2; ответ возвращает `date` в UTC.
- Риск: предел дат и числа отложенных известен только из документации Telegram. Стратегия: локально проверять только прошлое и 10 секунд; остальное по ошибке сервера; шаг 6 проверяет на живом аккаунте одну границу (`SCHEDULE_DATE_TOO_LATE` на дате через 400 дней).
- Риск: пять потоков правят одни и те же общие файлы (`api.go`, `daemon.go`, `throttle.go`, `errors.go`, `FEATURES.md`) и сливаются с конфликтами. Стратегия: своя логика только в файлах потока, в общих файлах минимальная вставка по разделу «Файлы потока», перебазирование на `main` перед проверкой каждого шага.
- Риск: миграция 0007 в ветке без 0004 до 0006 ломает `store.Open` (номера должны идти подряд), и `just check` в worktree падает. Стратегия: миграция пишется последней и только после слияния 0004 до 0006 в `main`; при рекомендованных ответах В4 и В5 миграции нет вовсе.
- Риск: тестовые отложенные сообщения сработают в «Браин Тест» позже, без присмотра. Стратегия: в живой проверке время не дальше 3 минут, всё лишнее удаляется командой `message scheduled delete`, в конце `message scheduled list` пуст.

## Decision log

| Date | Decision | Reason | Confirmed |
| --- | --- | --- | --- |
| 2026-11-14 | Отложенные, черновики и индикатор набора это запись в аккаунт под списком `write` (код 9 вне него), с `--dry-run`, без `--yes`, не `dangerous` | Правило записи и Security в `AGENTS.md`; условие задания планировщику (было: задание менеджера по `AGENTS.md`; владельцем отдельно не подтверждено) | no |
| 2026-11-14 | Поток работает параллельно с четырьмя другими Codex-кодерами в своём worktree; свои файлы по теме, в общих файлах минимальная локальная вставка | Слияние пяти веток без конфликтов (было: дополнение задания менеджера) | no |
| 2026-11-14 | За потоком зарезервирована миграция store 0007; 0004, 0005, 0006 заняты другими потоками | Номера миграций идут подряд и не должны совпасть (было: дополнение задания менеджера) | no |
| 2026-11-14 | Шаг 1 это аудит без кода, исполнитель Codex Terra; код начинается с шага 2 | Порядок работы потоков (было: дополнение задания менеджера) | no |
| 2026-11-14 | В1. Какие операции с отложенными сообщениями делать: В) `message send --at`, `scheduled list`, `scheduled delete` и `scheduled send-now` (отправить сразу); правка отложенного заменяется отменой и новой отправкой. | В: агент планирует, видит, отменяет и при нужде отправляет сразу; правка дешевле заменяется отменой и новой отправкой (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-14 | В2. Какие команды получают `--at`: А) только `message send`: минимум. | А: 5.6.1 говорит о сообщении; остальные добавляются тем же флагом позже, когда понадобятся (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-14 | В3. В каком виде задаётся время: Б) А плюс `--in ДЛИТЕЛЬНОСТЬ` (Go duration, `90m`, `2h`), взаимоисключающие: около часа, агенту не нужно считать время | Б: агенту чаще нужно «через N минут», а ошибка в поясе при ручном счёте вероятна (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-14 | В4. Хранить ли отложенные сообщения в локальной базе: А) не хранить, `scheduled list` всегда спрашивает Telegram: без миграции. | А: список короткий (до 100 на чат), локальный поиск по запланированному не нужен для 5.6, сработавшее сообщение и так сохранится как обычное (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-14 | В5. Какие операции с черновиками делать: Б) А плюс `draft list` по всем чатам (`getAllDrafts`, закрытые отбрасываются): плюс около 2 часов. | Б: агент находит начатые владельцем ответы во всех чатах; база и темы отдельной задачей вместе с 10.10 (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-14 | В6. Форматирование черновика: А) только простой текст и `--reply-to`: дёшево. | А: черновик это заготовка для владельца, форматирование он доведёт сам; Б добавляется без смены контракта (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-14 | В7. Помечать ли текст черновика `untrusted` в схеме: А) помечать: текст мог прийти вставкой чужого сообщения, в `--human` текст идёт в рамке недоверенного текста; ничего не стоит. | А: черновик синхронизируется с других устройств и может содержать чужой текст, лишняя рамка дешевле подсунутой инструкции (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-14 | В8. Как долго держать индикатор набора: А) один запрос, индикатор около 5 секунд, агент повторяет сам; `--cancel` снимает: около 2 часов. | А: проще и без фоновых задач; Б можно добавить, если агенту станет неудобно (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-14 | В9. Какие действия доступны в `--action`: Б) `typing`, `upload_photo`, `upload_video`, `upload_document`, `record_voice`, `upload_voice`, `record_video`, `choose_sticker`, `cancel`: таблица имён, около часа. | Б: действия, которые агент реально делает перед отправкой медиа; служебные исключены (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |

Ответы на В1 до В9 записываются сюда строкой на вопрос с цитатой выбора владельца.

## Step 0. Fix decisions before changes

- [ ] Владелец ответил на В1 до В9, ответы в Decision log, контракт выше поправлен по ответам.
- [ ] Исполнитель Go-кода (Codex) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [ ] Ветка `feature/20261114_2232_scheduled_drafts_typing` в worktree `../tg-cli-sched` начинается от текущего `main`; если `main` ушёл вперёд, ветка перебазируется до шага 1.

## Step 1. Audit and baseline

Исполнитель Codex Terra в worktree `../tg-cli-sched`. Шаг не меняет код: только чтение, команды проверки и отчёт. Claude проверяет отчёт и переносит итоги в план.

- [ ] `just check` и `just build` на чистом дереве; результат в Known facts.
- [ ] Сверить список общих файлов из раздела «Файлы потока» с кодом: точные места вставок (функция, соседняя строка), нет ли ещё общих файлов (тест полноты схемы, регистрация в `cli.go`); расхождения внести в раздел.
- [ ] По исходникам gotd v0.166.0: какие обновления приходят в ответе `messages.sendMessage` со `schedule_date`, `messages.sendScheduledMessages` (ожидается `UpdateNewMessage` и `UpdateDeleteScheduledMessages` с `SentMessages`) и `messages.deleteScheduledMessages`; как `messages.getScheduledHistory` возвращает `MessagesMessagesClass` (включая `MessagesMessagesNotModified` при `Hash`), как из него собрать `rpc.Message` существующим `toMessage`.
- [ ] Черновик: `messages.getPeerDialogs` для одного чата (`Dialog.Draft`, `DraftMessage` и `DraftMessageEmpty`), `messages.getAllDrafts` (ответ `Updates` с `UpdateDraftMessage`, поля `Peer`, `TopMsgID`), очистка пустым `saveDraft`, `ReplyTo` у черновика.
- [ ] Индикатор: `MessagesSetTypingRequest{Peer, TopMsgID, Action}`, соответствие имён `--action` типам `SendMessage*Action`, ошибки в канале без прав.
- [ ] Как `internal/tgclient/errors.go` переводит ошибки и что добавить: `SCHEDULE_DATE_TOO_LATE`, `SCHEDULE_DATE_INVALID`, `SCHEDULE_TOO_MUCH`, `SCHEDULE_STATUS_PRIVATE`, `MESSAGE_ID_INVALID` у отложенных, `CHAT_WRITE_FORBIDDEN`, `USER_IS_BLOCKED`.
- [ ] Как CLI строит группы команд (`message`, `chat`) и подкоманды, как схема описывает класс и `dangerous`, как тест полноты схемы перечисляет методы.
- [ ] Итоги в Known facts, непокрытое в Risks и Open questions; отчёт Codex в `var/agents/plans/20261114_2232_scheduled_drafts_typing/audit.md` (в git не входит).

## Implementation steps

Код начинается здесь. Каждый шаг выполняет Codex в worktree `../tg-cli-sched`, проверяет Claude (тесты, дифф, `just check`), коммитит Claude. Каждый шаг закрывает одну функцию от `rpc` до CLI, чтобы коммиты были независимы. Логика шага живёт в файлах потока; в общих файлах только вставки из раздела «Файлы потока». Перед проверкой шага ветка перебазируется на `main`, если `main` ушёл вперёд. Миграция 0007, если она нужна по В4 или В5, пишется отдельным последним коммитом своего шага после слияния 0004 до 0006 в `main`.

- [ ] 2. Отложенные сообщения (5.6): `--at` у `message send` (и у команд по В2), разбор и проверка времени в CLI и Daemon, поле `schedule_date` в запросе, разбор ответа с `UpdateNewScheduledMessage`, ответ с `scheduled: true`, отказ от записи в `messages`; методы `message.scheduled_list`, `message.scheduled_delete`, `message.scheduled_send_now` (объём по В1); добавление запросов в `writeRequest` и `classifyRequest`; перевод ошибок `SCHEDULE_*`; команды `message scheduled list|delete|send-now`, схема. Тесты на подмене Telegram и Daemon без сокетов: параметры каждого запроса, время в прошлом, ближе 10 секунд и без смещения код 2 до Telegram, чат вне `write` код 9 без вызова Telegram, закрытый чат `not_found`, `--dry-run` без вызова, отложенное не попало в базу, отправленное `send-now` попало с `out: true`, FLOOD_WAIT без повтора, `message send` без `--at` отправляет прежний запрос, полнота схемы.
- [ ] 3. Черновики (5.7): методы `chat.draft_get`, `chat.draft_set`, `chat.draft_clear` (и `chat.draft_list` по В5), чтение через `messages.getPeerDialogs`, запись через `messages.saveDraft`, `replaces` в `--dry-run`, пометка `untrusted` по В7, команды `chat draft get|set|clear` (`list`), схема. Тесты: параметры запроса, пустой черновик, `reply_to`, текст из stdin и `--text`, пределы длины, чат вне `write` код 9 для `set` и `clear`, `get` в закрытом чате `not_found`, `list` отбрасывает закрытые чаты, FLOOD_WAIT без повтора, полнота схемы.
- [ ] 4. Индикатор набора (5.8): метод `chat.typing`, `MessagesSetTypingRequest` с `TopMsgID` для `--topic`, `--cancel`, перечень действий по В9 (неизвестное действие код 2 до Daemon), добавление в `writeRequest`, команда `chat typing`, схема. Тесты: тип действия в запросе, `--topic`, `--cancel`, чат вне `write` код 9, `--dry-run`, FLOOD_WAIT без повтора, ошибка канала без прав в код 5.
- [ ] 5. Документы через `docwriter` и `glavred` по `docs_chain.md`: `FEATURES.md` (5.6, 5.7, 5.8 с подпунктами и статусом, строка 10.10.2 по В5), `README.md`, навык `docs/skill/tg-cli/` (предупреждения про замену черновика и частоту индикатора), `.agents/rules/project/contracts.md` (пункт про новые команды, убрать их из «Not done»), `AGENTS.md` (строка Running и Decisions made), решение в `.agents/decisions/` и строка в `_TOC.md`.
- [ ] 6. Живая проверка Claude в «Браин Тест» (временный `access.json` с `write` только для этого чата, затем удалён), с паузами между вызовами: `--dry-run` всех команд записи; отложенное сообщение через 2 минуты, `message scheduled list` показывает его, одно отложенное отменено `delete`, второе отправлено `send-now`, третье сработало само и найдено `message history` и `message watch`; дата через 400 дней даёт ошибку сервера (ожидается код 2); черновик сохранён, прочитан `chat draft get`, очищен; индикатор `typing` и `--cancel` приняты сервером (код 0), владелец подтверждает видимость черновика и индикатора в своём клиенте; чат вне `write` код 9. Тестовые сообщения удаляются `message delete --yes --revoke`, `message scheduled list` в конце пуст, текст сообщений в вывод не попадает.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/rpc/... ./internal/tgclient/... ./internal/daemon/... ./internal/cli/...` | зелёный | not_run |
| Без `write` запроса нет | тесты шагов 2 до 4 | счётчик вызовов подмены равен нулю | not_run |
| Нет повтора на FLOOD_WAIT | тесты шагов 2 до 4 | один вызов на каждый новый метод | not_run |
| Отложенное не в `messages` | тест шага 2 | строк с id отложенного нет | not_run |
| Прежний `message send` | тест шага 2 | запрос без `schedule_date` побайтно прежний | not_run |
| Полнота схемы | тест `internal/cli` | у новых команд класс, `dangerous: false`, тип результата | not_run |
| Живая проверка | шаг 6 | числом: запланировано, отменено, отправлено сразу, сработало само, черновик сохранён и очищен, индикатор принят | not_run |

## Deploy / rollback

- [ ] Выкладки нет. Откат: откат ветки. Если живая проверка оставила отложенные сообщения, их отменяет `message scheduled delete` до отката.

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261114_2232_scheduled_drafts_typing/`.
- [ ] Временные файлы не в git (`/var/` в `.gitignore`).
- [ ] Нужное позже перенесено в документы или в план.

## Definition of done

- [ ] Код изменён только в рамках плана.
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.
- [ ] Решения записаны в `.agents/decisions/`, `_TOC.md` и `FEATURES.md` обновлены.
- [ ] Тестовые сообщения живой проверки удалены, отложенных в «Браин Тест» нет, черновик очищен, `access.json` возвращён в исходное состояние.

## Known facts

Аудит планировщика по исходникам gotd v0.166.0 (2026-11-14, без вызовов Telegram):

- Отложенная отправка: `MessagesSendMessageRequest`, `MessagesSendMediaRequest` и `MessagesForwardMessagesRequest` имеют `ScheduleDate int` и `ScheduleRepeatPeriod int`, `MessagesSendMultiMediaRequest` только `ScheduleDate`; `MessagesEditMessageRequest` тоже имеет `ScheduleDate` (правка отложенного). В `telegram/message` есть `Builder.Schedule(time.Time)` и `Builder.ScheduleTS(int)`; прямой запрос проще, потому что `sendPreparedText` уже собирает `MessagesSendMessageRequest` вручную в двух ветках.
- Ошибки по документации в исходниках: у `sendMessage`, `sendMedia` и `forwardMessages` `SCHEDULE_BOT_NOT_ALLOWED`, `SCHEDULE_DATE_TOO_LATE`, `SCHEDULE_TOO_MUCH`, у `sendMessage` ещё `SCHEDULE_STATUS_PRIVATE`; `SCHEDULE_DATE_INVALID` описан у `editMessage` и есть в `tl_errors_gen.go`.
- Методы отложенных: `MessagesGetScheduledHistoryRequest{Peer, Hash}` и `MessagesGetScheduledMessagesRequest{Peer, ID []int}` возвращают `MessagesMessagesClass`; `MessagesSendScheduledMessagesRequest{Peer, ID []int}` и `MessagesDeleteScheduledMessagesRequest{Peer, ID []int}` возвращают `UpdatesClass`. В gotd есть обёртка `RequestBuilder.Scheduled()` (`Send`, `Delete`, `Get`, `History`).
- Обновления: `UpdateNewScheduledMessage{Message}`, `UpdateDeleteScheduledMessages{Peer, Messages, SentMessages}`, `UpdateDraftMessage{Peer, TopMsgID, SavedPeerID, Draft}`; у `tg.Message` есть `FromScheduled`. `OnAnyNewMessage` в `tg/dispatcher_any.go` берёт только `UpdateNewMessage` и `UpdateNewChannelMessage`; отдельные обработчики `OnNewScheduledMessage`, `OnDeleteScheduledMessages`, `OnDraftMessage` есть, проект их не подключает.
- `writtenMessages` в `internal/tgclient/write.go` ищет id в `UpdateMessageID`, `UpdateNewMessage`, `UpdateNewChannelMessage` и правках, `UpdateNewScheduledMessage` не знает; дату берёт из сообщения или из даты пакета обновлений, то есть для отложенного дала бы время отправки запроса вместо запланированного.
- Ответы RPC не попадают в обработчик обновлений gotd: `telegram/handle_updates.go` обрабатывает только присланные сервером пакеты. Поэтому своё отправленное Daemon сохраняет сам, как в `messageSend` и `messagePoll`.
- Черновик: `MessagesSaveDraftRequest{NoWebpage, InvertMedia, ReplyTo, Peer, Message, Entities, Media, Effect, SuggestedPost, RichMessage}` возвращает `bool`; `Builder.ClearDraft` очищает черновик запросом только с `Peer`. `DraftMessage{NoWebpage, InvertMedia, ReplyTo, Message, Entities, Media, Date, Effect, SuggestedPost, RichMessage}` и `DraftMessageEmpty{Date}`. Отдельного метода «черновик одного чата» нет: черновик лежит в `Dialog.Draft` (`messages.getPeerDialogs`), все черновики отдаёт `MessagesGetAllDrafts` как `UpdatesClass` с `UpdateDraftMessage`. Ошибки `saveDraft`: `CHAT_ADMIN_REQUIRED`, `CHAT_WRITE_FORBIDDEN`, `ENTITY_BOUNDS_INVALID`, `INPUT_USER_DEACTIVATED`, `MSG_ID_INVALID`, `PEER_ID_INVALID`.
- Индикатор: `MessagesSetTypingRequest{Peer, TopMsgID, Action}` возвращает `bool`; типов `SendMessage*Action` 18: `Typing`, `Cancel`, `RecordVideo`, `UploadVideo`, `RecordAudio`, `UploadAudio`, `UploadPhoto`, `UploadDocument`, `GeoLocation`, `ChooseContact`, `GamePlay`, `RecordRound`, `UploadRound`, `HistoryImport`, `ChooseSticker`, `TextDraft`, `RichMessageDraft`, `StopDraft` (у `Upload*` поле `Progress`). Ошибки `setTyping`: среди прочих `CHANNEL_PRIVATE`, `CHAT_ADMIN_REQUIRED`, `CHAT_WRITE_FORBIDDEN`, `USER_IS_BLOCKED`, `USER_BANNED_IN_CHANNEL`, `TEXTDRAFT_PEER_INVALID`. В gotd есть `RequestBuilder.TypingAction()` с `ThreadID`.
- Дроссель: `writeRequest` сегодня содержит отправку, медиа, пересылку, правку, удаление, отметку прочитанного, реакцию и закрепление; `MessagesSaveDraftRequest`, `MessagesSetTypingRequest`, `MessagesSendScheduledMessagesRequest`, `MessagesDeleteScheduledMessagesRequest` в нём нет, то есть без правки их повторил бы автоповтор FLOOD_WAIT до 10 секунд.
- Миграции: в ветке сейчас `0001_init.sql`, `0002_schema.sql`, `0003_events.sql`; `listMigrations` в `internal/store/store.go` требует номера подряд с 1 (`migration versions must start at 1 and be consecutive`), `applyMigrations` применяет всё выше `PRAGMA user_version`.
- Регистрация: методы Daemon в `internal/daemon/daemon.go` строками `d.handle(rpc.Method..., d....)`; интерфейс клиента `Telegram` в `internal/daemon/telegram.go`, его подделка в `internal/daemon/daemon_test.go`; группа `message` собирается в `internal/cli/read_cmd.go` (`cmd.AddCommand(a.messageWriteCommands()...)`), группа `chat` в `internal/cli/chat_cmd.go`.
- Время: `internal/daemon/read.go` `parseTime` разбирает RFC 3339, `message history --since/--until` описаны как RFC 3339.
- Только из документации Telegram, не из исходников и не живьём: отложить можно не дальше 365 дней, не больше 100 отложенных на чат, индикатор показывается около 5 секунд и его нужно повторять.

## Open questions

Каждый вопрос задаётся владельцу отдельно. Стоимость дана оценкой автора плана для исполнителя Codex и не измерена.

| № | Question | Why it blocks | Options и стоимость | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| В1 | Какие операции с отложенными сообщениями делать | Определяет число команд и методов шага 2 | А) только `message send --at`: дёшево, но агент не видит и не отменяет запланированное. Б) А плюс `scheduled list` и `scheduled delete`: плюс 2 метода, около полудня работы. В) Б плюс `scheduled send-now`: плюс 1 метод с сохранением отправленного, ещё около 2 часов. Г) В плюс правка отложенного (`editMessage` со `schedule_date`, перенос времени): плюс правка текста и даты, около полудня, `dangerous` как у `message edit` | В: агент планирует, видит, отменяет и при нужде отправляет сразу; правка дешевле заменяется отменой и новой отправкой | принято |
| В2 | Какие команды получают `--at` | Меняет `send-media`, `poll`, `forward` | А) только `message send`: минимум. Б) плюс `message send-media` и `message poll`: тот же флаг в двух запросах, около 2 часов и тесты на каждом. В) Б плюс `message forward`: ещё около часа | А: 5.6.1 говорит о сообщении; остальные добавляются тем же флагом позже, когда понадобятся | принято |
| В3 | В каком виде задаётся время | Влияет на CLI и навык | А) только `--at` RFC 3339 со смещением: как `--since`, ничего нового. Б) А плюс `--in ДЛИТЕЛЬНОСТЬ` (Go duration, `90m`, `2h`), взаимоисключающие: около часа, агенту не нужно считать время | Б: агенту чаще нужно «через N минут», а ошибка в поясе при ручном счёте вероятна | принято |
| В4 | Хранить ли отложенные сообщения в локальной базе | Миграция или нет | А) не хранить, `scheduled list` всегда спрашивает Telegram: без миграции. Б) отдельная таблица `scheduled_messages` с синхронизацией по ответам и событиям: миграция, подключение `OnNewScheduledMessage` и `OnDeleteScheduledMessages`, около дня | А: список короткий (до 100 на чат), локальный поиск по запланированному не нужен для 5.6, сработавшее сообщение и так сохранится как обычное | принято |
| В5 | Какие операции с черновиками делать | Определяет шаг 3 и строку 10.10.2 | А) `draft get`, `set`, `clear` для одного чата: около полудня. Б) А плюс `draft list` по всем чатам (`getAllDrafts`, закрытые отбрасываются): плюс около 2 часов. В) Б плюс хранение черновиков в базе (10.10.2) и черновики тем форума (`--topic`): миграция и обработчик `OnDraftMessage`, около дня | Б: агент находит начатые владельцем ответы во всех чатах; база и темы отдельной задачей вместе с 10.10 | принято |
| В6 | Форматирование черновика | Меняет `draft set` | А) только простой текст и `--reply-to`: дёшево. Б) плюс `--parse` и `--entities` как у `message send` (`prepareWriteText` уже есть): около 2 часов и тесты; rich-режимы для черновика не проверены сервером | А: черновик это заготовка для владельца, форматирование он доведёт сам; Б добавляется без смены контракта | принято |
| В7 | Помечать ли текст черновика `untrusted` в схеме | Правило пометки чужого текста | А) помечать: текст мог прийти вставкой чужого сообщения, в `--human` текст идёт в рамке недоверенного текста; ничего не стоит. Б) не помечать, как `Note.body` (текст владельца или агента) | А: черновик синхронизируется с других устройств и может содержать чужой текст, лишняя рамка дешевле подсунутой инструкции | принято |
| В8 | Как долго держать индикатор набора | Меняет устройство команды | А) один запрос, индикатор около 5 секунд, агент повторяет сам; `--cancel` снимает: около 2 часов. Б) `--for ДЛИТЕЛЬНОСТЬ` до 60 секунд, Daemon повторяет каждые 5 секунд в фоне и снимает по истечении или при отправке в этот чат: фоновая задача, отмена, тесты со временем, около дня | А: проще и без фоновых задач; Б можно добавить, если агенту станет неудобно | принято |
| В9 | Какие действия доступны в `--action` | Меняет схему и проверку | А) только `typing` и `--cancel`: минимум. Б) `typing`, `upload_photo`, `upload_video`, `upload_document`, `record_voice`, `upload_voice`, `record_video`, `choose_sticker`, `cancel`: таблица имён, около часа. В) все 18 типов gotd: плюс черновиковые и служебные (`TextDraft`, `HistoryImport`), смысл которых для пользовательского аккаунта не проверен | Б: действия, которые агент реально делает перед отправкой медиа; служебные исключены | принято |
