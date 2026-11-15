# Пошаговый план: флаги каналов, заявки на вступление, свой профиль и кто прочитал сообщение

ID: 20261115_0439_channels_profile
Created: 2026-11-15
Owner: Мязин Евгений
Branch: feature/20261115_0439_channels_profile (worktree `../tg-cli-channels_profile`, создан планировщиком от `main` b8330a8)
PR: нет

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: draft
- Last update: 2026-11-15
- Next safe step: менеджер принимает решения Q1 до Q9 из «Open questions» (владелец делегировал их менеджеру, принимаются рекомендуемые варианты) и пишет их в Decision log; затем статус `approved` и шаг 1 (аудит, Codex Terra в worktree `../tg-cli-channels_profile`). Q10 решается перед шагом 7. Код до решений Q1 и Q2 не писать: они задают барьер записи и форму команд.

## Context and sources

- Read: `AGENTS.md`, `INTENT.md`, `FEATURES.md` (7.3, 7.4, 7.6, 7.5, 16.7, 16.8, 18.3, 18.4, блок «Статус реализации»), `.agents/rules/plans.md`, `.agents/rules/project/AGENTS.md`, образец `.agents/plans/20261114_2244_users_contacts.md`, план админки `.agents/plans/20261114_2244_chat_admin.md` (Decision log Q1, Q1b, Q9, Q10, Q11, Q12), `docs/contracts.md`, оглавления `docs/cli/chats.md`, `docs/cli/access.md`, `docs/cli/folders-and-account.md`, `docs/cli/messages-read.md`.
- Код: `internal/access/access.go` (`Policy`, `AdminAllowed`, `CreateAllowed`, разбор полей `admin` и `create`), `internal/daemon/chat_admin.go` (`ChatAdmin`, `adminReference`, `memberReference`, `moderation`, `adminTypeOK`, `chatAdmin`), `internal/rpc/chat_admin.go` (`ChatOwnAdminRights`, `ErrAdminDisabled`), `internal/tgclient/chat_admin.go` (`ownAdminRights`), `internal/tgclient/chat_settings.go` (`channelSettings`), `internal/tgclient/throttle.go` (`writeRequest`), `internal/tgclient/readonly_test.go` (`readFiles`), `internal/daemon/access_test.go` (карта `classes`), `internal/daemon/daemon.go` (статус `access`), `internal/rpc/api.go` (`AccessStatus`, `ChatInfo`), `internal/rpc/users.go` (`UserProfile`), `internal/tgclient/users_read.go`, `internal/cli/chat_cmd.go`, `internal/cli/account_cmd.go`, `internal/cli/read_cmd.go`, `internal/cli/partial.go`.
- Проверено командами: `just build` и `./dist/tg schema` (список всех 80 команд с классами и флагами), `rg` по `signature|antispam|forum|prehistory|join.?request|emoji.?status|birthday|personal.?channel|privacy|read.?participants` в `internal/`, `go list -m -f '{{.Dir}}' github.com/gotd/td` (v0.166.0) и структуры запросов в `tg/*.go`, `git worktree list`.
- Not verified: `just check` на ветке не запускался (это шаг 1). Не проверены ограничения Telegram по типам чатов для каждого флага, коды ошибок, поведение `toggleForum(false)` для существующих топиков, порог числа участников для `antispam` и для `getMessageReadParticipants`, требования Premium. Всё это шаг 1.

## Goal

Агент включает и выключает флаги канала и супергруппы, смотрит и решает заявки на вступление, читает и меняет свой профиль (emoji status, дата рождения, личный канал, правила приватности) и узнаёт, кто прочитал его сообщение. Каждая запись проходит барьер, который открывает владелец в `access.json`.

## Что уже есть (аудит планировщика, 2026-11-15)

Блок «Статус реализации» в `FEATURES.md` устарел; ниже состояние по коду `main` b8330a8 и по `tg schema`.

| Пункт | Состояние | Где в коде |
| --- | --- | --- |
| 16.7 чтение флагов канала | уже есть частично: `tg chat info` отдаёт в `settings` ключи `signatures`, `join_request`, `join_to_send`, `hidden_history` (prehistory), а `forum` в самом ответе | `internal/tgclient/chat_settings.go:64` (`channelSettings`), `internal/rpc/api.go:220` (`ChatInfo.Forum`) |
| 16.7 чтение `antispam`, `signature_profiles` | нет: поля `ChannelFull.Antispam` и `Channel.SignatureProfiles` gotd не читаются | `internal/tgclient/chat_settings.go` |
| 16.7 смена флагов | нет ни одной команды | `tg schema`: нет `chat set` и аналогов |
| 16.7 одобрение заявок | нет: ни чтения заявок, ни одобрения; управления инвайт-ссылками (7.6.4) тоже нет | `tg schema`; `FEATURES.md` строка статуса 7.6 |
| Барьер `admin` | уже есть: список `admin` в `access.json`, `Policy.AdminAllowed`, отказ кодом 9 с `data.list: "admin"`, проверка своих прав в `--dry-run`, журнал `moderation.jsonl` | `internal/access/access.go:47`, `internal/daemon/chat_admin.go:64` (`adminReference`), `internal/rpc/chat_admin.go:174`, `internal/tgclient/chat_admin.go:404`, `internal/daemon/chat_admin.go:164` |
| Цель-пользователь для модерации | уже есть: `memberReference` (только `block` закрывает цель, решение Q12 админки) | `internal/daemon/chat_admin.go:119` |
| 16.8 чтение своего профиля | нет: `tg user profile` отдаёт имя, био, статус, но не день рождения, личный канал и emoji status (их явно вынес за рамки план людей) | `internal/rpc/users.go:20`, `internal/tgclient/users_read.go:47` |
| 16.8 запись профиля и приватность | нет ни одной команды; группа `tg account` есть (`sessions`, `blocked`, `notify`, все только чтение) | `internal/cli/account_cmd.go:19` |
| 18.4 кто прочитал | нет | `tg schema`: нет `message readers` |
| Частичный успех серии | уже есть: `partial: true`, `errors`, код 1 | `internal/cli/partial.go`, `docs/contracts.md` строка 7 |

## Problem and contract

Тип плана: product (новые команды записи и чтения, новое поле `access.json`).

Контракт записан для рекомендуемых вариантов Q1 до Q9; другой ответ меняет отмеченные места.

### 16.7. Флаги канала и супергруппы

- Команда (Q2): `tg chat set <chat> [--signatures on|off] [--signature-profiles on|off] [--antispam on|off] [--forum on|off] [--prehistory visible|hidden] [--join-requests on|off] [--join-to-send on|off] [--dry-run]`, класс `account_write`, не `dangerous` (Q4). Нужен хотя бы один флаг, иначе код 2 до Daemon.
- Методы Telegram (gotd v0.166.0): `channels.toggleSignatures{signatures_enabled, profiles_enabled}`, `channels.toggleAntiSpam`, `channels.toggleForum{enabled, tabs, sidebar}`, `channels.togglePreHistoryHidden`, `channels.toggleJoinRequest{enabled, apply_to_invites?, guard_bot?}`, `channels.toggleJoinToSend`. `--signatures` и `--signature-profiles` в одном вызове идут одним запросом. `toggleForum` шлёт `tabs` и `sidebar` выключенными (вид топиков списком, как по умолчанию в клиентах; уточняет шаг 1). `apply_to_invites` и `guard_bot` не задаются.
- Типы чатов (предварительно, уточняет шаг 1): `signatures` и `signature-profiles` только канал; `antispam`, `forum`, `prehistory`, `join-to-send` только супергруппа; `join-requests` супергруппа и канал. Обычная группа даёт код 2: смена этих флагов в Telegram сначала превращает её в супергруппу, а миграция вне плана. Неподходящий тип даёт код 2 до Telegram.
- Барьер: тот же, что у админки (7.3, 7.6). Чат должен быть видим по `allow` и `block` и стоять в `admin`; иначе код 9 `write_disabled` с `data.list: "admin"` и ни одного запроса записи. Используется `adminReference` с `write=false` без изменений.
- Порядок: флаги применяются по одному в фиксированном порядке (как в списке выше), один запрос на флаг. Ответ `{chat_id, changes: [{flag, value, changed}], partial?, errors?}`. `CHAT_NOT_MODIFIED` значит `changed: false` и код 0 (как Q11 админки). Ошибка одного флага не отменяет уже применённые: при смешанном исходе `partial: true`, элементы `errors` и код 1; если не применился ни один, возвращается первая ошибка с её кодом. FLOOD_WAIT и потеря входа останавливают остальные флаги.
- `--dry-run`: все проверки Daemon, тип чата и свежее чтение своих прав (`OwnAdminRights`, как Q10 админки); нужное право по флагу уточняет шаг 1 (ожидается `change_info`, для `antispam` возможно `delete_messages`). Нет права: код 5 `chat admin rights are required`. Методы записи не вызываются.
- Чтение: `tg chat info` получает в `settings` ключи `antispam` и `signature_profiles` (одна вставка в `channelSettings`). Других изменений `chat info` нет.
- Журнал: каждая попытка пишется в `<profile>/moderation.jsonl` (Q9) с `action: "set"`, `chat_id`, `flags` (имя и значение), фазами `intent` и `result`; сбой записи `intent` отменяет действие.
- Записи не повторяются: шесть запросов `channels.toggle*` добавляются в `writeRequest`.

### 16.7. Заявки на вступление (Q3)

- `tg chat requests <chat> [--limit 1..100] [--cursor C] [--search ТЕКСТ]`, класс `read`: `messages.getChatInviteImporters{requested: true}` с курсором по `offset_date` и `offset_user`. Чат должен быть видим; список `admin` не нужен (это чтение), но Telegram без прав администратора ответит ошибкой: код 5. Элемент: `user_id`, `name`, `username`, `date`, `about` (текст заявки). `name`, `username`, `about` помечены `untrusted`. Люди из `block` убираются до ответа и не сохраняются; остальные сохраняются в `people` как частичный источник.
- `tg chat approve <chat> <user> [--dry-run]` и `tg chat decline <chat> <user> [--dry-run]`, класс `account_write`, не `dangerous`: `messages.hideChatJoinRequest{approved}`. Барьер `admin` как у `chat set`; цель через `memberReference` (закрывает только `block`). Ответ `{chat_id, user_id, approved}`. Журнал `moderation.jsonl` с `action: "approve"` или `"decline"`. Одобрение и отклонение всех заявок сразу (`hideAllChatJoinRequests`) вне плана.

### 16.8. Свой профиль

- Барьер (Q1): новое булево поле `"profile": true` в `access.json`, по умолчанию выключено. Без него `account set`, `account privacy set` отвечают кодом 9 `write_disabled` с `data.list: "profile"` и не шлют запросов записи. Поле видно в `daemon status` (`access.profile: true`). Ошибка файла закрывает и его, как остальные поля. Чтение своего профиля и правил приватности барьера не требует.
- Команды (Q5): `tg account profile` (класс `read`): один `users.getFullUser(self)`. Ответ: `id`, `first_name`, `last_name`, `username`, `bio`, `premium`, `birthday` (`{day, month, year?}` или нет поля), `personal_channel_id` (Bot API id или нет поля), `emoji_status` (`{document_id, until?, collectible?}` или нет поля). Имена, `username` и `bio` помечены `untrusted` по образцу `user profile`. `tg user profile` не меняется.
- `tg account set [--birthday ГГГГ-ММ-ДД|ММ-ДД|none] [--personal-channel <chat>|none] [--emoji-status <document_id>|none] [--emoji-status-until <RFC3339>] [--dry-run]`, класс `account_write`, не `dangerous`. Нужен хотя бы один флаг. Методы: `account.updateBirthday` (без поля `birthday` при `none`), `account.updatePersonalChannel` (`inputChannelEmpty` при `none`), `account.updateEmojiStatus` (`emojiStatusEmpty` при `none`). Порядок и частичный успех как у `chat set`. Дата проверяется до Daemon (существующий день, год от 1900 до текущего); `--emoji-status-until` без `--emoji-status` и время в прошлом дают код 2.
- Личный канал: `<chat>` разрешается как ссылка на чат, должен быть видим по `allow` и `block` и быть каналом (не супергруппой), иначе `not_found` или код 2; список `admin` не нужен, потому что канал не меняется. Остальные условия Telegram (публичный, владелец создатель) проверяет сервер, ошибка даёт код 5.
- Emoji status (Q8): ставится по `document_id`. Без Premium Telegram отказывает; ответ код 5 с причиной Telegram и `data.reason: "premium_required"` (структурный отказ, как требует 18.3). `tg account emoji-statuses [--recent]` (класс `read`): `account.getDefaultEmojiStatuses` или `account.getRecentEmojiStatuses` с `hash = 0`; элемент `{document_id, until?}`; коллекционные статусы показываются с `collectible: true`, `title` и `slug` (`untrusted`), поставить их можно тем же `document_id`, если Telegram примет (уточняет шаг 1).
- Приватность (Q6): `tg account privacy get <key>` (класс `read`, один `account.getPrivacy`) и `tg account privacy set <key> --default everybody|contacts|nobody [--allow-user U]... [--deny-user U]... [--allow-chat C]... [--deny-chat C]... [--allow-premium] [--allow-bots|--deny-bots] [--allow-close-friends] --yes [--dry-run]` (класс `account_write`, `dangerous`, Q4): `account.setPrivacy` заменяет весь набор правил ключа, поэтому без `--yes` и без `--dry-run` код 2 до Daemon. Ключи: `last-seen`, `phone-number`, `added-by-phone`, `profile-photo`, `forwards`, `calls`, `p2p-calls`, `invites`, `voice-messages`, `bio`, `birthday`, `gifts-auto-save`, `no-paid-messages`, `saved-music` (точный список по gotd уточняет шаг 1; ключи без типа в gotd v0.166.0 не включаются). Ответ `get` и `set`: `{key, default, allow_users, deny_users, allow_chats, deny_chats, allow_premium, allow_bots, deny_bots, allow_close_friends}`; пользователи и чаты списками id. Пользователь в правиле разрешается как человек (закрытый списками даёт `not_found`), чат должен быть видим; в ответе `get` закрытые id убираются и считаются в `hidden: N`, чтобы агент знал, что `set` по этим данным их потеряет. Если `hidden > 0`, `set` для этого ключа отказывает кодом 2 с подсказкой, пока не передан `--replace-hidden`. Глобальные настройки приватности (`account.setGlobalPrivacySettings`) вне плана.
- Журнал (Q9): `account set` и `account privacy set` пишут в `moderation.jsonl` `action: "account.set"` или `"privacy.set"`, имена изменённых полей и ключ приватности, без значений даты рождения и без списков пользователей.
- Записи не повторяются: `account.updateBirthday`, `account.updatePersonalChannel`, `account.updateEmojiStatus`, `account.setPrivacy` в `writeRequest`.

### 18.4. Кто прочитал сообщение

- `tg message readers <chat> <id>`, класс `read`, чат должен быть видим. Группа и супергруппа: `messages.getMessageReadParticipants`. Личный чат (Q7): `messages.getOutboxReadDate`, ответ из одного читателя (собеседник) или пустой. Канал, бот и Saved Messages: код 2 до Telegram.
- Ответ `{chat_id, id, readers: [{user_id, name?, username?, date}]}`; `date` в RFC 3339 UTC. Имена берутся из кэша пиров и `people` без лишних запросов; неизвестное имя пропускается. `name` и `username` помечены `untrusted`. Читатели из `block` (человек это чат со своим id, решение Q5 плана людей) убираются до ответа.
- Ограничения Telegram (только своё исходящее сообщение, срок хранения отметок, порог числа участников, скрытое время прочтения) дают код 5 с причиной; точные имена ошибок и их перевод уточняет шаг 1. Ни один запрос записи не уходит.

### Общее

- Миграции store нет: состояние живёт в Telegram, локально только `people` и `saveChats`.
- Все новые методы JSON-RPC получают класс в `TestRegisteredMethodsHaveAccessClass`.
- Релиз одним слоем; откат: откат ветки, схема базы не меняется. Поле `profile` в `access.json` старой версией не распознаётся: проверить в шаге 1, что неизвестное поле не закрывает файл; если закрывает, записать в Risks и в README.

## Файлы потока

Новые файлы, которыми владеет поток (другие потоки их не трогают):

- `internal/rpc/channels_profile.go`, `internal/rpc/channels_profile_test.go`: константы флагов и ключей приватности, параметры и ответы (`ChatSetParams`, `ChatSetResult`, `ChatFlagChange`, `JoinRequestsParams`, `JoinRequestsResult`, `JoinRequest`, `JoinDecisionParams`, `JoinDecisionResult`, `AccountProfile`, `AccountSetParams`, `AccountSetResult`, `EmojiStatusList`, `PrivacyParams`, `PrivacyRules`, `ReadersParams`, `ReadersResult`, `Reader`), проверка флагов, даты рождения, ключей и правил, `ErrProfileDisabled`.
- `internal/tgclient/channels_profile_read.go`: `JoinRequests`, `SelfProfile`, `EmojiStatuses`, `Privacy`, `MessageReaders`; файл добавляется в `readFiles`.
- `internal/tgclient/channels_profile_write.go`: `SetChatFlags`, `DecideJoinRequest`, `SetProfile`, `SetPrivacy`; перевод ошибок в своей функции `mapChannelsProfileError` поверх `mapChatAdminError`.
- `internal/tgclient/channels_profile_test.go`.
- `internal/daemon/channels_profile.go`, `internal/daemon/channels_profile_test.go`, `internal/daemon/channels_profile_fake_test.go`: интерфейс `channelsProfileTelegram`, обработчики, барьер `profile`, заглушки `fakeTelegram` в своём файле.
- `internal/cli/channels_profile_cmd.go`, `internal/cli/channels_profile_cmd_test.go`: `chat set`, `chat requests`, `chat approve`, `chat decline`, `account profile`, `account set`, `account emoji-statuses`, `account privacy get|set`, `message readers`; аннотации схемы.
- `.agents/decisions/20261115_HHMM_channel_flags_under_admin_and_profile_writes_under_profile_flag.md` (имя уточняет шаг 6).

Общие файлы, каждая правка одной локальной вставкой (новые строки рядом, без переформатирования соседних):

| Файл | Вставка |
| --- | --- |
| `internal/rpc/api.go` | блок констант методов (`MethodChatSet = "chat.set"`, `MethodChatRequests`, `MethodChatApprove`, `MethodChatDecline`, `MethodAccountProfile`, `MethodAccountSet`, `MethodAccountEmojiStatuses`, `MethodAccountPrivacyGet`, `MethodAccountPrivacySet`, `MethodMessageReaders`) отдельной группой в конце `const`; поле `Profile bool \`json:"profile,omitempty"\`` в `AccessStatus` |
| `internal/access/access.go` | поле `profile bool`, метод `ProfileAllowed()`, `case "profile":` в разборе по образцу `create` |
| `internal/access/access_test.go` или новый `internal/access/profile_test.go` | тесты поля (предпочтительно новый файл) |
| `internal/daemon/daemon.go` | одна строка `d.registerChannelsProfile()` в конце `register()`; `policy.ProfileAllowed()` в условии и в `AccessStatus` статуса |
| `internal/daemon/telegram.go` | одна строка `channelsProfileTelegram` в конце `Telegram` |
| `internal/daemon/access_test.go` | одна новая строка в карте `classes` для десяти методов |
| `internal/tgclient/chat_settings.go` | ключи `antispam` и `signature_profiles` в `channelSettings` |
| `internal/tgclient/chat_admin.go`, `internal/rpc/chat_admin.go` | только если шаг 1 покажет, что `antispam` требует другого права: поле в `ChatOwnAdminRights` и в `ownAdminRights` |
| `internal/tgclient/throttle.go` | строки в `writeRequest`: шесть `*tg.ChannelsToggle*Request` (кроме уже стоящего `SlowMode`), `*tg.MessagesHideChatJoinRequestRequest`, `*tg.AccountUpdateBirthdayRequest`, `*tg.AccountUpdatePersonalChannelRequest`, `*tg.AccountUpdateEmojiStatusRequest`, `*tg.AccountSetPrivacyRequest` |
| `internal/tgclient/readonly_test.go` | `"channels_profile_read.go"` в конец `readFiles` |
| `internal/cli/chat_cmd.go` | одна строка `cmd.AddCommand(a.chatChannelCommands()...)` |
| `internal/cli/account_cmd.go` | одна строка `root.AddCommand(a.accountProfileCommands()...)` |
| `internal/cli/read_cmd.go` | одна строка `cmd.AddCommand(a.messageReadersCommand())` |
| `docs/cli/chats.md` | новый раздел «Channel settings and join requests» с подразделами `tg chat set`, `requests`, `approve`, `decline` |
| `docs/cli/folders-and-account.md` | разделы `tg account profile`, `set`, `emoji-statuses`, `privacy get`, `privacy set` |
| `docs/cli/messages-read.md` | раздел `tg message readers` |
| `docs/cli/access.md` | поле `profile` в «The file» и в «What the Daemon shows» |
| `docs/contracts.md`, `FEATURES.md`, `README.md`, `docs/skill/tg-cli/SKILL.md`, `AGENTS.md`, `.agents/decisions/_TOC.md` | свой абзац или своя строка в шаге 6 |

Изменения общих реестров (полный список):

1. Методы JSON-RPC: десять новых имён в `internal/rpc/api.go`, регистрация в `internal/daemon/daemon.go`.
2. Интерфейс `daemon.Telegram`: встроенный `channelsProfileTelegram`.
3. Классы доступа в `TestRegisteredMethodsHaveAccessClass`: `chat.set`, `chat.requests`, `chat.approve`, `chat.decline`, `message.readers` как `chat-ref`; `account.profile`, `account.emoji_statuses`, `account.privacy_get`, `account.set`, `account.privacy_set` как `none` (свой аккаунт, без чата), барьер `profile` проверяется тестом обработчика.
4. `writeRequest`: одиннадцать запросов выше.
5. `readFiles`: `channels_profile_read.go`.
6. Команды CLI и схема: четыре команды в `chat`, пять в `account`, одна в `message`; `dangerous` только у `account privacy set`.
7. `access.json`: поле `profile`; `daemon status`: `access.profile`.
8. Хранилище и миграции: не меняются.

## Scope

- В плане: `internal/{rpc,access,tgclient,daemon,cli}`, схема `tg schema`, тесты, `docs/cli/`, `docs/contracts.md`, `FEATURES.md`, навык, README, решение, живая проверка чтением и по Q10 записью.
- Вне плана: миграция обычной группы в супергруппу; `hideAllChatJoinRequests`; инвайт-ссылки (7.6.4) и `apply_to_invites`; `guard_bot`; `participants_hidden`, `view_forum_as_messages`, автоперевод; `tabs` и `sidebar` форума как отдельные флаги; правка имени, `username`, био и фото своего профиля (`account.updateProfile`); глобальные настройки приватности; сессии и устройства (16.9); Stars и подарки (17.3); обработка обновлений о смене флагов в живом потоке; `user profile` с днём рождения и личным каналом других людей.
- Затрагиваются: файлы из раздела «Файлы потока».

## Main invariant

1. Без чата в `admin` (и его видимости) ни один запрос `channels.toggle*` и `messages.hideChatJoinRequest` не уходит в Telegram; закрытый чат отвечает как неизвестный.
2. Без `"profile": true` ни один запрос `account.update*` и `account.setPrivacy` не уходит; `account privacy set` без `--yes` и без `--dry-run` не доходит до Daemon.
3. `--dry-run` не вызывает методы записи Telegram и не пишет журнал.
4. Записи не повторяются; FLOOD_WAIT на них не повторяется.
5. Существующие команды и ответы не меняются, кроме двух новых ключей в `settings` у `chat info`.
6. Схема базы не меняется; `internal/cli` не импортирует `tgclient`, gotd, `store`.
7. Ни тест, ни проверка по умолчанию не обращаются к живому Telegram; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: агент меняет приватность и открывает номер телефона или время в сети. Стратегия: барьер `profile`, `--yes`, `--dry-run`, отказ при скрытых правилах без `--replace-hidden`, журнал.
- Риск: `toggleForum(false)` скрывает или ломает топики. Стратегия: шаг 1 выясняет по документации TL и клиентам; если выключение теряет данные, Q4 пересматривается (метка `dangerous` у `chat set` целиком или отдельная команда).
- Риск: два новых ключа в `settings` у `chat info` создают версию в `chat changes` для каждого канала при первом чтении после обновления. Стратегия: шаг 1 проверяет поведение триггера на добавленные ключи (контракт: «заполнение неизвестного значения версию не создаёт»); если версия появляется, шаг 3 пишет ключи только для каналов, где они `true`, или шаг 6 описывает разовую версию.
- Риск: `chat set` с несколькими флагами применит часть и упадёт. Стратегия: фиксированный порядок, частичный успех по общему правилу, ответ называет применённые флаги.
- Риск: старая версия `tg` не понимает поле `profile` и закрывает весь файл. Стратегия: шаг 1 проверяет разбор неизвестных полей; результат в README и в описании поля.
- Риск: слияние с параллельными потоками третьей волны в общих реестрах (`api.go`, `daemon.go`, `telegram.go`, `access_test.go`, `throttle.go`, `readonly_test.go`, `chat_cmd.go`, `account_cmd.go`, `read_cmd.go`, `docs/cli/*.md`, `FEATURES.md`). Стратегия: вставки отдельными строками, список выше, при конфликте сохраняются обе стороны. Поток `dialogs_manage` (16.3 и, вероятно, 16.9) может тоже вставлять команды в `account_cmd.go`; поток `message_extras` в `read_cmd.go` и `writeRequest`.
- Риск: Premium нет у аккаунта, emoji status не проверить вживую. Стратегия: структурный отказ проверяется тестом; вживую только отказ.

## Decision log

| Date | Decision | Reason | Confirmed by |
| --- | --- | --- | --- |
| 2026-11-15 | Флаги канала и решения по заявкам открывает список `admin` (как 7.3 и 7.6), через `adminReference` без изменений; отказ кодом 9 с `data.list: "admin"` | Так задано в задаче потока; барьер уже в `main` | задача менеджера |
| 2026-11-15 | Миграции store нет, номера 0007 и дальше не занимаются | Состояние в Telegram, локально только `people` и `saveChats` | задача менеджера, аудит планировщика |
| 2026-11-15 | `tg chat requests` и `tg message readers` это чтение: нужна только видимость чата | Ничего не меняют; права проверяет Telegram | предложение планировщика |
| 2026-11-15 | `chat info` получает ключи `antispam` и `signature_profiles` | Агент должен видеть результат `chat set` тем же чтением | предложение планировщика |
| 2026-11-15 | Свой профиль читается новой командой `tg account profile`, `tg user profile` не меняется | Чужие дни рождения и личные каналы не попадают в вывод без отдельного решения; файл потока людей не трогается | предложение планировщика |
| 2026-11-15 | Имена читателей берутся из кэша и `people` без дополнительных запросов | Один запрос на вызов, меньше риска FLOOD_WAIT | предложение планировщика |
| 2026-11-15 | Q1. Что разрешает менять свой профиль (`account set`, `account privacy set`): вариант A | A. Цена: поле в `access`, счётчик в `daemon status`, абзац в `access.md`, около 2 часов шага 2; приватность дополнительно защищена `--yes`. B точнее, но второе поле ради одной команды | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q3. Что значит «одобрение заявок» в 16.7: вариант A | A. Цена: три команды и около полудня шагов 3 и 4; массовое решение вне плана, потому что одним вызовом впускает в чат неизвестное число людей | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q4. Какие команды `dangerous` и требуют `--yes`: вариант A | A. Цена: выключение форума и скрытие истории проходят без подтверждения; если шаг 1 покажет, что `forum off` теряет топики, вопрос пересматривается | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q6. Объём правил приватности: вариант A | A. Цена: около дня шагов 2 до 4; B теряет исключения владельца при первой же записи, потому что `setPrivacy` заменяет набор целиком | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q7. Покрывает ли `message readers` личный чат: вариант A | A. Цена: один метод и ветка в обработчике; агент получает один ответ для обоих типов чатов | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q8. Как агент находит `document_id` для emoji status: вариант A | A. Цена: одна команда чтения; без Premium установка всё равно даст код 5 со структурной причиной | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q9. Куда писать журнал изменений настроек: вариант A | A. Цена: около 50 строк, один файл для всех действий агента в чатах и аккаунте | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q10. Что проверять вживую записью (шаг 7): вариант A | A. Решить перед шагом 7. Цена B: записи не проверены на живом аккаунте, в `FEATURES.md` остаются в «не проверено» | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q2. Форма команды флагов канала: вариант A | A. Цена: одна команда и один раздел справочника вместо шести; несколько флагов в одном вызове дают частичный успех, который агент уже умеет читать | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q5. Форма команд профиля: вариант A | A. Цена: та же форма, что у `chat set`; группа `account` уже есть и отвечает за свой аккаунт | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |

## Step 0. Fix decisions before changes

- [ ] Решения Q1 до Q9 записаны в Decision log, контракт поправлен под ответы.
- [ ] Исполнитель Go-кода (Codex Terra) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [x] Ветка `feature/20261115_0439_channels_profile` создана от `main` b8330a8 в worktree `../tg-cli-channels_profile`.

## Step 1. Audit and baseline (исполнитель Codex Terra)

Отчёт в `var/agents/plans/20261115_0439_channels_profile/audit.md` (в git не входит), итог в «Known facts».

- [ ] `just check` и `just build` на чистом дереве ветки; результат в Known facts.
- [ ] gotd v0.166.0: точные сигнатуры и ответы десяти методов записи и шести методов чтения из контракта; что возвращают `Updates` у `channels.toggle*` и нужно ли передать их в `peers.Manager` или в `saveChats`.
- [ ] Типы чатов и права для каждого флага `chat set` (по документации TL, исходникам Telegram Desktop или tdlib из `gh api`); поведение `toggleForum(false)` для существующих топиков; смысл `tabs` и `sidebar`; порог `antispam`.
- [ ] Ошибки Telegram и коды: `CHAT_NOT_MODIFIED`, `CHAT_ADMIN_REQUIRED`, `CHANNEL_INVALID`, `PARTICIPANTS_TOO_FEW`, `PREMIUM_ACCOUNT_REQUIRED`, `BIRTHDAY_INVALID`, `MESSAGE_TOO_OLD`, `USER_PRIVACY_RESTRICTED`, `YOUR_PRIVACY_RESTRICTED`, `HIDE_REQUESTER_MISSING`, `USER_CHANNELS_TOO_MUCH` и прочие найденные; что переводит `mapChatAdminError`, что нужно добавить.
- [ ] Полный список типов `InputPrivacyKey*` и `InputPrivacyValue*` в gotd v0.166.0, соответствие ключам CLI; какие ключи принимают какие правила.
- [ ] Условия `getMessageReadParticipants` и `getOutboxReadDate` (свои сообщения, срок, порог участников, Premium) по TL-документации.
- [ ] Поведение триггера `chat changes` при новых ключах в `settings` (тест на временной базе).
- [ ] Разбор неизвестного поля в `access.json` текущей версией (код `internal/access/access.go`, ветка `default` в разборе).
- [ ] Записать итоги в Known facts, непокрытое в Risks и Open questions.

## Implementation steps

Каждый шаг кода выполняет Codex Terra, проверяет Claude (тесты, дифф, `just check`), коммитит Claude.

- [ ] 2. `rpc` и `access`: `internal/rpc/channels_profile.go` (типы, проверка флагов `on|off|visible|hidden`, даты рождения, ключей и правил приватности, `until`), константы в `api.go`, поле `Profile` в `AccessStatus`; поле `profile` и `ProfileAllowed` в `access`. Тесты: таблица проверок (пустой набор флагов, неверное значение, 29 февраля без года и с невисокосным годом, год вне диапазона, ключ вне списка, `--default` обязателен, противоречивые `allow-bots` и `deny-bots`), разбор `profile` (`true`, `false`, не булево, ошибка файла закрывает).
- [ ] 3. `tgclient`: `channels_profile_read.go` и `channels_profile_write.go`, выбор `InputChannel` по типу, курсор заявок, перевод `ReadParticipantDate`, `OutboxReadDate`, `UserFull` (день рождения, личный канал), `EmojiStatusClass`, правил приватности в типы `rpc` и обратно; ключи `antispam` и `signature_profiles` в `channelSettings`; строки в `writeRequest`; файл в `readFiles`; перевод ошибок. Тесты на подменённом invoker: параметры каждого запроса, `signatures` и `signature-profiles` одним запросом, `tabs` и `sidebar` выключены, `apply_to_invites` не задан, `none` даёт пустые значения, FLOOD_WAIT записи без повтора, `CHAT_NOT_MODIFIED` как `changed: false`, Premium отказ с `reason`.
- [ ] 4. Daemon: `channelsProfileTelegram` и строка в `Telegram`; обработчики десяти методов в `channels_profile.go`: `adminReference` для `chat set`, `approve`, `decline`; `memberReference` для цели заявки; барьер `profile`; видимость личного канала и пользователей и чатов в правилах; `hidden` и `--replace-hidden`; последовательное применение флагов с частичным успехом; журнал `moderation.jsonl`; отбор закрытых людей в `requests` и `readers`; сохранение людей. Заглушки в `channels_profile_fake_test.go`, строка в карте классов, регистрация в `daemon.go`, `access.profile` в статусе. Тесты без сокетов: чат вне `admin` код 9 и ноль вызовов записи; без `profile` код 9 и ноль вызовов; закрытый чат `not_found` до Telegram; неподходящий тип код 2; `--dry-run` без вызова записи и без журнала; частичный успех с `partial` и кодом 1; сбой `intent` журнала отменяет действие; закрытые читатели и заявители убраны.
- [ ] 5. CLI и справочник: `channels_profile_cmd.go`, флаги и проверки до Daemon (хотя бы один флаг, `--yes` у `privacy set`, пределы `--limit`), аннотации (`read`, `account_write`, `dangerous` у `privacy set`, `ItemPath` у `requests`), три строки в `chat_cmd.go`, `account_cmd.go`, `read_cmd.go`; разделы в `docs/cli/chats.md`, `folders-and-account.md`, `messages-read.md`, поле `profile` в `access.md` (раздел на каждую команду и каждый флаг, иначе падает тест справочника); таблицы пересобрать `go test ./internal/cli -run TestDocsGenerated -update-docs`. Тесты по образцу `chat_admin_cmd_test.go`; тест полноты схемы и справочника проходит.
- [ ] 6. Документы через `docwriter` и `glavred` по `docs_chain.md` (механические правки без `glavred`): `docs/contracts.md` (абзац «channel settings, join requests, own profile, readers»), `FEATURES.md` (подпункты 16.7.x, 16.8.x, 18.4.x со следующими свободными номерами, строки статуса, «не проверено на живом аккаунте»), `README.md` (только проверенное), `docs/skill/tg-cli/SKILL.md` (предупреждение про `privacy set` и барьер `profile`), решение в `.agents/decisions/` и строка в `_TOC.md`, `AGENTS.md` (одна строка в Security про `profile`, если решение Q1 принято как A).
- [ ] 7. Живая проверка Claude по Q10, только после явного разрешения владельца в текущей задаче; `access.json` правит только владелец. Без разрешения: `account profile`, `account privacy get last-seen`, `account emoji-statuses`, `chat info` канала (новые ключи), `chat requests` своего канала или код 5 на чужом, `message readers` на своём сообщении в «Браин Тест» (если это группа) и код 2 на канале; `--dry-run` и код 9 у всех записей. С разрешением: по ответу Q10. В отчёт идут только числа и коды, без имён, дат рождения и списков людей.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/rpc/... ./internal/access/... ./internal/tgclient/... ./internal/daemon/... ./internal/cli/...` | зелёный | not_run |
| Без `admin` записи нет | тесты `internal/daemon` | счётчик вызовов записи подмены равен нулю, код 9, `data.list: "admin"` | not_run |
| Без `profile` записи нет | тесты `internal/daemon` | ноль вызовов, код 9, `data.list: "profile"` | not_run |
| `privacy set` без `--yes` | тесты `internal/cli` | код 2, Daemon не вызван | not_run |
| Нет повтора записи на FLOOD_WAIT | тесты `internal/tgclient` | один вызов | not_run |
| Частичный успех | тесты `internal/daemon`, `internal/cli` | `partial: true`, `errors`, код 1 | not_run |
| Только чтение | `TestReadFilesCallNothingThatWrites` | `channels_profile_read.go` без вызовов записи | not_run |
| Классы и схема | `TestRegisteredMethodsHaveAccessClass`, тесты схемы `internal/cli` | у десяти методов класс, у команд класс безопасности | not_run |
| Справочник | `go test ./internal/cli -run 'TestDocs'` | раздел на каждую команду и флаг, ссылки целы, таблицы совпадают | not_run |
| Миграций нет | `git diff --stat main -- internal/store/migrations` | пусто | not_run |
| Живая проверка | шаг 7 | числа и коды по каждой команде | not_run |

## Deploy / rollback

- [ ] Выкладки нет. Откат: откат ветки; схема базы не меняется.
- [ ] После слияния перезапустить Daemon (`tg daemon stop`, `tg daemon start`), чтобы появились новые методы.
- [ ] Если владелец включил `"profile": true`, а версия откатывается, поведение старой версии с этим полем описано по итогам шага 1.

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261115_0439_channels_profile/`.
- [ ] Временные файлы не в git (`/var/` в `.gitignore`).
- [ ] Флаги тестового чата и поля профиля, изменённые в шаге 7, возвращены к исходным значениям (исходные записаны до проверки).

## Definition of done

- [ ] Код изменён только в рамках плана и раздела «Файлы потока».
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.
- [ ] Решение записано в `.agents/decisions/`, `_TOC.md`, `FEATURES.md`, `docs/contracts.md` и `docs/cli/` обновлены.

## Known facts

- `tg schema` на `main` b8330a8: 80 команд; нет ни одной для смены флагов канала, заявок, своего профиля, приватности и читателей сообщения.
- `channelSettings` (`internal/tgclient/chat_settings.go:64`) пишет `no_forwards`, `join_request`, `join_to_send`, `signatures`, `hidden_history`, `default_denied`, `ttl_period`, `slow_mode_seconds`, `linked_chat_id`, `reactions`; `antispam` и `signature_profiles` не пишет.
- `Policy` (`internal/access/access.go:16`) знает `allow`, `block`, `write`, `admin`, `create`, `files`; булево `create` разбирается отдельным `case` и даёт ошибку на не булево значение, образец для `profile`.
- `adminReference(ctx, value, write=false, api)` требует видимость и `admin` и отвечает `ErrAdminDisabled` (код `CodeWriteDisabled`, `data.list: "admin"`); `memberReference` закрывает цель только по `block`.
- Журнал `moderation.jsonl` пишется функцией `d.moderation(record)` с правами 0600 и `Sync`; сбой `intent` отменяет действие.
- `ChatOwnAdminRights` содержит только `creator`, `change_info`, `ban_users`.
- `writeRequest` (`internal/tgclient/throttle.go:76`) содержит `ChannelsToggleSlowModeRequest`, но ни одного другого `channels.toggle*`, `account.update*`, `account.setPrivacy`, `messages.hideChatJoinRequest`.
- gotd v0.166.0: `ChannelsToggleSignaturesRequest{SignaturesEnabled, ProfilesEnabled, Channel}`, `ChannelsToggleAntiSpamRequest{Channel, Enabled}`, `ChannelsToggleForumRequest{Channel, Enabled, Tabs, Sidebar}`, `ChannelsTogglePreHistoryHiddenRequest{Channel, Enabled}`, `ChannelsToggleJoinRequestRequest{ApplyToInvites, Channel, Enabled, GuardBot}`, `ChannelsToggleJoinToSendRequest{Channel, Enabled}`, `AccountUpdateEmojiStatus(EmojiStatusClass)`, `AccountUpdateBirthdayRequest{Birthday}`, `AccountUpdatePersonalChannel(InputChannelClass)`, `AccountGetPrivacy(key)` и `AccountSetPrivacyRequest{Key, Rules}` (ответ `AccountPrivacyRules{Rules, Chats, Users}`), `MessagesGetMessageReadParticipantsRequest{Peer, MsgID}` (ответ `[]ReadParticipantDate{UserID, Date}`), `MessagesGetOutboxReadDateRequest{Peer, MsgID}`, `MessagesGetChatInviteImportersRequest{Requested, Peer, Link, Q, OffsetDate, OffsetUser, Limit}`, `MessagesHideChatJoinRequestRequest{Approved, Peer, UserID}`, `AccountGetDefaultEmojiStatuses(hash)`, `AccountGetRecentEmojiStatuses(hash)`; `ChannelFull.Antispam`, `ChannelFull.HiddenPrehistory`, `Channel.SignatureProfiles`, `Channel.Forum`, `UserFull.Birthday`, `UserFull.PersonalChannelID`, `User.EmojiStatus`; 17 типов `InputPrivacyKey*` (вместе с `Box` и срезами).
- Группа `tg account` собирается в `internal/cli/account_cmd.go:19` (`root.AddCommand(sessions, blocked, notify)`); группа `chat` в `chat_cmd.go:40-41`; `message` в `read_cmd.go:106-109`.
- Worktree соседних потоков третьей волны на момент планирования: `../tg-cli-dialogs_manage`, `../tg-cli-media_export`, `../tg-cli-message_extras` (все от b8330a8, планов в них ещё нет).

## Open questions

Владелец делегировал решения менеджеру; менеджер принимает рекомендуемый вариант (первый) и пишет его в Decision log. Каждый вопрос отдельной строкой.

| # | Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| Q1 | Что разрешает менять свой профиль (`account set`, `account privacy set`) | Барьер записи; списки чатов к профилю не подходят | A: новое булево `"profile": true` в `access.json`, по умолчанию выключено, отказ кодом 9 с `data.list: "profile"`; B: два поля, `profile` для дня рождения, канала и статуса и `privacy` для правил приватности; C: id своего аккаунта в `write` (заодно открывает запись в Saved Messages); D: без списка, только `--dry-run` и `--yes` | A. Цена: поле в `access`, счётчик в `daemon status`, абзац в `access.md`, около 2 часов шага 2; приватность дополнительно защищена `--yes`. B точнее, но второе поле ради одной команды | принято |
| Q2 | Форма команды флагов канала | Имена попадают в схему, навык и справочник | A: одна `tg chat set <chat>` с флагами `--signatures`, `--antispam` и так далее, флаги по одному запросу, частичный успех по общему правилу; B: команда на флаг (`chat signatures <chat> on`, `chat antispam ...`, шесть команд); C: `tg chat toggle <chat> <flag> on|off` | A. Цена: одна команда и один раздел справочника вместо шести; несколько флагов в одном вызове дают частичный успех, который агент уже умеет читать | принято |
| Q3 | Что значит «одобрение заявок» в 16.7 | Определяет объём: флаг или ещё и решения по людям | A: флаг `--join-requests` в `chat set` и команды `chat requests` (список), `chat approve`, `chat decline` по одному человеку; B: только флаг; C: A плюс одобрение и отклонение всех сразу | A. Цена: три команды и около полудня шагов 3 и 4; массовое решение вне плана, потому что одним вызовом впускает в чат неизвестное число людей | принято |
| Q4 | Какие команды `dangerous` и требуют `--yes` | Защита от случайного действия | A: только `account privacy set` (заменяет весь набор правил ключа и может открыть номер или время в сети); флаги канала и заявки без `--yes`, они обратимы той же командой; B: ещё `chat set` целиком; C: ещё `chat decline` | A. Цена: выключение форума и скрытие истории проходят без подтверждения; если шаг 1 покажет, что `forum off` теряет топики, вопрос пересматривается | принято |
| Q5 | Форма команд профиля | Имена в схеме и навыке | A: `tg account profile` (чтение), `tg account set` с флагами `--birthday`, `--personal-channel`, `--emoji-status`, `tg account emoji-statuses`, `tg account privacy get|set`; B: команда на поле (`account birthday set|clear`, `account emoji-status set|clear`, `account personal-channel set|clear`); C: новая группа `tg profile` | A. Цена: та же форма, что у `chat set`; группа `account` уже есть и отвечает за свой аккаунт | принято |
| Q6 | Объём правил приватности | Чем шире, тем больше кода и риск открыть данные | A: все ключи gotd, правила `--default`, пользователи и чаты списками, `premium`, `bots`, `close-friends`; пользователи и чаты в правилах проходят списки доступа, при скрытых id `set` требует `--replace-hidden`; B: только `--default` без исключений; C: только чтение правил | A. Цена: около дня шагов 2 до 4; B теряет исключения владельца при первой же записи, потому что `setPrivacy` заменяет набор целиком | принято |
| Q7 | Покрывает ли `message readers` личный чат | В личном чате другой метод и другие условия | A: да, через `getOutboxReadDate`, ответ тем же списком из одного читателя; B: только группы и супергруппы | A. Цена: один метод и ветка в обработчике; агент получает один ответ для обоих типов чатов | принято |
| Q8 | Как агент находит `document_id` для emoji status | Без списка агент не знает, что ставить | A: команда `tg account emoji-statuses [--recent]` (стандартные и недавние статусы); B: только ставить по известному id; C: A плюс поиск по стикерам (вне плана, это 16.5) | A. Цена: одна команда чтения; без Premium установка всё равно даст код 5 со структурной причиной | принято |
| Q9 | Куда писать журнал изменений настроек | Владелец должен видеть, что агент поменял в канале и профиле | A: тот же `moderation.jsonl` с `action` `set`, `approve`, `decline`, `account.set`, `privacy.set`; без значений даты рождения и без списков людей; B: отдельный `account.jsonl` для профиля; C: без журнала | A. Цена: около 50 строк, один файл для всех действий агента в чатах и аккаунте | принято |
| Q10 | Что проверять вживую записью (шаг 7) | Флаги и профиль меняют реальный аккаунт; без явной цели записи нет | A: тестовая супергруппа и тестовый канал владельца (созданные им или `chat create` при `create: true`), их id владелец сам вносит в `admin`, плюс `account set --birthday` и возврат значения при временном `"profile": true`; `privacy set` только `--dry-run`; B: только чтение, `--dry-run` и коды 9 | A. Решить перед шагом 7. Цена B: записи не проверены на живом аккаунте, в `FEATURES.md` остаются в «не проверено» | принято |
