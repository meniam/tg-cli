# Пошаговый план: непрочитанное для агента и хранение настроек и истории чатов

ID: 20261115_0241_read_unread_chats
Created: 2026-11-15
Owner: Мязин Евгений
Branch: feature/20261115_0241_read_unread_chats (создана заранее в worktree `../tg-cli-read_unread_chats` от `main` ea157b2, код не менялся)
PR: —

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: draft
- Last update: 2026-11-15
- Next safe step: менеджер принимает решения В1 до В8 (владелец делегировал их менеджеру), записывает их в Decision log; затем Codex Terra начинает шаг 1 (аудит).

## Context and sources

- Read: `AGENTS.md`, `INTENT.md`, `FEATURES.md` (4.6, 4.7, 4.9, 7.4, 10.1, 10.5, 10.10, 10.12, сценарии 4.6 и 10.5, статус строки 661 и 664), `.agents/rules/plans.md`, `.agents/rules/project/AGENTS.md`, план-образец и план соседнего потока `../tg-cli-folders_account_state/.agents/plans/20261114_2244_folders_account_state.md` (ветка `feature/20261114_2244_folders_account_state`, коммиты `0a648ca`, `8ea84e1`, `5dda001`).
- Код `main` ea157b2: `internal/store/migrations/0002_schema.sql` (`chats`, `message_versions`), `internal/store/telegram.go` (`Chat`, `mergeChat`, `UpsertChats`), `internal/daemon/persist.go` (`saveChats`, `saveChatInfo`), `internal/daemon/daemon.go` (`register`, `chatList`), `internal/daemon/read.go` (`chatInfo`), `internal/tgclient/client.go` (`chats`), `internal/tgclient/convert.go` (`toChat`), `internal/tgclient/history.go` (`History`), `internal/tgclient/peers.go` (`ChatInfo`), `internal/tgclient/topics_common.go` (`Topics`), `internal/tgclient/chat_admin.go` (`bannedRights`), `internal/rpc/api.go` (`Chat`, `ChatInfo`, `HistoryParams`, `HistoryQuery`), `internal/rpc/chat_lists.go` (`Topic`), `internal/cli/chat_cmd.go`, `internal/cli/read_cmd.go`.
- Код ветки папок: `internal/rpc/owner_state.go` (`ChatState`, `ChatStatesResult`), `internal/tgclient/owner_state.go` (`dialogState`, `peerDialogsAPI`, `ChatStates`), `internal/store/owner_state.go` (`UpsertDialogStates`, `DialogState`), `internal/store/migrations/0006_folders_account_state.sql` (`dialog_state`).
- gotd v0.166.0: `telegram/query/messages/queries.gen.go` (`GetHistoryQueryBuilder.MinID`, `GetRepliesQueryBuilder.MinID`), `tg/tl_forum_topic_gen.go` (`ForumTopic.ReadInboxMaxID`, `UnreadCount`, `UnreadMentionsCount`, `UnreadReactionsCount`), `tg/tl_messages_get_forum_topics_by_id_gen.go`, `tg/tl_messages_get_dialogs_gen.go` (`FolderID`).
- Verified by commands: `tg schema` из сборки `main` (список команд и флагов, см. Known facts); `rg 'UpsertChats|saveChats\(|saveChatInfo\(' internal/daemon` (все записи чатов идут через `store.UpsertChats`); `rg 'Settings' internal/store internal/daemon` (столбец `chats.settings` никто не пишет); `git worktree list`.
- Not verified: какой `read_inbox_max_id` у диалога форума и включает ли `getDialogs` без `folder_id` архив; возвращает ли `getHistory` с `min_id` исходящие сообщения после маркера; сколько диалогов и непрочитанных у владельца. Это проверяет шаг 1 по исходникам gotd и документации MTProto, шаг 8 вживую.

## Goal

Агент одной командой видит чаты с непрочитанным и одной командой читает непрочитанные сообщения чата или темы форума, ничего не помечая прочитанным. Daemon хранит настройки чата и историю изменений его названия, username, описания, типа и настроек, а агент читает эту историю из базы.

## Problem and contract

Тип плана: product (новые команды чтения, новый столбец в ответе `chat info`, новая таблица).

FEATURES 4.6 и 10.5 записаны как `[Must]` и сделаны частично. Агент видит `unread` в `chat list` и в `chat topics`, но чтобы найти чаты с непрочитанным, листает все диалоги, а непрочитанные сообщения получить не может: нет маркера прочитанного в ответах и нет нижней границы в истории. Столбец `chats.settings` создан и пуст, истории изменений чата нет.

### Что уже есть (не планируется)

| Пункт | Что сделано | Код |
| --- | --- | --- |
| 4.6.1 частично | `chat list` выдаёт `unread` каждого диалога и пишет его в `chats.unread` | `internal/tgclient/convert.go:20`, `internal/daemon/daemon.go:449`, `internal/daemon/persist.go:47` |
| 4.6.2 частично | `chat topics` выдаёт `unread` каждой темы форума | `internal/tgclient/topics_common.go:76`, `internal/rpc/chat_lists.go:38` |
| 10.5.1 | тип, название, username пишут `chat list`, `chat resolve`, `chat common`, `chat create`, `chat join`; описание, число участников, `forum`, `verified` пишет `chat info`; слияние не затирает известное пустым | `internal/daemon/persist.go:39`, `internal/daemon/persist.go:96`, `internal/tgclient/peers.go:166`, `internal/store/telegram.go:262` |
| 10.5.2 состояние | маркеры прочитанного, счётчики, `unread_mark`, закрепление, архив, мьют, TTL диалога: `tg chat state` и таблица `dialog_state` | поток папок, ветка `feature/20261114_2244_folders_account_state`, `internal/rpc/owner_state.go`, `internal/store/owner_state.go`; в `main` ещё нет |
| 4.7 | `chat mark-read` | `internal/daemon/discovery.go`, план `20261114_1140_message_search_context_links` |

### Что делает этот план

Контракт записан с рекомендованными ответами на В1 до В8; другой ответ меняет отмеченный пункт.

- `tg chat unread [--limit N] [--archived]` (метод `chat.unread`, класс `read`, В1): Daemon листает диалоги (`messages.getDialogs`, пачки по 100, через общий дроссель как чтение) и возвращает те, у которых `unread > 0` или `unread_mark`. Поля элемента: `id`, `type`, `title`, `username`, `forum`, `unread`, `unread_mentions`, `unread_reactions`, `unread_mark`, `muted` (истинно, если `mute_until` в будущем), `archived`, `read_inbox_max_id`, `top_message`. Ответ: `{chats, scanned, complete}`, где `scanned` число просмотренных диалогов, `complete` истинно, если список диалогов кончился. `--limit` от 1 до 500, по умолчанию 100; просмотр останавливается на `--limit` найденных или после 1000 диалогов. Без `--archived` смотрится основной список, с ним только архив (`folder_id = 1`). Порядок как у Telegram (по последнему сообщению). Курсора нет.
- `tg message unread <чат> [--topic N] [--limit N] [--cursor C]` (метод `message.unread`, класс `read`, В2, В3): непрочитанные входящие сообщения. Маркер берётся из `messages.getPeerDialogs` для чата или из `messages.getForumTopicsByID` для темы; сообщения из `messages.getHistory` или `messages.getReplies` с `min_id` равным маркеру. Ответ: `{chat_id, topic, read_inbox_max_id, unread, messages, next_cursor}`; сообщения в формате `message history`, от новых к старым, курсор тот же, что у истории, и каждая страница заново читает маркер. Исходящие сообщения после маркера отбрасываются. Лимит по умолчанию 20, наибольший 100. Форум без `--topic` даёт код 2 с подсказкой про `chat topics` (В2), `--topic` у чата без форума код 2, неизвестная тема код 7.
- Ни одна из двух команд не помечает прочитанным: не вызываются `messages.readHistory`, `channels.readHistory`, `messages.readMentions`, `messages.readReactions`, `messages.readDiscussion`, `messages.getMessagesViews` с `increment`. Тест проверяет, что подмена Telegram получила только эти чтения: `getDialogs`, `getPeerDialogs`, `getForumTopicsByID`, `getHistory`, `getReplies`.
- Запись в базу попутно (как у истории, 10.12.1): `chat unread` пишет чаты (`saveChats` с `unread`) и строки `dialog_state` через `store.UpsertDialogStates` потока папок; `message unread` пишет сообщения через `saveMessages` и одну строку `dialog_state` для чата без темы. Сбой записи не ломает ответ (`d.save`).
- `chat info` (В4): ответ получает объект `settings`, Daemon пишет его в `chats.settings`. Состав для группы, супергруппы и канала: `default_denied` (имена прав как у `chat permissions --deny`), `slow_mode_seconds`, `no_forwards`, `join_request`, `join_to_send`, `signatures`, `hidden_history`, `ttl_period`, `linked_chat_id`, `reactions` (`all`, `some` со списком эмодзи, `none`); для личного чата и бота `ttl_period`. Поле без значения у Telegram в объект не попадает; точный список подтверждает шаг 1. Новых запросов нет: данные уже приходят в `FullRaw`.
- История изменений чата (В5): миграция 0008 создаёт `chat_versions` и триггер `AFTER UPDATE ON chats`. Когда меняется `type`, `title`, `username`, `about` или `settings`, триггер сохраняет прежние значения строкой версии с `observed_at` (время, когда Daemon увидел новое значение, а не время правки в Telegram). Появление значения, которого раньше не было (`NULL` или пустое название), версией не считается. `members` и `unread` версию не создают. Триггер ловит все пути записи, потому что все они идут через `UpsertChats`.
- `tg chat changes <чат> [--limit N]` (метод `chat.changes`, класс `read`, В5): только из базы, без Telegram и без входа (Daemon нужен), как `search` и `note`. Ответ: `{chat_id, current, versions}`, где `current` текущие поля из `chats`, `versions` прежние состояния от новых к старым с `version` и `observed_at`. `--limit` от 1 до 100, по умолчанию 20. Чат разрешается по базе (id, `@username`); неизвестный чат даёт `not_found`.
- Списки доступа (решение 6, FEATURES 4.9): закрытый чат в `message unread`, `chat changes` даёт `not_found` до обращения к Telegram и к базе; в `chat unread` закрытые чаты отбрасываются до ответа и до записи в базу; число отброшенных не выводится и в `scanned` не отличается. `chat info` уже проверяет доступ.
- Недоверенный текст: `title`, `username` элементов `chat unread`, текст сообщений `message unread` (как у истории), `title`, `username`, `about` в `current` и `versions` у `chat changes` помечены `untrusted` в `tg schema`. Поля `settings` не текст и пометки не получают, кроме списка эмодзи реакций (не помечается, это значения из фиксированного набора Telegram; подтверждает шаг 1).
- Журнал Daemon без параметров и данных ответа (действует `Server.call`).
- Коды выхода: без входа код 4 (кроме `chat changes`), закрытый или неизвестный чат и неизвестная тема 7, неверные флаги и форум без `--topic` 2, FLOOD_WAIT дольше порога 6.
- `--local` у `chat unread` и `message unread` нет (В8).

## Scope

- В плане: миграция 0008, хранилище (`chat_versions`, чтение версий), `rpc` (методы и типы), `tgclient` (`UnreadChats`, `UnreadMessages`, сбор `settings`), Daemon (обработчики, фильтр доступа, сохранение), CLI (`chat unread`, `message unread`, `chat changes`, поле `settings` у `chat info`), схема, справочник `docs/cli/`, тесты, документы, живая проверка только чтением.
- Вне плана: пометка прочитанным (есть 4.7), чтение непрочитанных упоминаний и реакций (`messages.getUnreadMentions`, `getUnreadReactions`), склейка непрочитанного по всем темам форума (В2 Б), курсор по диалогам у `chat unread`, `--local` у непрочитанного (В8), обновление `chats` и `dialog_state` событиями потока (как В6 потока папок), настройки уведомлений чата (у `chat state` потока папок), папки чата, черновики (поток 0007), история изменений людей (`people`), удаление чата из базы при выходе.
- Затрагиваются: `internal/{store,rpc,tgclient,daemon,cli}`, `docs/cli/chats.md`, `docs/cli/messages-read.md`, `docs/contracts.md`, `FEATURES.md`, `AGENTS.md`, `README.md`, `docs/skill/tg-cli/`, `.agents/decisions/`.

## Main invariant

1. Ни один запрос записи и ни один запрос, меняющий состояние прочитанного, не уходит в Telegram; тест на подмене проверяет список вызванных методов.
2. Закрытый чат не виден ни в ответе, ни в базе; на него отвечают как на неизвестный.
3. Существующие команды и их ответы не меняются, кроме нового поля `settings` у `chat info` (добавление поля, В4); `message history` без нового параметра ведёт себя как раньше.
4. Миграция 0008 только добавляет таблицу и триггер; строки `chats` не меняются; номера 0005, 0006, 0007 этим потоком не заняты.
5. `internal/cli` не импортирует `tgclient`, gotd, `store`.
6. Ни тест, ни проверка по умолчанию не обращаются к живому Telegram; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: `message unread` и `chat unread` опираются на `rpc.ChatState`, `dialogState`, `peerDialogsAPI` и `store.UpsertDialogStates` потока папок, которых нет в `main`. Стратегия по В6: шаги 10.5 идут первыми и от папок не зависят.
- Риск: `listMigrations` требует номера подряд, а 0005, 0006, 0007 ещё не в `main`. Стратегия по В7; живая проверка только после перебазирования на `main` с настоящими 0005 до 0007 и сверки `ls internal/store/migrations` и `PRAGMA user_version`.
- Риск: маркер форума. У диалога форума `read_inbox_max_id` может не отражать темы. Стратегия: В2 А (форум только с `--topic`), шаг 1 сверяет по документации MTProto.
- Риск: `getHistory` с `min_id` возвращает свои исходящие после маркера (отправленные с другого устройства или через `tg`). Стратегия: отбрасывать `out`; страница может оказаться короче лимита при непустом `next_cursor`, это записано в справочнике.
- Риск: `chat unread` у владельца с тысячами диалогов тратит до 10 запросов на вызов. Стратегия: предел 1000 диалогов, пачки по 100 через общий дроссель, `complete: false` честно говорит, что список не досмотрен.
- Риск: триггер версий шумит, если источники дают разное значение одного поля (например, название личного чата из `chat list` и из `chat info`). Стратегия: шаг 1 сверяет, что `title` пользователя везде `displayName`; тест на повторную запись без изменений (версии нет).
- Риск: правка `chat info` (общие `tgclient/peers.go`, `rpc/api.go`, `daemon/persist.go`) пересекается с соседями второй волны. Стратегия: раздел «Файлы потока», вставки локальные, сбор `settings` в своём файле.
- Риск: `message unread` читает чужой текст. Стратегия: текст помечен `untrusted`, как у истории; в живой проверке тексты в отчёт не выводятся.

## Decision log

| Date | Decision | Reason | Confirmed |
| --- | --- | --- | --- |
| 2026-11-15 | За потоком зарезервирована миграция store 0008; 0004 в `main`, 0005, 0006, 0007 заняты потоками контактов, папок, отложенных | Номера идут подряд и не должны совпасть (было: задание менеджера) | no |
| 2026-11-15 | Состояние диалога (маркеры, счётчики, архив, закрепление, мьют) не дублируется: используются `chat state`, `rpc.ChatState`, `dialog_state` потока папок | Условие задания, один владелец данных (было: задание менеджера) | no |
| 2026-11-15 | Обе команды непрочитанного только читают; маркер прочитанного не двигается | Security в `AGENTS.md`: пометка прочитанным только по явной команде `chat mark-read` (было: правило проекта) | no |
| 2026-11-15 | [Закрытые чаты отбрасываются из ответов и базы](../decisions/20261114_0900_access_lists_hide_chats_and_untrusted_text_is_marked.md), на закрытый чат ответ `not_found` | Решение 6 и FEATURES 4.9 (было: правило проекта) | no |
| 2026-11-15 | Версии чата пишет триггер SQLite, а не Go-код `UpsertChats` | Ловит все пути записи без правки общего `store/telegram.go`; так же, как FTS-триггеры в 0002 (было: предложено планировщиком, подтверждается на ревью шага 2) | no |
| 2026-11-15 | В1. Как агент получает список чатов с непрочитанным (4.6.1): вариант А | А: свой файл, ответ сразу пригоден, `chat list` не меняется (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | В2. Что делает `message unread` для форума без `--topic` (4.6.2): вариант А | А: агент делает два понятных вызова, Daemon не тратит лимит запросов (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | В3. Порядок непрочитанных сообщений: вариант А | А: тот же формат и курсор, что у истории; агент видит свежее первым и общий `unread` (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | В4. Как хранить и показывать настройки чата (10.5.2): вариант А | А: данные уже приходят в `chat info`, новых запросов нет (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | В5. Что считать изменением чата и как агенту его прочитать (10.5.3): вариант А | А: история хранится и читается, шума нет (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | В6. Как работать при зависимости от неслитого потока папок (`rpc.ChatState`, `dialogState`, `peerDialogsAPI`, `UpsertDialogStates`): вариант А | А: без дубля, независимая часть не ждёт (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | В7. Как разрабатывать миграцию 0008, пока 0005 до 0007 не в `main`: вариант А | А: то же правило, что у потока папок; ошибка заглушек ловится сама (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | В8. Нужен ли `--local` у `chat unread` и `message unread`: вариант А | А: устаревшее непрочитанное вводит агента в заблуждение; без сети есть `chat state --local` потока папок (было: Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана) | no |
| 2026-11-15 | Уточнение к В7: потоки папок и контактов уже слиты в `main`, миграции идут 0001-0005, поэтому миграция этого потока имеет номер 0006 (не 0008), заглушки не нужны | Номера идут подряд (было: менеджер, по состоянию `main`) | no |

## Step 0. Fix decisions before changes

- [ ] Менеджер принял решения В1 до В8 (владелец делегировал), записал их в Decision log, контракт поправлен под ответы.
- [ ] Менеджер сверил с потоком папок имена `rpc.ChatState`, `dialogState`, `peerDialogsAPI`, `UpsertDialogStates` и их судьбу на ревью (по В6).
- [ ] Исполнитель Go-кода (Codex Terra) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [ ] Ветка начинается от текущего `main`; если `main` ушёл вперёд, ветка обновляется слиянием `main` до шага 1.

## Step 1. Audit and baseline

Исполнитель: Codex Terra. Код не меняется. Отчёт в `var/agents/plans/20261115_0241_read_unread_chats/audit.md` (вне git).

- [ ] `just check` и `just build` на чистом дереве; результат в Known facts.
- [ ] Диалоги: включает ли `query.GetDialogs` без `folder_id` архивные чаты или даёт `DialogFolder`; как задать `folder_id = 1`; как получить из `dialogs.Elem` и `tg.Dialog` все поля элемента `chat unread`; можно ли переиспользовать `toChat` и `dialogState` без правки общего файла.
- [ ] Маркер чата: `messages.getPeerDialogs` для одного пира, поле `ReadInboxMaxID`; что Telegram отдаёт для форума и для канала без подписки.
- [ ] Маркер темы: `messages.getForumTopicsByID{Peer, Topics}`; ответ для несуществующей темы; поле `ReadInboxMaxID`.
- [ ] История с нижней границей: `GetHistoryQueryBuilder.MinID` и `GetRepliesQueryBuilder.MinID`; останавливается ли итератор на `min_id`; попадают ли исходящие; как вставить `MinID` в `rpc.HistoryQuery` и `tgclient/history.go` одной локальной правкой.
- [ ] Настройки: какие поля `Channel`, `ChannelFull`, `Chat`, `ChatFull`, `UserFull` дают состав `settings` из контракта; функция обратная `bannedRights` (права в имена `chat permissions`); `ChatReactions*`.
- [ ] Версии: срабатывает ли `AFTER UPDATE` триггер на `INSERT ... ON CONFLICT DO UPDATE` в modernc SQLite; совпадает ли `title` личного чата у `chat list`, `chat resolve`, `chat info`.
- [ ] Как `classifyRequest` в `tgclient/throttle.go` относит `getPeerDialogs` и `getForumTopicsByID` (ожидается чтение).
- [ ] Как `search` и `note` работают без входа и как так же сделать `chat changes`; как разрешить чат по базе для `@username` (`store/chat_known.go`).
- [ ] Как фейки `fakeTelegram` в `internal/daemon/daemon_test.go` и `internal/cli/cli_test.go` дополняются методами из отдельного тестового файла; как встроить свой интерфейс `unreadTelegram` в `Telegram` одной строкой.
- [ ] Как тест полноты схемы и `TestDocsGenerated` требуют разделы и таблицы для новых команд и флагов.
- [ ] Записать итоги в Known facts, непокрытое в Risks и Open questions.

## Файлы потока

Новые файлы, которыми владеет поток (другие потоки их не трогают):

| Слой | Файлы | Что внутри |
| --- | --- | --- |
| `internal/store` | `migrations/0008_chat_versions.sql`, `chat_versions.go`, `chat_versions_test.go` | таблица `chat_versions`, триггер, чтение `ChatVersions` с `ChatFilter` |
| `internal/rpc` | `unread.go`, `unread_test.go` | константы `chat.unread`, `message.unread`, `chat.changes`; параметры, результаты, проверка `limit`, `topic`, `cursor` |
| `internal/tgclient` | `unread.go`, `chat_settings.go`, `unread_test.go`, `chat_settings_test.go` | `UnreadChats`, `UnreadMessages`, маркер темы, сбор `settings`, права в имена |
| `internal/daemon` | `unread.go` (интерфейс `unreadTelegram`, `registerUnread`, обработчики), `unread_test.go` (методы `fakeTelegram` для daemon) | фильтр доступа, сохранение, `chat changes` из базы |
| `internal/cli` | `unread_cmd.go`, `chat_changes_cmd.go`, `unread_cmd_test.go` (методы `fakeTelegram` для cli) | команды, флаги, `commandSpec`, вывод |
| решения | `.agents/decisions/20261115_HHMM_unread_is_read_live_and_chat_changes_are_kept_by_a_store_trigger.md` | решение плана |
| временное | `var/agents/plans/20261115_0241_read_unread_chats/` | отчёт аудита, вне git |

Общие файлы. Поток вносит в них только локальную вставку рядом с соседними строками того же вида, без переупорядочивания, переименований и правки чужих строк. Это полный список изменений общих файлов:

| Файл | Вставка |
| --- | --- |
| `internal/rpc/api.go` | поле `Settings json.RawMessage` (`json:"settings,omitempty"`) в `ChatInfo`; поле `MinID int` в `HistoryQuery` |
| `internal/tgclient/history.go` | две строки: `MinID(q.MinID)` в обоих построителях, когда `q.MinID > 0` |
| `internal/tgclient/peers.go` | в `ChatInfo` по одной строке `info.Settings = ...Settings(...)` в трёх ветках `switch` |
| `internal/daemon/persist.go` | `Settings: info.Settings` в `saveChatInfo` |
| `internal/daemon/daemon.go` | одна строка `d.registerUnread()` в конце `register()` |
| `internal/daemon/telegram.go` | одна строка `unreadTelegram` (встроенный интерфейс) в конце `Telegram` |
| `internal/cli/chat_cmd.go` | два имени `a.chatUnreadCommand(), a.chatChangesCommand()` в конце `cmd.AddCommand(...)` |
| `internal/cli/read_cmd.go` | одно имя `a.messageUnreadCommand()` в `cmd.AddCommand(...)` |
| `internal/store/schema_test.go` | `chat_versions` в списке ожидаемых таблиц и триггер в списке объектов |
| `docs/cli/chats.md`, `docs/cli/messages-read.md` | разделы `chat unread`, `chat changes`, `message unread`, поле `settings` у `chat info`; таблицы пересобираются |
| `docs/contracts.md` | один пункт «Unread and chat changes» |
| `FEATURES.md` | подпункты 4.6 и 10.5, сценарии 4.6 и 10.5, строки статуса 661 и 664 |
| `AGENTS.md` | не меняется: «Layout» перечисляет пакеты, а не файлы; правка только если шаг 7 найдёт устаревший текст |
| `.agents/decisions/_TOC.md` | одна строка решения |
| `README.md`, `docs/skill/tg-cli/SKILL.md` | по одному абзацу о непрочитанном и об истории чата |

Если шаг 1 покажет, что встроенный интерфейс невозможен, поток добавляет методы прямо в `Telegram` одной группой строк в конце и пишет это в Known facts.

## Implementation steps

Код начинается с шага 2. Каждый шаг выполняет Codex Terra в worktree `../tg-cli-read_unread_chats`, проверяет Claude (тесты, дифф, `just check`), коммитит Claude. Шаги 2 до 4 не зависят от потока папок, шаги 5 и 6 зависят (В6).

- [ ] 2. `internal/store`: миграция `0008_chat_versions.sql` (с временными заглушками по В7): `chat_versions(chat_id INTEGER NOT NULL, version INTEGER NOT NULL, type TEXT, title TEXT, username TEXT, about TEXT, settings TEXT CHECK (settings IS NULL OR json_valid(settings)), observed_at INTEGER NOT NULL, PRIMARY KEY (chat_id, version)) STRICT` и триггер `chats_versions_au AFTER UPDATE ON chats` с условием из контракта (изменение известного значения, `members` и `unread` не в счёт), номер версии `COALESCE(MAX(version), 0) + 1`, `observed_at = new.last_seen_at`. Метод `ChatVersions(ctx, chatID, limit, filter)`. Тесты: миграция на пустой базе и на базе версии 7, смена названия даёт версию с прежним названием, повторная запись без изменений и смена `members` версии не дают, заполнение `about` из `NULL` версии не даёт, смена `settings` даёт версию, закрытый чат не читается.
- [ ] 3. Настройки чата: `tgclient/chat_settings.go` (сбор `settings` из полных данных, обратная к `bannedRights` функция), вставки в `peers.go`, `rpc/api.go`, `daemon/persist.go`. Тесты: супергруппа с правами по умолчанию, медленным режимом и реакциями `some`, канал с подписями и запретом пересылки, обычная группа, личный чат с TTL, пустые поля не выводятся, `settings` попал в `chats.settings`, повторный `chat info` с другим медленным режимом создал версию.
- [ ] 4. `chat changes`: `rpc/unread.go` (константа, параметры, результат), обработчик в `daemon/unread.go` (разрешение чата по базе, фильтр доступа, без входа), `cli/chat_changes_cmd.go` (`commandSpec` класс `read`, `untrusted`, `json`, `jsonl`, `human`). Тесты: без входа работает, закрытый чат `not_found`, неизвестный чат `not_found`, лимит, порядок от новых к старым.
- [ ] 5. `chat unread`: `tgclient/unread.go` (`UnreadChats` своей итерацией `getDialogs`, предел 1000, `--archived` через `folder_id = 1`, переиспользование `toChat` и `dialogState`), обработчик (фильтр закрытых до ответа и до записи, `saveChats`, `UpsertDialogStates`), CLI. Тесты на подмене: только `getDialogs`, `unread_mark` без счётчика попадает в ответ, `muted` по `mute_until`, предел 1000 даёт `complete: false`, закрытые чаты не в ответе и не в базе, без входа код 4.
- [ ] 6. `message unread`: вставка `MinID` в `rpc.HistoryQuery` и `tgclient/history.go`, `UnreadMessages` (маркер через `peerDialogsAPI` или `getForumTopicsByID`, затем `History` с `MinID`, отбрасывание `out`), обработчик (доступ до и после, `saveMessages`, `UpsertDialogStates`), CLI. Тесты на подмене: вызваны только методы чтения из контракта, `min_id` равен маркеру, курсор продолжает и не опускается ниже маркера, исходящие отброшены, форум без `--topic` код 2, `--topic` у не форума код 2, неизвестная тема код 7, закрытый чат `not_found` без вызова Telegram, `message history` без `MinID` не изменился.
- [ ] 7. Справочник и документы. Codex (лёгкая модель): разделы `docs/cli/` и пересборка таблиц `go test ./internal/cli -run TestDocsGenerated -update-docs`, затем `go test ./internal/cli` зелёный. `docwriter` и `glavred` по `docs_chain.md`: `FEATURES.md` (подпункты 4.6.1, 4.6.2, 10.5.2, 10.5.3 с поведением из контракта, сценарии, статус), `docs/contracts.md`, `README.md`, навык `docs/skill/tg-cli/`, решение в `.agents/decisions/` и строка в `_TOC.md`.
- [ ] 8. Живая проверка Claude только чтением, после перебазирования на `main` с настоящими 0005 до 0007 (сверить `ls internal/store/migrations` и `PRAGMA user_version`), с паузами между вызовами: `chat unread` и `chat unread --archived` (число чатов и `complete`), `message unread` для одного чата с непрочитанным и для темы форума, если такая есть; до и после снова `chat unread`: `unread` и `read_inbox_max_id` этого чата не изменились, в приложении владельца чат остался непрочитанным; `chat info` для «Браин Тест» (`settings` есть и совпадает с приложением), затем `chat changes` для него; сверка числами по `sqlite3` (строки `chat_versions`, `dialog_state`, нет закрытых чатов). Тексты сообщений и названия в отчёт не выводятся.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/store/... ./internal/rpc/... ./internal/tgclient/... ./internal/daemon/... ./internal/cli/...` | зелёный | not_run |
| Только чтение | тесты `tgclient` | подмена получила только `getDialogs`, `getPeerDialogs`, `getForumTopicsByID`, `getHistory`, `getReplies` и запросы `chat info` | not_run |
| Закрытые чаты | тесты `daemon` и `store` | нет в ответе и в базе, ответ `not_found` | not_run |
| Миграция и триггер | тесты `store` | применяется к пустой базе и к базе версии 7; версии только по правилу контракта | not_run |
| История без изменений | тесты `tgclient` и `daemon` для `message history` | ответы и запросы как до плана | not_run |
| Справочник | `go test ./internal/cli -run 'TestDocs'` | у каждой новой команды и флага есть раздел, таблицы совпадают со схемой | not_run |
| Полнота схемы | тест `internal/cli` | класс `read`, тип результата, пометки `untrusted` у новых команд | not_run |
| Временных миграций нет | `ls internal/store/migrations` перед слиянием | только настоящие `0001` до `0008` | not_run |
| Живая проверка | шаг 8 | маркер прочитанного не сдвинулся, числа совпадают | not_run |

## Deploy / rollback

- [ ] Выкладки нет; бинарник собирается `just build`.
- [ ] Порядок слияния: 0005 (контакты), 0006 (папки), 0007 (отложенные), затем эта ветка (0008).
- [ ] Откат: откат ветки до слияния. После применения 0008 на живой базе старая сборка не откроет базу (`user_version` 8); откат тогда это сборка с этой веткой. Таблица `chat_versions` и триггер безвредны для остальных путей.

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261115_0241_read_unread_chats/`.
- [ ] Временные файлы не в git (`/var/` в `.gitignore`).
- [ ] Временные заглушки миграций (по В7) удалены до слияния.

## Definition of done

- [ ] Код изменён только в рамках плана и раздела «Файлы потока».
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.
- [ ] Решение записано в `.agents/decisions/`, `_TOC.md`, `FEATURES.md` и `docs/cli/` обновлены.

## Known facts

- `tg schema` сборки `main` ea157b2: группы `auth`, `chat`, `daemon`, `message`, `note`, `schema`, `search`, `version`. Команд `chat unread`, `chat changes`, `message unread` нет; у `chat list` флаги только `--limit`, `--type`; у `message history` флаги `--cursor`, `--limit`, `--since`, `--topic`, `--until`, нижней границы по id нет.
- `rpc.Chat{ID, Type, Title, Username, Unread}`; `rpc.ChatInfo{ID, Type, Title, Username, About, Members, Forum, Verified}`; `rpc.Topic` содержит `Unread`, но не маркер прочитанного.
- `chats` (0002): `id, type, title, username, about, members, forum, verified, unread, settings, raw, search_text, first_seen_at, last_seen_at`. `settings` и `raw` никто не пишет. `message_versions` хранит версии сообщений, для чатов аналога нет.
- Все записи чатов идут через `store.UpsertChats` (`persist.go:57` и `persist.go:103`); `mergeChat` не затирает известное значение пустым, а `ON CONFLICT DO UPDATE` обновляет строку при каждом чтении (`last_seen_at`).
- Миграции в `main`: 0001 до 0004; `listMigrations` требует номера подряд с 1.
- Поток папок (не в `main`): `rpc.ChatState{ChatID, ReadInboxMaxID, ReadOutboxMaxID, Unread, UnreadMentions, UnreadReactions, UnreadMark, Pinned, Archived, TopMessage, TTLPeriod, MuteUntil, Silent, ShowPreviews, SyncedAt}`; `tgclient.dialogState(*tg.Dialog)`, `peerDialogsAPI`, `Client.ChatStates`; `store.UpsertDialogStates(ctx, states, filter)`; таблица `dialog_state(chat_id, data JSON, synced_at)`; его ветка содержит заглушки 0004 и 0005 и отстаёт от `main`.
- gotd: у построителей `GetHistory` и `GetReplies` есть `MinID`; `ForumTopic` содержит `ReadInboxMaxID`, `UnreadCount`, `UnreadMentionsCount`, `UnreadReactionsCount`; есть `MessagesGetForumTopicsByID`; `MessagesGetDialogsRequest` имеет `FolderID`.
- Права по умолчанию пишет `chat permissions` через `bannedRights(names, until)` в `tgclient/chat_admin.go`; обратной функции нет.

## Open questions

Решает менеджер по делегированию владельца, рекомендованный вариант первым. Стоимость в часах работы Codex.

| № | Question | Why it blocks | Options и стоимость | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| В1 | Как агент получает список чатов с непрочитанным (4.6.1) | Определяет команду, файлы и пересечение с соседями | А) новая команда `tg chat unread [--limit N] [--archived]` в своих файлах, своя итерация `getDialogs`, поля чата и счётчики: около 4 часов. Б) флаг `chat list --unread` и новые поля в `rpc.Chat`: около 3 часов, но правка общих `daemon.go` (`chatList`), `tgclient/client.go` и `rpc.Chat`, который выводят ещё `chat resolve` и `chat common`. В) ничего, агент фильтрует `chat state --limit N` потока папок: 0, но в ответе нет названий и типа, а агент листает все диалоги | А: свой файл, ответ сразу пригоден, `chat list` не меняется | принято |
| В2 | Что делает `message unread` для форума без `--topic` (4.6.2) | Число запросов и сложность курсора | А) код 2 с подсказкой: темы с непрочитанным видны в `chat topics` (`unread`), дальше `--topic N`: 0. Б) Daemon сам обходит темы с непрочитанным и склеивает сообщения: плюс день, запрос на каждую тему, курсор из пары тема и id | А: агент делает два понятных вызова, Daemon не тратит лимит запросов | принято |
| В3 | Порядок непрочитанных сообщений | Формат ответа и курсора | А) от новых к старым, как `message history`, нижняя граница `min_id`, курсор истории: около 4 часов на весь шаг 6. Б) от старых к новым, начиная с первого непрочитанного: плюс 2 часа (свой обход через `offset_id` и `add_offset`, свой курсор), удобнее «догонять» переписку | А: тот же формат и курсор, что у истории; агент видит свежее первым и общий `unread` | принято |
| В4 | Как хранить и показывать настройки чата (10.5.2) | Меняет ответ `chat info` и общие файлы | А) `chat info` выводит `settings` и пишет его в `chats.settings`: около 3 часов, вставки в `peers.go`, `rpc/api.go`, `persist.go`. Б) только хранить, не выводить: те же 3 часа, агенту данные не видны. В) отдельная команда `chat settings`: плюс 2 часа, второй запрос полных данных чата дублирует `chat info` | А: данные уже приходят в `chat info`, новых запросов нет | принято |
| В5 | Что считать изменением чата и как агенту его прочитать (10.5.3) | Схема 0008 и число команд | А) триггер пишет прежнюю версию при смене `type`, `title`, `username`, `about`, `settings`; команда `tg chat changes <чат> [--limit N]` из базы: около 4 часов. Б) то же без команды: около 2,5 часов, история невидима агенту. В) А плюс `members`: шум, версия почти на каждый `chat info` большой группы | А: история хранится и читается, шума нет | принято |
| В6 | Как работать при зависимости от неслитого потока папок (`rpc.ChatState`, `dialogState`, `peerDialogsAPI`, `UpsertDialogStates`) | Шаги 5 и 6 не собираются без кода папок | А) шаги 2 до 4 идут на `main` сразу; перед шагом 5 ветка обновляется из `main`, где уже есть папки; если к шагу 5 папки не слиты, менеджер вливает в эту ветку ветку папок, а при её правке на ревью вливает снова: 0, возможна пауза. Б) своя копия чтения маркера и диалогов без зависимости, запись `dialog_state` отдельной правкой после слияния: плюс 3 часа и дубль кода. В) ветвиться от ветки папок сразу: 0, но вся ветка зависит от ревью папок | А: без дубля, независимая часть не ждёт | принято |
| В7 | Как разрабатывать миграцию 0008, пока 0005 до 0007 не в `main` | `listMigrations` требует номера подряд, иначе `just check` падает | А) временные пустые `0005_placeholder_do_not_merge.sql`, `0006_...`, `0007_...`; при обновлении из `main` настоящие файлы дают дубль версии и `listMigrations` падает, пока заглушки не удалены; живой запуск только после удаления: 0, нужна дисциплина слияния (как В9 потока папок). Б) шаг 2 ждёт слияния 0005 до 0007, шаги 3 и 4 идут на подмене хранилища: поток ждёт, плюс полдня. В) менять механизм миграций: общая инфраструктура, не для этого потока | А: то же правило, что у потока папок; ошибка заглушек ловится сама | принято |
| В8 | Нужен ли `--local` у `chat unread` и `message unread` | Свежесть против работы без сети | А) нет: непрочитанное всегда из Telegram, база пополняется попутно: 0. Б) `chat unread --local` из `chats` и `dialog_state`, `message unread --local` из сохранённых сообщений выше маркера: плюс 3 часа, данные устаревают, потому что поток событий маркеры не обновляет (В6 потока папок) | А: устаревшее непрочитанное вводит агента в заблуждение; без сети есть `chat state --local` потока папок | принято |
