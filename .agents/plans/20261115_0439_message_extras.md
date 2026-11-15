# Пошаговый план: кнопки и inline-боты, правка медиа, стикеры, самоуничтожение и авто-удаление

ID: 20261115_0439_message_extras
Created: 2026-11-15
Owner: Мязин Евгений
Branch: feature/20261115_0439_message_extras
PR: —

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: draft
- Last update: 2026-11-15
- Next safe step: менеджер принимает решения Q1 до Q9 из «Open questions» по одному (владелец делегировал решения менеджеру, принимаются рекомендуемые варианты), записывает их в Decision log; после этого статус `approved`, затем шаг 1 (аудит, Codex Terra в worktree `../tg-cli-message_extras`). Q10 решается перед шагом 8.

## Context and sources

- Read: `AGENTS.md`, `INTENT.md`, `FEATURES.md` (раздел 16 и блок «Статус реализации»), `.agents/rules/plans.md`, план-образец `20261114_2244_users_contacts.md`, решения `20261114_1100_writes_are_off_until_the_owner_lists_the_chat.md`, `20261114_2010_media_goes_by_send_media_from_roots_in_access_json_with_explicit_type.md`, `20261114_2010_reactions_pins_and_polls_are_writes_under_write_without_yes.md`, `.agents/decisions/_TOC.md`.
- Код (ветка от `main` b8330a8): `internal/daemon/telegram.go` (интерфейс `Telegram` со встроенными `usersTelegram`, `ChatAdmin`, `ownerStateTelegram`, `unreadTelegram`), `internal/daemon/write.go` (`writeReference`, `messageEdit`), `internal/daemon/send_media.go` (`validSendMediaParams`, `mediaFileError`, `checkMediaContent`, `messageSendMedia`), `internal/daemon/daemon.go` (`register`, `SetMethodTimeout` для `message.send_media`), `internal/daemon/account_guard.go` (`accountMethod`), `internal/daemon/access_test.go` (`TestRegisteredMethodsHaveAccessClass`), `internal/tgclient/throttle.go` (`classifyRequest`, `writeRequest`), `internal/tgclient/readonly_test.go` (`readFiles`, `writeCall`), `internal/tgclient/history.go` (`Message`), `internal/tgclient/send_media.go` (`makeMediaSpec`, `uploadedMediaInput`), `internal/tgclient/chat_settings.go`, `internal/tgclient/message.go`, `internal/rpc/api.go` (`Message`, константы методов), `internal/rpc/write.go` (`SendMediaParams`), `internal/rpc/owner_state.go`, `internal/cli/cli.go` (`callTimeout`, `rootCommand`), `internal/cli/read_cmd.go`, `internal/cli/chat_cmd.go`, `internal/cli/docs_test.go`, `internal/cli/docs_gen_test.go`, `docs/cli/messages-write.md`.
- Проверено командами: `go build ./cmd/tg-cli`, `tg schema` (список из 80 команд, флаги `message edit`, `message send-media`, `chat edit`, поля `message get`, `chat info`, `chat state`), `rg` по `ttl|sticker|inline|ReplyMarkup|buttons` в `internal/`, исходники gotd v0.166.0 (`go list -m -f '{{.Dir}}' github.com/gotd/td`), `tg --help` установленной `tg` 0.5.1 (аналогов кнопок, inline-ботов, стикеров и авто-удаления в ней нет).
- Not verified: `just check` и тесты не запускались (это шаг 1). Не проверены на живом аккаунте и по документации TL: срок жизни `query_id` inline-бота, допустимые значения `messages.setHistoryTTL` и `ttl_seconds` у медиа, поведение `messages.editMessage` с медиа без поля `message` (сохраняется ли подпись), можно ли добавить медиа к текстовому сообщению, ответ сервера на нажатие кнопки, когда бот не ответил вовремя.

## Goal

Агент видит inline-кнопки сообщения и нажимает callback-кнопку, запрашивает результаты inline-бота и отправляет выбранный результат, заменяет медиа своего сообщения файлом из разрешённых корней, ищет наборы стикеров, смотрит набор и отправляет из него стикер, отправляет самоуничтожающееся фото или видео в личный чат и включает или выключает авто-удаление сообщений в чате. Каждая запись проходит барьер `write`, умеет `--dry-run` и требует `--yes`, если действие необратимо.

## Что уже есть

Блок «Статус реализации» в `FEATURES.md` устарел; сверка шла по коду и `tg schema` на `main` b8330a8.

| Пункт | Состояние | Где |
| --- | --- | --- |
| 16.4 правка медиа | нет: `message edit` правит только текст (`--text`, `--parse`, `--entities`) | `internal/daemon/write.go` `messageEdit`, `internal/tgclient/write.go` `editText` |
| 16.4 inline-кнопки | нет: `rpc.Message` не содержит разметки, `ReplyMarkup` нигде не читается | `internal/rpc/api.go:251` |
| 16.4 inline-боты | нет | `rg` по `inline` находит только право `send-inline` в `chat permissions` |
| 16.5 стикеры | нет: есть только распознавание типа `sticker` у входящего медиа и право `send-stickers` | `internal/tgclient/message.go:137`, `internal/tgclient/chat_admin.go:209` |
| 16.6 чтение таймера авто-удаления | **уже есть**: `chat info` печатает `settings.ttl_period`, `chat state` печатает `states.ttl_period` | `internal/tgclient/chat_settings.go:45-70`, `internal/rpc/owner_state.go:69` |
| 16.6 установка авто-удаления | нет: `chat edit` меняет только `--title` и `--about` | `internal/cli/chat_admin_cmd.go` |
| 16.6 самоуничтожающееся медиа | нет: `send-media` не передаёт `ttl_seconds` | `internal/tgclient/send_media.go` `makeMediaSpec` |
| Общие механизмы, которые план переиспользует без правки | **уже есть**: барьер записи `writeReference`, корни файлов `fileroot`, проверка содержимого `mediameta` и `checkMediaContent`, идемпотентность `beginIdempotency`, журнал Daemon, дроссель | `internal/daemon/write.go`, `internal/daemon/send_media.go`, `internal/daemon/idempotency.go` |

## Problem and contract

Тип плана: product (новые команды записи и чтения, расширение `send-media`).

Контракт записан для рекомендованных вариантов Q1 до Q9; другой ответ меняет отмеченные места.

### Кнопки (16.4, Q1 до Q4)

- `tg message buttons <chat> <id>` (класс `read`): один запрос сообщения (`GetMessagesWithEntities`, как `message get`), разбор `ReplyInlineMarkup` и `ReplyKeyboardMarkup`. Ответ `{chat_id, message_id, keyboard: inline|reply|none, buttons: [{index, row, column, text, type, url?, copy_text?, user_id?, pressable}]}`. `index` сквозной с 0 по строкам. `type` из набора `callback`, `callback_password`, `url`, `switch_inline`, `copy`, `user_profile`, `game`, `buy`, `url_auth`, `web_view`, `disabled`, `text` (кнопка reply-клавиатуры), `request_phone`, `request_location`, `request_poll`, `request_peer`, `web_view_simple`. `text`, `url`, `copy_text` помечены `untrusted`. Байты `data` callback-кнопки не печатаются. `rpc.Message` не меняется (Q2).
- `tg message press <chat> <id> (--button N | --text ПОДПИСЬ) [--yes] [--dry-run]` (класс `account_write`, `dangerous`, Q3): без `--yes` и без `--dry-run` код 2, Daemon не вызывается. Daemon проверяет `write`, перечитывает сообщение и выбирает кнопку: по индексу или по точному совпадению подписи (ноль или больше одной кнопки с такой подписью дают код 2 со списком индексов). Нажимается только `callback` без пароля (Q4); любой другой тип даёт код 2 с названием типа, `url` и `copy_text` агент берёт из `message buttons`. Запрос `messages.getBotCallbackAnswer`. Ответ `{chat_id, message_id, button: {index, text, type}, answered, alert?, message?, url?, dry_run?}`; `message` и `url` из ответа бота `untrusted`. Ошибка `BOT_RESPONSE_TIMEOUT` не ошибка команды: нажатие доставлено, ответ `answered: false` (шаг 1 подтверждает по документации). Повтора нет.
- `--dry-run` у `press` выполняет все проверки, читает сообщение и возвращает выбранную кнопку без запроса к боту.

### Inline-боты (16.4, Q5)

- `tg message inline-query <chat> --bot @bot --query ТЕКСТ [--offset СТРОКА]` (класс `account_write`, не `dangerous`): чат в `write`, бот виден по спискам доступа (как человек в `user profile`). Запрос `messages.getInlineBotResults` с `peer` целевого чата. Ответ `{chat_id, bot_id, query_id, next_offset?, gallery, results: [{id, type, title?, description?, url?, has_media}]}`; `query_id` строкой (int64 не теряет точность в JSON); все поля результата `untrusted`. Daemon запоминает в памяти `query_id` вместе с чатом, ботом, списком `id` результатов и сроком (`cache_time`, не меньше 60 секунд и не больше 10 минут, точное правило уточняет шаг 1).
- `tg message send-inline <chat> --query-id ID --result ID [--reply-to N] [--topic N] [--silent] [--idempotency-key K] [--dry-run]` (класс `account_write`, не `dangerous`): чат в `write`; `query_id` должен быть выдан этим Daemon для этого же чата и не истечь, `result` должен быть в списке этого запроса, иначе код 2 с подсказкой повторить `inline-query`. Запрос `messages.sendInlineBotResult` (`hide_via` выключен). Ответ как у `message send`: `{chat_id, id, date, dry_run?, replayed?}`. Отправленное сообщение сохраняется в базу из `Updates` ответа (полный текст и тип медиа, если сервер их вернул, иначе только `id` и `date`). `QUERY_ID_INVALID` и `RESULT_ID_INVALID` дают код 2.

### Правка медиа (16.4)

- `tg message edit-media <chat> <id> --as photo|video|audio|document --file ПУТЬ [--caption ТЕКСТ] [--duration] [--width] [--height] [--yes] [--dry-run]` (класс `account_write`, `dangerous`, как `message edit`; решение `20261114_1100`): без `--yes` и без `--dry-run` код 2. Файл проходит те же проверки, что у `send-media`: чат в `write`, каталог в `files`, `fileroot.Open`, `checkMediaContent`, предел 100 МиБ, один файл. `voice` не принимается (шаг 1 проверяет, разрешает ли сервер). Перед загрузкой Daemon читает сообщение: нет сообщения код 7, у сообщения нет медиа (текст или служебное) код 2, так 100 МиБ не грузятся зря. `--caption` не задан: подпись не меняется (поле `message` не передаётся, шаг 1 подтверждает); задан пустым: подпись удаляется; предел 1024 символа. Запрос `messages.editMessage` с `media`. Ответ `{chat_id, id, date, action: "edit_media", media_type, size, caption_length?, dry_run?}`. Изменённое сообщение сохраняется в базу (прежняя версия сохраняется механизмом правки). Дедлайн метода 600 секунд, как у `send-media`. `MESSAGE_EDIT_TIME_EXPIRED`, `MESSAGE_AUTHOR_REQUIRED`, `MEDIA_NEW_INVALID` дают код 2 или 1 по таблице шага 1.

### Стикеры (16.5, Q6)

- `tg sticker search <запрос> [--limit N]` (класс `read`): `messages.searchStickerSets` (`hash` 0, рекомендованные не исключаются). Ответ `{sets: [{short_name, title, count, type: regular|masks|emojis, official, animated, video}]}`; `title` `untrusted`; `--limit` по умолчанию 20, от 1 до 100, срез локальный.
- `tg sticker set <имя|t.me/addstickers/имя>` (класс `read`): `messages.getStickerSet` по `short_name`. Ответ `{set: {...как выше}, stickers: [{index, emoji, type: static|animated|video, premium}]}`.
- `tg sticker installed` (класс `read`, Q6): `messages.getAllStickers` (`hash` 0). Ответ `{sets: [...]}` того же вида.
- `tg message send-sticker <chat> --set ИМЯ (--index N | --emoji ЭМОДЗИ) [--reply-to N] [--topic N] [--silent] [--idempotency-key K] [--dry-run]` (класс `account_write`, не `dangerous`): чат в `write`. Daemon перечитывает набор (`file_reference` устаревает, поэтому документ не кэшируется), выбирает стикер по индексу или первый с этим эмодзи, отправляет `messages.sendMedia` с `InputMediaDocument`. Ответ как у `message send`. В базу пишется `media_type: "sticker"`, текст пустой. `--dry-run` читает набор и возвращает выбранный стикер без отправки.
- Наборы стикеров не чаты: списки доступа на них не действуют, `access.json` не меняется. Установка и удаление наборов вне плана.

### Самоуничтожение и авто-удаление (16.6, Q7 до Q9)

- `tg chat auto-delete <chat> --period off|1d|1w|1m|СЕКУНДЫ [--yes] [--dry-run]` (класс `account_write`, `dangerous`, Q7): без `--yes` и без `--dry-run` код 2. Чат в `write`. Запрос `messages.setHistoryTTL`; `off` передаёт 0. Таймер действует на новые сообщения у всех участников, сервер добавляет в чат служебное сообщение. Числовой период проверяется до Telegram: от 1 дня до 1 года (точные границы уточняет шаг 1), иначе код 2; `TTL_PERIOD_INVALID` даёт код 2, `CHAT_ADMIN_REQUIRED` прежним переводом. Ответ `{chat_id, ttl_period, dry_run?}`. Локальная база не меняется: свежий таймер показывают `chat info` и `chat state`.
- `tg message send-media ... --self-destruct СЕКУНДЫ|view-once` (Q8): только `--as photo|video`, один файл, только личный чат с пользователем (группа, канал, альбом и другой тип дают код 2 до Telegram). Секунды от 1 до 60 (шаг 1 уточняет), `view-once` передаёт значение, которое сервер понимает как однократный просмотр. Значение уходит в `ttl_seconds` загруженного медиа. Ответ `send-media` дополняется полем `self_destruct`; в базу пишется как обычное медиа. Класс и `dangerous` у `send-media` не меняются.
- Таймер аккаунта по умолчанию (`messages.setDefaultHistoryTTL`) и самоуничтожение аккаунта (`account.setAccountTTL`) вне плана (Q9).

### Общее

- Барьер записи: все записи идут через `writeReference(..., true)`; чат вне `write` код 9 без запроса к Telegram, закрытый чат `not_found`. `--dry-run` не вызывает методы записи Telegram (чтение сообщения или набора разрешено).
- Повторов нет: `messages.getBotCallbackAnswer`, `messages.sendInlineBotResult`, `messages.setHistoryTTL` добавляются в `writeRequest`; `messages.sendInlineBotResult` добавляется в `classifyRequest` (корзина отправки по `peer`). `messages.editMessage` и `messages.sendMedia` там уже есть. `messages.getInlineBotResults` остаётся чтением дросселя.
- Проверка только чтения: `writeCall` в `readonly_test.go` дополняется явными `MessagesGetBotCallbackAnswer` и `MessagesGetInlineBotResults`, потому что глагол `Get` не ловится; новые файлы чтения `buttons_read.go` и `stickers_read.go` входят в `readFiles`.
- `--expect-user`: методы `message.*` и `chat.*` уже охвачены `accountMethod`; для `sticker.*` добавляется префикс.
- Журнал Daemon пишет метод, длительность и код итога, без подписей кнопок, запросов inline, текстов ответов бота, путей и имён наборов.
- Миграция не нужна: новых таблиц и колонок нет, `query_id` живёт в памяти Daemon.
- Релиз одним слоем; откат: откат ветки, база не меняется.

## Файлы потока

Новые файлы, которыми владеет поток (другие потоки их не трогают):

- `internal/rpc/message_extras.go`, `internal/rpc/message_extras_test.go`: параметры и ответы (`ButtonsParams`, `ButtonsResult`, `Button`, `PressParams`, `PressResult`, `InlineQueryParams`, `InlineQueryResult`, `InlineResult`, `SendInlineParams`, `EditMediaParams`, `EditMediaResult`, `StickerSearchParams`, `StickerSetParams`, `StickerSetInfo`, `StickerSetResult`, `Sticker`, `StickerSetsResult`, `SendStickerParams`, `AutoDeleteParams`, `AutoDeleteResult`), разбор `--period` и `--self-destruct`, разбор ссылки `t.me/addstickers/`.
- `internal/tgclient/buttons_read.go`: разбор разметки сообщения в `[]rpc.Button` (только чтение, в `readFiles`).
- `internal/tgclient/stickers_read.go`: поиск наборов, набор по имени, установленные наборы (только чтение, в `readFiles`).
- `internal/tgclient/message_extras_write.go`: `PressButton`, `InlineQuery`, `SendInline`, `EditMedia`, `SendSticker`, `SetAutoDelete`, перевод ошибок.
- `internal/tgclient/message_extras_test.go`: тесты на подмене invoker.
- `internal/daemon/message_extras.go`, `internal/daemon/message_extras_test.go`, `internal/daemon/message_extras_fake_test.go`: интерфейс `messageExtrasTelegram`, обработчики, память `query_id`, заглушки `fakeTelegram` в своём файле.
- `internal/cli/message_extras_cmd.go`, `internal/cli/message_extras_cmd_test.go`: команды `message buttons|press|inline-query|send-inline|edit-media|send-sticker`, группа `sticker`, `chat auto-delete`, аннотации схемы.
- `docs/cli/buttons-stickers-auto-delete.md`: разделы справочника для каждой новой команды и флага.
- `.agents/decisions/20261115_HHMM_buttons_inline_bots_stickers_and_auto_delete_are_writes_under_write.md` (время в имени ставится в шаге 7).

Общие файлы, каждая правка одной локальной вставкой (новые строки рядом, без переформатирования соседних):

| Файл | Вставка |
| --- | --- |
| `internal/rpc/api.go` | блок констант отдельной группой в конце `const`: `MethodMessageButtons = "message.buttons"`, `MethodMessagePress = "message.press"`, `MethodMessageInlineQuery = "message.inline_query"`, `MethodMessageSendInline = "message.send_inline"`, `MethodMessageEditMedia = "message.edit_media"`, `MethodMessageSendSticker = "message.send_sticker"`, `MethodStickerSearch = "sticker.search"`, `MethodStickerSet = "sticker.set"`, `MethodStickerInstalled = "sticker.installed"`, `MethodChatAutoDelete = "chat.auto_delete"` |
| `internal/rpc/write.go` | поле `SelfDestruct string` в `SendMediaParams` и `SelfDestruct string` (omitempty) в `SendMediaResult` (Q8) |
| `internal/daemon/telegram.go` | одна строка `messageExtrasTelegram` в `Telegram` |
| `internal/daemon/daemon.go` | одна строка `d.registerMessageExtras()` в конце `register()` (таймаут 600 секунд для `message.edit_media` ставится внутри) |
| `internal/daemon/send_media.go` | проверка `SelfDestruct` в `validSendMediaParams` и личного чата после `writeReference` (Q8) |
| `internal/daemon/account_guard.go` | `strings.HasPrefix(method, "sticker.")` в `accountMethod` |
| `internal/daemon/access_test.go` | одна строка в карте `classes` для десяти методов |
| `internal/tgclient/send_media.go` | `TTLSeconds` в `makeMediaSpec` для фото и видео (Q8) |
| `internal/tgclient/throttle.go` | `*tg.MessagesSendInlineBotResultRequest` в `classifyRequest`; `*tg.MessagesGetBotCallbackAnswerRequest, *tg.MessagesSendInlineBotResultRequest, *tg.MessagesSetHistoryTTLRequest` в `writeRequest` |
| `internal/tgclient/readonly_test.go` | `"buttons_read.go", "stickers_read.go"` в конец `readFiles`; явная альтернатива `MessagesGet(BotCallbackAnswer|InlineBotResults)` в `writeCall` и два имени в `TestWriteCallPatternCatchesWriters` |
| `internal/cli/cli.go` | `a.stickerCommand()` в `root.AddCommand`; `rpc.MethodMessageEditMedia` в `callTimeout` рядом с `send-media` |
| `internal/cli/read_cmd.go` | одна строка `cmd.AddCommand(a.messageExtrasCommands()...)` |
| `internal/cli/chat_cmd.go` | одна строка `cmd.AddCommand(a.chatAutoDeleteCommand())` |
| `internal/cli/send_media_cmd.go` | флаг `--self-destruct` (Q8) |
| `docs/cli/messages-write.md` | раздел `--self-destruct` у `send-media` пересобирается `-update-docs`, абзац о самоуничтожении (Q8) |
| `docs/README.md`, `docs/cli/overview.md` | строка-ссылка на новый файл справочника |
| `docs/contracts.md`, `FEATURES.md`, `README.md`, `AGENTS.md`, `docs/skill/tg-cli/SKILL.md`, `.agents/decisions/_TOC.md`, `CHANGELOG.md` | свой абзац или своя строка в шаге 7 |

Изменения общих реестров (полный список):

1. Методы JSON-RPC: десять имён выше, регистрация в `registerMessageExtras`.
2. Интерфейс `daemon.Telegram`: встроенный `messageExtrasTelegram` (`MessageButtons`, `PressButton`, `InlineQuery`, `SendInline`, `EditMedia`, `SearchStickerSets`, `StickerSet`, `InstalledStickerSets`, `SendSticker`, `SetAutoDelete`).
3. Классы доступа в `TestRegisteredMethodsHaveAccessClass`: `message.buttons` `chat-ref`; `message.press`, `message.inline_query`, `message.send_inline`, `message.edit_media`, `message.send_sticker`, `chat.auto_delete` `chat-write`; `sticker.search`, `sticker.set`, `sticker.installed` `none`.
4. Дроссель: три запроса в `writeRequest`, один в `classifyRequest`.
5. `readFiles`: `buttons_read.go`, `stickers_read.go`; `writeCall`: два явных имени.
6. CLI и схема: команды под `message` и `chat`, новая группа `sticker` в корне; `dangerous` у `press`, `edit-media`, `chat auto-delete`.
7. `SendMediaParams.SelfDestruct`, `SendMediaResult.SelfDestruct` (Q8).
8. Хранилище и `access.json`: без изменений, миграций нет.

## Scope

- В плане: `internal/{rpc,tgclient,daemon,cli}`, схема `tg schema`, тесты, справочник `docs/cli/`, документы, живая проверка чтением и, после Q10, записью в «Браин Тест».
- Вне плана: поле кнопок во всех сообщениях (Q2, вариант B); кнопки с паролем, запросом телефона, геопозиции, опроса, чата, игры, оплаты, web view и URL-авторизации; нажатие кнопок reply-клавиатуры (агент отправляет её подпись через `message send`); `ephemeral.getCallbackAnswer`; inline-режим с геопозицией; `switch_pm` и `switch_webview` из ответа inline-бота; установка, удаление, сортировка и архив наборов стикеров, эмодзи-паки, маски, GIF, недавние и избранные стикеры; стикер из чужого сообщения; добавление медиа к текстовому сообщению (если шаг 1 покажет, что сервер разрешает, это отдельная задача); `voice` в `edit-media`; таймер по умолчанию для новых чатов и самоуничтожение аккаунта (Q9); секретные чаты; поля `ttl_period` и `ttl_seconds` в выводе сообщений; обновление `ttl_period` в локальной базе.
- Затрагиваются: файлы из раздела «Файлы потока».

## Main invariant

1. Без видимости чата и без его id в `write` ни один запрос `messages.getBotCallbackAnswer`, `messages.getInlineBotResults`, `messages.sendInlineBotResult`, `messages.editMessage`, `messages.sendMedia`, `messages.setHistoryTTL` в Telegram не уходит; закрытый чат отвечает как неизвестный.
2. `press`, `edit-media` и `chat auto-delete` без `--yes` и без `--dry-run` не вызывают Daemon.
3. `--dry-run` не вызывает методы записи Telegram и не меняет базу.
4. Записи не повторяются ни при FLOOD_WAIT, ни при сетевой ошибке.
5. Файл для `edit-media` открывается только внутри корней `files`, по тем же правилам, что у `send-media`.
6. Существующие команды и их ответы не меняются; `send-media` без `--self-destruct` ведёт себя как раньше.
7. Схема базы не меняется; `internal/cli` не импортирует `tgclient`, gotd, `store`.
8. Ни тест, ни проверка по умолчанию не обращаются к живому Telegram; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: нажатие кнопки запускает у бота необратимое действие (заказ, оплата, удаление, голос). Стратегия: `dangerous` и `--yes`, `--dry-run` показывает выбранную кнопку, нажимаются только callback-кнопки без пароля, выбор по точной подписи с отказом при неоднозначности.
- Риск: сообщение меняется между `message buttons` и `press`, индекс указывает на другую кнопку. Стратегия: Daemon перечитывает сообщение перед нажатием, ответ называет нажатую кнопку, навык советует `--text` вместо `--button`.
- Риск: бот не отвечает вовремя (`BOT_RESPONSE_TIMEOUT`), агент считает нажатие неудачным и жмёт снова. Стратегия: такой ответ даёт `answered: false` с кодом 0, повтора нет, навык это объясняет.
- Риск: inline-запрос раскрывает боту текст запроса, id аккаунта и тип чата. Стратегия: класс `account_write` и барьер `write` (Q5), запрос не пишется в журнал.
- Риск: подделанный или чужой `query_id` в `send-inline`. Стратегия: Daemon принимает только `query_id`, выданный им для этого чата, с известным `result`, и с истёкшим сроком отказывает.
- Риск: `edit-media` теряет подпись или прежнее медиа без возможности вернуть. Стратегия: `dangerous`, проверка наличия медиа до загрузки, шаг 1 проверяет поведение поля `message`, шаг 8 проверяет на живом сообщении в «Браин Тест».
- Риск: авто-удаление стирает будущие сообщения у всех участников, а служебное сообщение видно всем. Стратегия: `dangerous`, `--yes`, `--dry-run`, откат командой `--period off`.
- Риск: вставки в `send_media.go` (три слоя) пересекаются с параллельным потоком медиа (`media_export` правит соседний код скачивания). Стратегия: вставки отдельными строками, `SelfDestruct` только добавляет поле; при конфликте сохраняются обе стороны.
- Риск: слияние с параллельными потоками в общих реестрах (`api.go`, `telegram.go`, `daemon.go`, `access_test.go`, `throttle.go`, `readonly_test.go`, `cli.go`). Стратегия: вставки отдельными строками по списку выше.
- Риск: премиум-стикеры и стикеры в чатах с запретом стикеров дают ошибки сервера. Стратегия: поле `premium` в наборе, таблица ошибок шага 1 (`CHAT_SEND_STICKERS_FORBIDDEN`, `PREMIUM_ACCOUNT_REQUIRED`).

## Decision log

| Date | Decision | Reason | Confirmed by |
| --- | --- | --- | --- |
| 2026-11-15 | Миграция не нужна; `query_id` inline-бота хранится в памяти Daemon | Новых данных в базе нет; после перезапуска агент повторяет `inline-query` | предложено планировщиком |
| 2026-11-15 | `edit-media` это `dangerous` с `--yes`, как `message edit` | Решение `20261114_1100`: правка необратима; прежнее медиа не возвращается | предложено планировщиком |
| 2026-11-15 | `edit-media` отдельная команда, а не флаги `message edit` | Решение `20261114_2010` о медиа: у файла свои корни, предел и дедлайн; общий `messageEdit` других потоков не трогается | предложено планировщиком |
| 2026-11-15 | `edit-media` читает сообщение до загрузки файла | Без медиа в сообщении сервер откажет уже после загрузки до 100 МиБ | предложено планировщиком |
| 2026-11-15 | Стикер выбирается по `--index` или `--emoji` при каждой отправке из свежего набора | `file_reference` документа устаревает; кэш дал бы отказы сервера | предложено планировщиком |
| 2026-11-15 | `send-sticker` и `send-inline` принимают `--idempotency-key`, как `send` и `send-media` | Единое правило отправок: повтор вызова не даёт второго сообщения | предложено планировщиком |
| 2026-11-15 | Байты `data` callback-кнопки не печатаются | В них бывают токены бота; агенту нужен только индекс или подпись | предложено планировщиком |
| 2026-11-15 | `BOT_RESPONSE_TIMEOUT` у `press` даёт успех с `answered: false` | Нажатие доставлено боту; ошибка толкнула бы агента нажать второй раз | предложено планировщиком, шаг 1 подтверждает |
| 2026-11-15 | Наборы стикеров не проходят списки доступа | Набор не чат и не человек; `access.json` их не описывает | предложено планировщиком |
| 2026-11-15 | Q2. Как агент видит кнопки: вариант A | A. Цена: агент не знает о кнопках, пока не спросит; лишний запрос на сообщение. B меняет ответ пяти команд и общий тип других потоков | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q3. Помечать ли нажатие кнопки `dangerous` с `--yes`: вариант A | A. Цена: агент добавляет `--yes` к каждому нажатию; меню из десяти шагов требует десяти подтверждений в строке вызова | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q4. Какие кнопки можно нажать: вариант A | A. Цена: игры, оплаты, web app и вход через бота агенту недоступны | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q5. Как устроены inline-боты и их барьер: вариант A | A. Цена: два вызова на отправку; после перезапуска Daemon запрос повторяется. C отправляет вслепую | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q6. Включать ли список установленных наборов `sticker installed`: вариант A | A. Цена: ещё одна команда и раздел справочника; без неё агент не знает привычные стикеры владельца | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q7. Требует ли авто-удаление чата `--yes`: вариант A | A. Цена: выключение тоже требует `--yes` (схема хранит `dangerous` на команду, не на значение) | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q9. Нужен ли таймер по умолчанию для новых чатов (`messages.setDefaultHistoryTTL`): вариант A | A. Цена: агент не видит и не меняет таймер по умолчанию; владелец меняет его в приложении | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q10. На чём проверять запись вживую (шаг 8): вариант A | A. Задать перед шагом 8. Цена B: нажатие и inline не проверены на живом аккаунте | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q1. Как назвать команды: вариант A | A. Цена: новая группа `sticker` в корне и шесть подкоманд у `message`; `bot` в B путает с Bot API, который не цель (INTENT) | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q8. Как сделать самоуничтожающиеся сообщения: вариант A | A. Цена: вставки в три чужих файла `send-media`; текст самоуничтожаться не может (только через авто-удаление чата) | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |

## Step 0. Fix decisions before changes

- [ ] Решения Q1 до Q9 записаны в Decision log, контракт поправлен под ответы.
- [ ] Исполнитель Go-кода (Codex Terra) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [x] Ветка `feature/20261115_0439_message_extras` создана от `main` (b8330a8) в worktree `../tg-cli-message_extras`.

## Step 1. Audit and baseline (исполнитель Codex Terra)

Отчёт в `var/agents/plans/20261115_0439_message_extras/audit.md` (в git не входит), итог в «Known facts».

- [ ] `just check` и `just build` на чистом дереве ветки; результат в Known facts.
- [ ] gotd: точные типы разметки (`ReplyInlineMarkup.Rows []KeyboardInlineButtonRow`, `InlineButtonType*`, `ReplyKeyboardMarkup` и `ButtonType*`) и полный перечень типов кнопок слоя v0.166.0; таблица «тип TL → `type` в ответе → можно ли нажать».
- [ ] `messages.getBotCallbackAnswer`: ответ `MessagesBotCallbackAnswer` (`Alert`, `HasURL`, `NativeUI`, `Message`, `URL`, `CacheTime`), ошибки `BOT_RESPONSE_TIMEOUT`, `DATA_INVALID`, `MESSAGE_ID_INVALID`, `PEER_ID_INVALID`; подтвердить по документации TL, что при `BOT_RESPONSE_TIMEOUT` нажатие доставлено.
- [ ] `messages.getInlineBotResults` и `messages.sendInlineBotResult`: поля `MessagesBotResults` (`QueryID`, `NextOffset`, `Gallery`, `CacheTime`, `Results`), виды `BotInlineResult` и `BotInlineMediaResult`, срок годности `query_id`, ошибки `BOT_INLINE_DISABLED`, `QUERY_ID_INVALID`, `RESULT_ID_INVALID`, `BOT_RESPONSE_TIMEOUT`; правило срока в памяти Daemon.
- [ ] `messages.editMessage` с медиа: сохраняется ли подпись без поля `message`; можно ли менять тип (фото на документ, видео в альбоме); разрешён ли `voice`; можно ли добавить медиа к текстовому сообщению; ошибки `MESSAGE_EDIT_TIME_EXPIRED`, `MESSAGE_NOT_MODIFIED`, `MEDIA_NEW_INVALID`, `MEDIA_PREV_INVALID`, `MESSAGE_AUTHOR_REQUIRED`. Можно ли переиспользовать `makeMediaSpec` и `uploadedMediaInput` из `send_media.go` без правки файла; если нет, назвать минимальную вставку.
- [ ] Стикеры: `messages.searchStickerSets` (`MessagesFoundStickerSets`, `StickerSetCovered*`), `messages.getStickerSet` (`MessagesStickerSet`, `Packs`, `Documents`, `DocumentAttributeSticker`), `messages.getAllStickers`; как определить `static|animated|video` и `premium`; ошибки `STICKERSET_INVALID`, `CHAT_SEND_STICKERS_FORBIDDEN`, `PREMIUM_ACCOUNT_REQUIRED`.
- [ ] `messages.setHistoryTTL`: допустимые значения периода, служебное сообщение в ответе, ошибки `TTL_PERIOD_INVALID`, `CHAT_ADMIN_REQUIRED`; поведение в личном чате, группе, канале.
- [ ] `ttl_seconds` у `InputMediaUploadedPhoto` и `InputMediaUploadedDocument`: допустимые значения, значение однократного просмотра, ограничение личными чатами, ошибки сервера.
- [ ] Где `mapError` переводит перечисленные ошибки в коды выхода; какие новые строки нужны.
- [ ] Подтвердить, что `writeReference` и `beginIdempotency` подходят новым обработчикам без правки.
- [ ] Записать итоги в Known facts, непокрытое в Risks и Open questions.

## Implementation steps

Каждый шаг кода выполняет Codex Terra, проверяет Claude (тесты, дифф, `just check`), коммитит Claude.

- [ ] 2. `rpc`: типы и проверки в `message_extras.go`, константы в `api.go`, поле `SelfDestruct` в `write.go`. Тесты: разбор `--period` (`off`, `1d`, `1w`, `1m`, секунды, границы, мусор), `--self-destruct` (секунды, `view-once`, границы), ссылка `t.me/addstickers/`, ровно один из `--button`/`--text` и `--index`/`--emoji`, пределы `--limit`, `query_id` строкой.
- [ ] 3. `tgclient`, чтение: `buttons_read.go` (разбор inline и reply разметки, сквозной индекс, `pressable`), `stickers_read.go` (поиск, набор, установленные); обе в `readFiles`, две альтернативы в `writeCall`. Тесты на подмене invoker: таблица типов кнопок, `data` не попадает в ответ, типы стикеров и `premium`, срез `--limit`.
- [ ] 4. `tgclient`, запись: `message_extras_write.go` (`PressButton` с выбором кнопки и отказом на непригодный тип, `InlineQuery`, `SendInline`, `EditMedia` через загрузку как у `send-media`, `SendSticker`, `SetAutoDelete`), `TTLSeconds` в `makeMediaSpec`, вставки в `throttle.go`. Тесты: параметры каждого запроса, `BOT_RESPONSE_TIMEOUT` даёт `answered: false`, `hide_via` выключен, FLOOD_WAIT записи без повтора, `ttl_seconds` только при `SelfDestruct`.
- [ ] 5. Daemon: `messageExtrasTelegram` и строка в `Telegram`; обработчики в `message_extras.go` (барьер `write`, `--dry-run`, проверка медиа до загрузки, корни `files`, память `query_id` со сроком и привязкой к чату, сохранение отправленного и изменённого в базу, идемпотентность); `registerMessageExtras` с таймаутом 600 секунд для `message.edit_media`; проверка `SelfDestruct` в `send_media.go`; префикс `sticker.` в `account_guard.go`; строка в `access_test.go`; заглушки в `message_extras_fake_test.go`. Тесты без сокетов: чат вне `write` код 9 и ноль вызовов записи у всех семи записей, закрытый чат `not_found`, `--dry-run` без вызова записи, чужой и истёкший `query_id` код 2, сообщение без медиа код 2 и ноль загрузок, файл вне корней, `--self-destruct` в группе и в альбоме код 2.
- [ ] 6. CLI и справочник: `message_extras_cmd.go` (флаги, `--yes` у трёх `dangerous`, взаимоисключающие флаги, аннотации `read`/`account_write`/`dangerous`, `ItemPath` для списков), три строки в `cli.go`, `read_cmd.go`, `chat_cmd.go`, `callTimeout`, флаг `--self-destruct` в `send_media_cmd.go`; файл `docs/cli/buttons-stickers-auto-delete.md` с разделом на каждую команду, ссылки в `docs/README.md` и `overview.md`; таблицы флагов `go test ./internal/cli -run TestDocsGenerated -update-docs`. Тесты по образцу `send_media_cmd_test.go`, тест полноты справочника проходит.
- [ ] 7. Документы через `docwriter` и `glavred` по `docs_chain.md`: `docs/contracts.md` (абзац про кнопки, inline-ботов, правку медиа, стикеры, авто-удаление), тексты разделов справочника из шага 6, `FEATURES.md` (подпункты 16.4.x, 16.5.x, 16.6.x, строка статуса, «не проверено»), `README.md` (только проверенное), `docs/skill/tg-cli/SKILL.md` (порядок `buttons` → `press --dry-run` → `press --yes`, `answered: false` без повтора, двухшаговый inline, откат авто-удаления), `AGENTS.md` (одна строка в Running при необходимости), решение в `.agents/decisions/` и строка в `_TOC.md`, `CHANGELOG.md` (создаётся первой записью).
- [ ] 8. Живая проверка Claude. Чтение: `message buttons` на сообщении с кнопками в разрешённом для чтения чате, `sticker search`, `sticker set`, `sticker installed`, `chat info` показывает `ttl_period`. Запись только по Q10 и только в «Браин Тест» с временным `access.json` (затем удалить): `--dry-run` всех записей, чат вне `write` код 9; `inline-query` и `send-inline` через публичного inline-бота; `send-sticker`; `send-media --self-destruct` (только если «Браин Тест» личный чат, иначе проверка отказа кода 2); `edit-media` своего тестового сообщения; `chat auto-delete --period 1d --yes`, затем `--period off --yes`; `press` по цели из Q10. Тестовые сообщения удаляются, таймер выключается. В отчёт идут числа и коды, без текстов чужих сообщений.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/rpc/... ./internal/tgclient/... ./internal/daemon/... ./internal/cli/...` | зелёный | not_run |
| Без `write` запроса нет | тесты `internal/daemon` | счётчик вызовов записи подмены равен нулю, код 9 | not_run |
| `--yes` у трёх `dangerous` | тесты `internal/cli` | без `--yes` и `--dry-run` код 2, Daemon не вызван | not_run |
| Нет повтора записи на FLOOD_WAIT | тесты `internal/tgclient` | один вызов | not_run |
| Только чтение | `go test ./internal/tgclient -run 'TestReadFilesCallNothingThatWrites|TestWriteCallPatternCatchesWriters'` | `buttons_read.go`, `stickers_read.go` без записи; два новых имени распознаны | not_run |
| Полнота схемы и классов | `go test ./internal/cli ./internal/daemon -run 'TestDocs|TestRegisteredMethodsHaveAccessClass'` | у десяти методов класс, у каждой команды и флага раздел | not_run |
| Таблицы справочника | `go test ./internal/cli -run TestDocsGenerated` | без `-update-docs` зелёный | not_run |
| Миграций нет | `git diff --stat main -- internal/store/migrations` | пусто | not_run |
| `send-media` прежний | тесты `internal/daemon`, `internal/tgclient` для `send-media` | без `--self-destruct` запрос и ответ как до плана | not_run |
| Живая проверка | шаг 8 | числа и коды по каждой команде | not_run |

## Deploy / rollback

- [ ] Выкладки нет. Откат: откат ветки; база и `access.json` не меняются.
- [ ] После слияния перезапустить Daemon (`tg daemon stop`, `tg daemon start`), чтобы появились новые методы.

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261115_0439_message_extras/`.
- [ ] Временные файлы не в git (`/var/` в `.gitignore`).
- [ ] Временный `access.json` живой проверки удалён, тестовые сообщения удалены, таймер авто-удаления в «Браин Тест» выключен.

## Definition of done

- [ ] Код изменён только в рамках плана и раздела «Файлы потока».
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.
- [ ] Решение записано в `.agents/decisions/`, `_TOC.md`, `FEATURES.md`, справочник и навык обновлены.

## Known facts

- gotd v0.166.0. Есть все нужные запросы: `MessagesGetBotCallbackAnswerRequest{Game, Peer, MsgID, Data, Password}`, `MessagesGetInlineBotResultsRequest{Bot, Peer, GeoPoint, Query, Offset}`, `MessagesSendInlineBotResultRequest{Silent, Background, ClearDraft, HideVia, Peer, ReplyTo, RandomID, QueryID, ID, ScheduleDate, SendAs, ...}`, `MessagesEditMessageRequest{..., Message, Media, ...}`, `MessagesSetHistoryTTLRequest{Peer, Period}`, `MessagesSearchStickerSetsRequest{ExcludeFeatured, Q, Hash}`, `MessagesGetStickerSetRequest{Stickerset, Hash}`, `messages.getAllStickers`.
- В слое v0.166.0 inline-кнопки устроены иначе, чем в старых версиях: `ReplyInlineMarkup{Rows []KeyboardInlineButtonRow}`, тип кнопки в отдельном классе `InlineButtonType*` (`Callback{RequiresPassword, Data}`, `URL`, `SwitchInline`, `Copy`, `Game`, `Buy`, `URLAuth`, `UserProfile`, `WebView`, `Disabled`); у reply-клавиатуры `KeyboardButton{Style, Text, Type ButtonTypeClass}` с `ButtonType*` (`Default`, `RequestPhone`, `RequestGeoLocation`, `RequestPoll`, `RequestPeer`, `SimpleWebView`). Перечень сверяет шаг 1.
- `MessagesBotCallbackAnswer{Alert, HasURL, NativeUI, Message, URL, CacheTime}`; `MessagesBotResults{Gallery, QueryID int64, NextOffset, SwitchPm, SwitchWebview, Results, CacheTime, Users}`.
- `InputMediaUploadedPhoto` и `InputMediaUploadedDocument` имеют `TTLSeconds`; `InputMediaDocument{Spoiler, ID, VideoCover, VideoTimestamp, TTLSeconds, Query}` подходит для отправки стикера.
- `chat info` печатает `settings.ttl_period` (`chat_settings.go:45-70`), `chat state` печатает `states.ttl_period` (`rpc/owner_state.go:69`): чтение таймера уже есть.
- `rpc.Message` (`api.go:251`) содержит `id, chat_id, date, sender, text, out, reply_to, topic_id, media_type, edited, pinned, service`; разметки нет. Поле кнопок в нём изменило бы ответ `history`, `get`, `watch`, `export`, `search` и общий тип других потоков.
- `writeCall` в `readonly_test.go` ловит глаголы `Send|Edit|Set|...`, но не `Get`: `MessagesGetBotCallbackAnswer` и `MessagesGetInlineBotResults` сегодня не считались бы записью. `SetHistoryTTL` и `SendInlineBotResult` ловятся.
- `writeRequest` уже содержит `MessagesEditMessageRequest`, `MessagesSendMediaRequest`; не содержит `MessagesGetBotCallbackAnswerRequest`, `MessagesSendInlineBotResultRequest`, `MessagesSetHistoryTTLRequest`.
- `accountMethod` охватывает префиксы `chat.`, `message.`, `note.`, `history_sync.`; `sticker.` нет.
- Таймаут 600 секунд задан только для `message.send_media`: `daemon.go:330` (`SetMethodTimeout`) и `cli.go:24` (`callTimeout`).
- Миграции `0001` до `0006` заняты; поток миграцию не берёт.
- `send-media`, `send`, `forward`, `poll` принимают `--idempotency-key`; `react`, `pin`, `edit` нет.
- В установленной `tg` 0.5.1 нет команд для кнопок, inline-ботов, стикеров и авто-удаления: имена выбираются без оглядки на неё.

## Open questions

Каждое решение принимается отдельно. Рекомендуемый вариант первый. Решения принимает менеджер (делегировано владельцем).

| # | Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| Q1 | Как назвать команды | Имена попадают в схему, справочник и навык | A: `message buttons\|press\|inline-query\|send-inline\|edit-media\|send-sticker`, группа `sticker search\|set\|installed`, `chat auto-delete`; B: отдельная группа `bot` для кнопок и inline (`bot press`, `bot inline`), остальное как в A; C: всё под `message` (`message sticker-search`, `message auto-delete`) | A. Цена: новая группа `sticker` в корне и шесть подкоманд у `message`; `bot` в B путает с Bot API, который не цель (INTENT) | принято |
| Q2 | Как агент видит кнопки | Определяет, трогает ли поток общий тип `rpc.Message` | A: отдельная команда `message buttons <chat> <id>`; B: поле `buttons` в каждом сообщении (`history`, `get`, `watch`, `export`) | A. Цена: агент не знает о кнопках, пока не спросит; лишний запрос на сообщение. B меняет ответ пяти команд и общий тип других потоков | принято |
| Q3 | Помечать ли нажатие кнопки `dangerous` с `--yes` | Последствия нажатия определяет чужой бот | A: `dangerous`, `--yes`, `--dry-run` показывает кнопку; B: `account_write` без `--yes`, как реакции | A. Цена: агент добавляет `--yes` к каждому нажатию; меню из десяти шагов требует десяти подтверждений в строке вызова | принято |
| Q4 | Какие кнопки можно нажать | Часть кнопок отдаёт боту телефон, геопозицию или платёж | A: только callback без пароля; `url`, `copy`, `user_profile` показываются как данные, остальные дают код 2; B: плюс `switch_inline` как переход к `inline-query`; C: плюс `url_auth` и `web_view` | A. Цена: игры, оплаты, web app и вход через бота агенту недоступны | принято |
| Q5 | Как устроены inline-боты и их барьер | Запрос уже передаёт боту текст и id аккаунта | A: две команды; `inline-query` класса `account_write` с чатом в `write`, `send-inline` принимает только `query_id`, выданный этим Daemon для этого чата; B: `inline-query` как `read` без `write`; C: одна команда сразу отправляет N-й результат | A. Цена: два вызова на отправку; после перезапуска Daemon запрос повторяется. C отправляет вслепую | принято |
| Q6 | Включать ли список установленных наборов `sticker installed` | Объём 16.5 | A: да, один запрос `messages.getAllStickers`; B: только поиск и набор по имени | A. Цена: ещё одна команда и раздел справочника; без неё агент не знает привычные стикеры владельца | принято |
| Q7 | Требует ли авто-удаление чата `--yes` | Таймер стирает будущие сообщения у всех участников | A: `dangerous`, `--yes` для любого значения, включая `off`; B: `account_write` без `--yes`, откат через `off` | A. Цена: выключение тоже требует `--yes` (схема хранит `dangerous` на команду, не на значение) | принято |
| Q8 | Как сделать самоуничтожающиеся сообщения | В обычных чатах Telegram даёт таймер только фото и видео в личных чатах | A: флаг `--self-destruct СЕКУНДЫ\|view-once` у `send-media` (фото и видео, один файл, личный чат); B: отдельная команда; C: не делать, 16.6 закрывается авто-удалением чата | A. Цена: вставки в три чужих файла `send-media`; текст самоуничтожаться не может (только через авто-удаление чата) | принято |
| Q9 | Нужен ли таймер по умолчанию для новых чатов (`messages.setDefaultHistoryTTL`) | Настройка аккаунта, к ней не подходит барьер `write` по чату | A: вне плана; B: только чтение `account auto-delete`; C: чтение и запись под новым ключом `access.json` | A. Цена: агент не видит и не меняет таймер по умолчанию; владелец меняет его в приложении | принято |
| Q10 | На чём проверять запись вживую (шаг 8) | Нажатие и inline-запрос общаются с реальными ботами; без явной цели записи нет | A: «Браин Тест» во временном `write`; inline через публичного бота (например `@gif`); нажатие кнопки на сообщении бота, которое назовёт владелец, этот чат тоже временно в `write`; B: только `--dry-run` и код 9 для `press` и inline, остальное в «Браин Тест» | A. Задать перед шагом 8. Цена B: нажатие и inline не проверены на живом аккаунте | принято |
