# Пошаговый план: управление диалогами, папками, Saved Messages и сессиями

ID: 20261115_0439_dialogs_manage
Created: 2026-11-15
Owner: Мязин Евгений
Branch: feature/20261115_0439_dialogs_manage (создана заранее в worktree `../tg-cli-dialogs_manage` от `main` b8330a8, код не менялся)
PR: —

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: draft
- Last update: 2026-11-15
- Next safe step: менеджер принимает рекомендуемые варианты Q1 до Q7 (владелец делегировал решения) и записывает их в Decision log; план переходит в `approved`; Codex Terra начинает шаг 1 (аудит) в worktree `../tg-cli-dialogs_manage`. Q8 задаётся перед шагом 8.

## Context and sources

- Read: `AGENTS.md`, `INTENT.md`, `FEATURES.md` (16.1, 16.2, 16.3, 16.9, 10.6, 10.10, 4.6, 5.3, 9.2, статус), `.agents/rules/plans.md`, `.agents/rules/project/AGENTS.md`, план-образец `20261114_2244_users_contacts.md`, слитый план `20261114_2244_folders_account_state.md` (вопрос В3 о полях сессий, «Вне плана»: все записи), `docs/contracts.md` (запись сообщений, администрирование чатов, пакетные записи), `docs/cli/chats.md` (раздел «Chat administration»), `docs/cli/folders-and-account.md`, `docs/cli/access.md` (оглавление).
- Код: `internal/access/access.go` (поля `allow`, `block`, `write`, `admin`, `create`, `files`; неизвестное поле закрывает всё), `internal/daemon/account_guard.go` (`accountMethod`), `internal/daemon/chat_admin.go` (`adminReference`, журнал `moderation.jsonl`, `--dry-run`), `internal/daemon/users.go` (`changeBlock`), `internal/daemon/owner_state.go`, `internal/daemon/daemon.go` (`register`, статус доступа), `internal/daemon/access_test.go` (карта `classes`), `internal/rpc/owner_state.go` (`Session.Hash` с `json:"-"`), `internal/rpc/batch.go`, `internal/tgclient/folders.go`, `internal/tgclient/owner_state.go`, `internal/tgclient/throttle.go` (`writeRequest`), `internal/tgclient/readonly_test.go` (`readFiles`, `writeVerbs`), `internal/chatref/chatref.go` (`parseLinkPath`), `internal/cli/folder_cmd.go`, `internal/cli/account_cmd.go`, `internal/cli/chat_state_cmd.go`, `internal/cli/spec.go` (`noRisk`, `dangerous`), `internal/cli/cli.go` (корень команд), миграции `0001` до `0006`.
- Проверено командами: `go build` текущего `main` и `tg schema` собранного бинаря (78 команд, список ниже в Known facts); `grep` по `internal` на методы gotd записи диалогов, папок, Saved Messages и сессий: ни одного вызова; наличие методов в gotd v0.166.0 (`grep "func (c *Client) <Method>("` в `$(go list -m -f '{{.Dir}}' github.com/gotd/td)/tg`): все 27 нужных методов есть.
- Установленный в системе `tg` это старая утилита `@miolamio/tg-cli` на gramjs: на `tg account ...` она отвечает `unknown command 'account'`. Схему нашей утилиты смотреть только по собранному `dist/tg` (`just build`).
- Not verified: тесты и `just check` не запускались (это шаг 1). Не проверено: сбрасывает ли `account.updateNotifySettings` поля, которых нет в запросе; как Telegram отвечает на удаление папки, у которой есть пригласительные ссылки; работает ли `message history` и `message react` по своему id (Saved Messages); какой peer у «скрытого автора» в `messages.getSavedDialogs`; точные пределы папок и закреплений без Premium. Всё это пункты аудита.

## Goal

Агент архивирует, закрепляет и мьютит диалоги, меняет уведомления чата; создаёт, правит, упорядочивает и удаляет папки чатов, смотрит общую папку по ссылке без вступления; читает Saved Messages по подчатам и тегам, ищет по тегу и переименовывает тег; завершает чужую сессию аккаунта. Каждая запись проходит барьер владельца в `access.json`, поддерживает `--dry-run`, необратимые записи помечены `dangerous` и требуют `--yes`.

## Problem and contract

Тип плана: product (новые команды записи в аккаунт, два новых поля `access.json`, новое поле в ответе `account sessions`).

### Что уже есть (по коду и `tg schema`, 2026-11-15)

| Пункт | Уже есть | Где |
| --- | --- | --- |
| 16.1 чтение папок | `tg folder list [--local]`: правила, порядок, закреплённые, включённые и исключённые чаты; снимок в базе | `internal/cli/folder_cmd.go`, `internal/tgclient/folders.go` (`foldersAPI`, `convertFolders`), `internal/daemon/owner_state.go`, `internal/store/folders.go`, миграция `0005_folders_account_state.sql` |
| 16.3 чтение состояния | `tg chat state [chat] [--local]`: `pinned`, `archived`, `mute_until`, `silent`, `show_previews`; `tg account notify` (настройки по умолчанию); `tg chat unread --archived` | `internal/cli/chat_state_cmd.go`, `internal/tgclient/owner_state.go` (`ChatStates`, `notifyDefaultsAPI`), таблицы `dialog_state`, `notify_defaults` |
| 16.9 список сессий | `tg account sessions [--local]`; `hash` сессии хранится в `account_sessions`, но в ответ не выводится (решение В3 потока 0005) | `internal/cli/account_cmd.go`, `internal/tgclient/owner_state.go` (`sessionsAPI`), `internal/rpc/owner_state.go` (`Session.Hash json:"-"`) |
| 16.2 Saved Messages | Предположительно читаются как обычный чат по своему id (`message history <свой id>`), тег ставится существующим `message react <свой id> <id> <emoji>`. Не проверено, проверяет шаг 1 | `internal/daemon/read.go`, `internal/daemon/write.go` |

Записей нет ни одной: в коде нет вызовов `FoldersEditPeerFolders`, `MessagesToggleDialogPin`, `AccountUpdateNotifySettings`, `MessagesUpdateDialogFilter`, `MessagesUpdateDialogFiltersOrder`, `ChatlistsCheckChatlistInvite`, `MessagesGetSavedDialogs`, `MessagesGetSavedHistory`, `MessagesGetSavedReactionTags`, `MessagesUpdateSavedReactionTag`, `AccountResetAuthorization`. План делает только недостающее.

### Контракт

Записан для рекомендованных вариантов Q1 до Q7; другой ответ меняет отмеченные места.

Барьеры (Q1, Q2):

- Новое булево поле `dialogs` в `<profile>/access.json` (Q1). При `dialogs: true` разрешены архив, закрепление, мьют и уведомления диалога и все записи папок для любого видимого чата; `write` для этого не нужен. Без поля или при `false` такие команды дают код 9 `write_disabled` с подсказкой про `dialogs`, запроса в Telegram нет. Закрытый чат (вне `allow` или в `block`) даёт `not_found`, как неизвестный.
- Новое булево поле `sessions` в `access.json` (Q2). Без `sessions: true` завершение сессии даёт код 9 с подсказкой про `sessions`.
- Записи Saved Messages (переименование тега) идут под существующим `write`: свой id должен стоять в `write`, как у `message react` в Saved Messages.
- `daemon status` показывает `dialogs` и `sessions` в блоке `access` (булевы, `omitempty`, как `create`).
- Поля читает только человек; команды, которая пишет `access.json`, нет.

Диалоги, 16.3 (группа `chat`, класс `account_write`, не `dangerous`, без `--yes`, всё обратимо):

- `tg chat archive <chat>... [--dry-run]` и `tg chat unarchive <chat>... [--dry-run]`: до 100 чатов, все проверяются до Telegram (ссылка, видимость, `dialogs`); при первой ошибке запрос не уходит. Один запрос `folders.editPeerFolders` (папка 1 архив, 0 основной список). Ответ `{chats: [{chat_id, archived}], dry_run?}`.
- `tg chat pin <chat> [--dry-run]` и `tg chat unpin <chat> [--dry-run]`: `messages.toggleDialogPin`; закрепление в той папке Telegram, где лежит диалог (основной список или архив). Ответ `{chat_id, pinned}`. Предел закреплений даёт код 5 с понятным текстом (`PINNED_DIALOGS_TOO_MUCH`).
- `tg chat mute <chat> [--for DURATION | --until RFC3339] [--dry-run]` и `tg chat unmute <chat> [--dry-run]`: без флага срока мьют бессрочный (`mute_until = 2147483647`); `--for` и `--until` взаимоисключающие, время в прошлом даёт код 2.
- `tg chat notify <chat> [--silent=true|false] [--previews=true|false] [--dry-run]`: нужен хотя бы один флаг, иначе код 2.
- `mute`, `unmute`, `notify` читают текущие настройки (`account.getNotifySettings`) и отправляют `account.updateNotifySettings` с изменённым полем и сохранёнными остальными. Ответ `{chat_id, mute_until, silent, show_previews}`.
- После записи Daemon перечитывает состояние затронутых чатов одним `messages.getPeerDialogs` и сохраняет в `dialog_state`; ошибка перечитывания пишется в журнал и не ломает ответ.
- Мьют темы форума, глобальные настройки по областям и сброс всех настроек не делаются (Q5).

Папки, 16.1 (группа `folder`):

- `tg folder create <title> [--emoticon E] [--color N] [--include CHAT]... [--exclude CHAT]... [--pin CHAT]... [--rules LIST] [--dry-run]` (класс `account_write`, не `dangerous`): `LIST` из `contacts,non-contacts,groups,channels,bots,exclude-muted,exclude-read,exclude-archived`. Нужен хотя бы один включающий источник (правило или чат), иначе код 2 до Telegram. Id новой папки: наименьший свободный от 2 по свежему `messages.getDialogFilters`. Запрос `messages.updateDialogFilter`. Ответ `{id, title_length, include, exclude, pinned}` (числа чатов).
- `tg folder edit <id> [--title T] [--emoticon E] [--color N] [--add CHAT]... [--remove CHAT]... [--exclude CHAT]... [--unexclude CHAT]... [--pin CHAT]... [--unpin CHAT]... [--rules LIST] [--dry-run]` (не `dangerous`): чтение, правка, запись по свежему `messages.getDialogFilters`. Меняются только названные поля; `--rules` заменяет набор правил, `--rules ""` очищает. Чаты папки, которые агенту не видны (закрытые или неизвестные), сохраняются как есть и не раскрываются; ответ показывает только их число `hidden`.
- `tg folder delete <id> [--yes] [--dry-run]` (`dangerous`): без `--yes` и без `--dry-run` код 2, Daemon не вызывается. Перед запросом Daemon пишет в журнал `account.jsonl` фазу `intent` с полным определением папки (id, название, правила, id чатов), после ответа фазу `result`. Так владелец может воссоздать папку. Ответ `{id, deleted: true}`.
- `tg folder order <id>...` (не `dangerous`): `messages.updateDialogFiltersOrder`; список должен совпасть с набором существующих папок вида `filter` и `chatlist`, иначе код 2.
- `tg folder invite-info <link>` (класс `read`): `chatlists.checkChatlistInvite` по ссылке `t.me/addlist/<slug>`; вступления нет (Q6). Ответ `{title, emoticon, already: bool, folder_id?, chats: [{id, type, title, members?, joined}]}`; названия `untrusted`. Непустой `allow` закрывает команду так же, как `chat invite-info` (`InviteAllowed`); чаты из `block` убираются из ответа.
- Папки вида `chatlist` (общие, в которые владелец вступил) и псевдопапка `all` не правятся и не удаляются: код 2 с объяснением.
- После каждой записи Daemon перечитывает папки (`messages.getDialogFilters`) и обновляет снимок в базе тем же путём, что `folder list`.
- Барьер: `dialogs: true`; каждый добавляемый, исключаемый или закрепляемый чат должен быть видимым.

Saved Messages, 16.2 (новая группа `saved`, Q4):

- Saved Messages видны, только если свой id виден по `allow` и `block`; иначе все команды группы дают `not_found`.
- `tg saved list [--limit N] [--cursor C]` (`read`): `messages.getSavedDialogs`; элементы `{peer_id, type, title, top_message, date, pinned}`, `title` `untrusted`; подчаты закрытых источников убираются.
- `tg saved history <peer> [--limit N] [--cursor C]` (`read`): `messages.getSavedHistory`; сообщения в формате `message history`, сохраняются в базу как сообщения своего чата.
- `tg saved tags [--peer P]` (`read`): `messages.getSavedReactionTags`; элементы `{emoji | custom_emoji_id, title, count}`.
- `tg saved search [query] [--tag EMOJI] [--peer P] [--limit N] [--cursor C]` (`read`): `messages.search` по своему чату с `saved_reaction` и `saved_peer_id`; нужен запрос или тег.
- `tg saved tag-rename <emoji> (--title T | --clear) [--dry-run]` (`account_write`, не `dangerous`): `messages.updateSavedReactionTag`; без Premium Telegram отвечает ошибкой, код 5 с понятным текстом.
- Пометка сообщения тегом: существующий `tg message react <свой id> <id> <emoji>` (проверяет шаг 1; если не работает, план правится).
- Удаление и закрепление подчатов не делаются (Q4).

Сессии, 16.9 (группа `account`):

- `tg account sessions` получает поле `id` (Q3): 16 шестнадцатеричных символов SHA-256 от `hash` сессии; считается при выводе и для `--local` из сохранённого `hash`. Сам `hash` по-прежнему не выводится.
- `tg account terminate <id> [--yes] [--dry-run]` (`account_write`, `dangerous`, Q4 вариант A): без `--yes` и без `--dry-run` код 2. Daemon читает свежий `account.getAuthorizations`, находит сессию по `id` (нет такой: код 7), отказывает для текущей сессии (код 2, своя сессия не завершается этой командой), проверяет `sessions: true` (код 9). `--dry-run` возвращает `{dry_run, action: "terminate", id, device_model, platform, app_name, date_active}` (поля `untrusted`). Без `--dry-run`: запись `intent` в `account.jsonl` (id, `device_model`, `app_name`, `date_active`), `account.resetAuthorization(hash)`, запись `result`, перечитывание списка сессий в базу. Ответ `{id, terminated: true}`. `FRESH_RESET_AUTHORISATION_FORBIDDEN` (сессия Daemon моложе суток) даёт код 5 с объяснением.
- Завершение всех остальных сессий, срок жизни неактивных сессий, подтверждение незнакомого входа и веб-сессии не делаются (Q4).

Общее:

- Записи не повторяются: новые запросы добавляются в `writeRequest` дросселя, FLOOD_WAIT даёт код 6.
- `--dry-run` проходит все проверки Daemon, включая барьер, и не вызывает методы записи Telegram, не пишет в базу и в журнал. Чтения для проверки (`getDialogFilters`, `getAuthorizations`, `getNotifySettings`) при `--dry-run` разрешены.
- `--expect-user` охватывает новые методы: префиксы `folder.`, `saved.`, `account.` добавляются в `accountMethod`.
- Журнал `<profile>/account.jsonl`, права 0600, по строке JSON на фазу: `time`, `account`, `tool: "tg"`, `phase` (`intent`, `result`), `action` (`folder_delete`, `session_terminate`), данные действия, `outcome`; ошибка записи `intent` останавливает действие.
- RPC: `chat.archive`, `chat.unarchive`, `chat.pin_dialog`, `chat.unpin_dialog`, `chat.mute`, `chat.unmute`, `chat.notify`, `folder.create`, `folder.edit`, `folder.delete`, `folder.order`, `folder.invite_info`, `saved.list`, `saved.history`, `saved.tags`, `saved.search`, `saved.tag_rename`, `account.terminate`.
- Миграция не нужна: пишутся существующие `dialog_state`, `folders`, `folder_chats`, `account_sessions`. Номер `0008` закреплён за потоком и берётся, только если аудит найдёт нужду (например, хранить подчаты Saved Messages для `--local`).
- Найденная ошибка (по коду, `internal/chatref/chatref.go`, `parseLinkPath`): ссылка `t.me/addlist/<slug>` сейчас разбирается как `@addlist`, и `chat join t.me/addlist/...` пойдёт в чужой публичный чат. Поток исправляет: `parseLinkPath` отклоняет путь `addlist/` кодом 2 с подсказкой про `folder invite-info`.

Предлагаемые подпункты FEATURES (вписывает шаг 7):

- 16.1.1 создание папки с правилами и чатами; 16.1.2 правка папки без потери невидимых агенту чатов; 16.1.3 удаление папки с `--yes` и журналом; 16.1.4 порядок папок; 16.1.5 просмотр общей папки по ссылке без вступления.
- 16.2.1 список подчатов Saved Messages; 16.2.2 история подчата; 16.2.3 список тегов; 16.2.4 поиск по тегу и подчату; 16.2.5 переименование тега; 16.2.6 тег ставится через `message react`.
- 16.3.1 архив и возврат из архива до 100 чатов; 16.3.2 закрепление диалога; 16.3.3 мьют на срок, до времени и бессрочно; 16.3.4 флаги уведомлений чата без потери остальных настроек; 16.3.5 барьер `dialogs`.
- 16.9.1 `id` сессии в списке; 16.9.2 завершение чужой сессии с `--yes`, барьером `sessions` и журналом; 16.9.3 текущая сессия не завершается.

## Файлы потока

Новые файлы, которыми владеет поток:

- `internal/rpc/dialogs.go`, `internal/rpc/dialogs_test.go`: константы методов (своим блоком, `api.go` не трогается), типы параметров и ответов, проверка параметров (пределы, `--rules`, срок мьюта, id сессии).
- `internal/chatref/chatlist.go`, `internal/chatref/chatlist_test.go`: `ParseChatlist(s) (slug string, err error)` для `t.me/addlist/<slug>`.
- `internal/tgclient/dialogs_write.go` (архив, закрепление, мьют, уведомления), `internal/tgclient/folders_write.go` (создание, правка, удаление, порядок), `internal/tgclient/chatlist_read.go` (`checkChatlistInvite`), `internal/tgclient/saved_read.go`, `internal/tgclient/saved_write.go`, `internal/tgclient/sessions_write.go`, тесты `*_test.go` к каждому.
- `internal/daemon/dialogs.go`, `internal/daemon/folders_write.go`, `internal/daemon/saved.go`, `internal/daemon/sessions.go`, `internal/daemon/account_journal.go`, тесты к каждому и `internal/daemon/dialogs_fake_test.go` (заглушки `fakeTelegram` для нового интерфейса).
- `internal/cli/dialogs_cmd.go`, `internal/cli/folder_write_cmd.go`, `internal/cli/saved_cmd.go`, `internal/cli/sessions_cmd.go`, тесты к каждому.
- `docs/cli/saved.md`.
- `.agents/decisions/20261115_HHMM_dialog_and_session_writes_need_owner_switches_in_access_json.md` (имя уточняет шаг 7).

Общие файлы, каждая правка одной локальной вставкой (новые строки рядом, без переформатирования соседних):

| Файл | Вставка |
| --- | --- |
| `internal/access/access.go` | поля `dialogs`, `sessions` в `Policy`; два `case` в `parse` (булевы, как `create`); методы `DialogsAllowed()`, `SessionsAllowed()` |
| `internal/access/access_test.go` | случаи разбора двух полей |
| `internal/rpc/api.go` | поля `Dialogs bool`, `Sessions bool` (`omitempty`) в `AccessStatus` |
| `internal/rpc/owner_state.go` | поле `ID string json:"id"` в `Session` |
| `internal/chatref/chatref.go` | один `case` в `parseLinkPath`: путь `addlist/` даёт ошибку с подсказкой |
| `internal/daemon/daemon.go` | одна строка `d.registerDialogs()` в конце `register()`; условие и поля `Dialogs`, `Sessions` в построении `st.Access` |
| `internal/daemon/telegram.go` | одна строка `dialogsTelegram` (встроенный интерфейс) в конце `Telegram` |
| `internal/daemon/account_guard.go` | префиксы `folder.`, `saved.`, `account.` в `accountMethod` |
| `internal/daemon/owner_state.go` | заполнение `Session.ID` из `hash` при выводе (одна функция-помощник в `sessions.go`, здесь один вызов) |
| `internal/daemon/access_test.go` | одна строка в карте `classes` для 18 методов |
| `internal/tgclient/throttle.go` | одна строка в `writeRequest`: `*tg.FoldersEditPeerFoldersRequest, *tg.MessagesToggleDialogPinRequest, *tg.AccountUpdateNotifySettingsRequest, *tg.MessagesUpdateDialogFilterRequest, *tg.MessagesUpdateDialogFiltersOrderRequest, *tg.MessagesUpdateSavedReactionTagRequest, *tg.AccountResetAuthorizationRequest` |
| `internal/tgclient/readonly_test.go` | `"chatlist_read.go", "saved_read.go"` в конец `readFiles` |
| `internal/cli/cli.go` | одна строка `a.savedCommand()` в `root.AddCommand` |
| `internal/cli/chat_cmd.go` | одна строка добавления команд из `dialogs_cmd.go` |
| `internal/cli/folder_cmd.go` | одна строка добавления команд из `folder_write_cmd.go`; `Short` группы «Read chat folders» меняется на «Read and manage chat folders» |
| `internal/cli/account_cmd.go` | одна строка добавления `terminate`; `Short` группы меняется на «Read account state and manage sessions» |
| `docs/cli/chats.md`, `docs/cli/folders-and-account.md`, `docs/cli/access.md`, `docs/cli/overview.md`, `docs/README.md` | новые разделы и ссылка на `saved.md` |
| `docs/contracts.md`, `FEATURES.md`, `README.md`, `docs/skill/tg-cli/SKILL.md`, `AGENTS.md`, `.agents/decisions/_TOC.md` | абзац или строка на тему потока |

Миграций нет; `0008` зарезервирован (см. Decision log).

## Scope

- В плане: 16.1 (запись папок, просмотр общей папки), 16.2 (чтение Saved Messages по подчатам и тегам, поиск по тегу, переименование тега), 16.3 (архив, закрепление диалога, мьют, уведомления чата), 16.9 (`id` сессии, завершение одной чужой сессии); два поля `access.json`; журнал `account.jsonl`; исправление разбора `t.me/addlist`; документы и справочник `docs/cli`; живая проверка.
- Вне плана: вступление в общую папку и выход из неё, создание пригласительных ссылок на папку (Q6); мьют темы форума, глобальные настройки уведомлений, `account.resetNotifySettings`, исключения уведомлений (Q5); удаление и закрепление подчатов Saved Messages, порядок закреплённых подчатов (Q4); завершение всех остальных сессий, срок жизни сессий, подтверждение незнакомого входа, веб-сессии (Q4); выход из своей сессии (`auth logout`); порядок закреплённых диалогов (`messages.reorderPinnedDialogs`); пометка «непрочитано» (`messages.markDialogUnread`); теги папок (`toggleDialogFilterTags`) и рекомендованные папки; живое обновление `dialog_state` и папок событиями потока; псевдоним `me` для своего чата.
- Затрагиваются только файлы раздела «Файлы потока».

## Main invariant

1. Без барьера владельца (`dialogs`, `sessions` или свой id в `write`) ни один запрос записи из этого плана не уходит в Telegram: счётчик вызовов записи подмены равен нулю, код 9.
2. Закрытый чат ведёт себя как неизвестный (`not_found`) во всех новых командах; в `saved list` и `folder invite-info` закрытые убраны до ответа и не сохраняются.
3. `folder edit` меняет только названные поля: невидимые агенту чаты и неупомянутые правила папки остаются байт в байт (по `tg.DialogFilter` до и после в тесте).
4. `chat mute|unmute|notify` меняют только своё поле; остальные настройки уведомлений чата сохраняются.
5. Текущая сессия Daemon не завершается ни одной командой плана.
6. Записи не повторяются; FLOOD_WAIT на них не повторяется.
7. `--dry-run` не вызывает методов записи Telegram, не пишет в базу и в журнал.
8. Существующие команды и их ответы не меняются, кроме нового поля `id` в `account sessions` и полей `dialogs`, `sessions` в `daemon status`; файл `access.json` без новых полей читается как раньше.
9. `internal/cli` не импортирует `tgclient`, gotd, `store`; тесты не обращаются к живому Telegram; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: `account.updateNotifySettings` трактует отсутствующие поля как «по умолчанию» и сбрасывает их (предположение). Стратегия: чтение, правка, запись через `account.getNotifySettings`; тест на сохранение полей; шаг 1 уточняет по TL-схеме.
- Риск: `folder edit` теряет невидимые чаты или `access_hash` при пересборке `InputPeer`. Стратегия: правка поверх свежего `tg.DialogFilter` из `getDialogFilters` с исходными `InputPeer`; новые чаты разрешаются через кэш пиров; тест инварианта 3.
- Риск: гонка с правкой папки владельцем на телефоне: последняя запись выигрывает. Стратегия: чтение непосредственно перед записью; описать в `docs/cli`.
- Риск: агент завершает сессию телефона владельца. Стратегия: отдельное поле `sessions`, `dangerous`, `--yes`, `--dry-run` с устройством и приложением, журнал, запрет текущей сессии. Откат: владелец входит заново на устройстве.
- Риск: `FRESH_RESET_AUTHORISATION_FORBIDDEN`, пределы папок (`DIALOG_FILTERS_TOO_MUCH`, `FILTER_INCLUDE_TOO_MUCH`), закреплений (`PINNED_DIALOGS_TOO_MUCH`), Premium (`PREMIUM_ACCOUNT_REQUIRED`). Стратегия: перевод в код 5 с понятным текстом; шаг 1 составляет таблицу ошибок.
- Риск: мьют скрывает от владельца важный чат (вред тихий, не сразу виден). Стратегия: барьер `dialogs`, ответ с `mute_until`, `chat state` показывает мьют; отдельный вопрос Q1 объясняет цену.
- Риск: разбор `t.me/addlist/` как `@addlist` (уже в `main`). Стратегия: исправление в шаге 2 с тестом.
- Риск: конфликты слияния с параллельными потоками третьей волны в общих файлах. Самые вероятные: `internal/access/access.go` (другой поток может добавить своё поле, например для профиля 16.8), `internal/cli/account_cmd.go` (настройки приватности 16.8 могут лечь в группу `account`), `internal/cli/chat_cmd.go`, `internal/tgclient/throttle.go`, `internal/daemon/access_test.go`, `internal/daemon/telegram.go`. Стратегия: вставки отдельными строками по таблице «Файлы потока», при конфликте сохраняются обе стороны; номера миграций не нужны.
- Риск: раннер миграций требует номеров подряд. Стратегия: поток миграцию не добавляет; если аудит потребует, берётся `0008` только после слияния `0007` в `main`.

## Decision log

| Date | Decision | Reason | Confirmed by |
| --- | --- | --- | --- |
| 2026-11-15 | Необратимые записи (`folder delete`, `account terminate`) `dangerous` с `--yes`; архив, закрепление, мьют, уведомления, создание и правка папки, порядок, переименование тега без `--yes` | Указание владельца в задаче: `--yes` там, где необратимо; остальное откатывается обратной командой | задача владельца, оформлено планировщиком |
| 2026-11-15 | Миграции нет, `0008` зарезервирован за потоком | Нужные таблицы уже есть в `0005`; номера подряд обязательны | предложено планировщиком |
| 2026-11-15 | Журнал необратимых записей аккаунта в новом файле `account.jsonl` того же формата, что `moderation.jsonl`; `intent` папки содержит полное определение | Восстановление удалённой папки и след завершённой сессии; `chat_admin.go` не трогается | предложено планировщиком |
| 2026-11-15 | `archive`/`unarchive` принимают до 100 чатов одним запросом, все проверяются до Telegram, частичного успеха нет | `folders.editPeerFolders` атомарен, частичный ответ невозможен | предложено планировщиком |
| 2026-11-15 | Переименование тега Saved Messages идёт под `write` со своим id | Saved Messages это чат владельца, так уже работает `message react` | предложено планировщиком |
| 2026-11-15 | Общие папки вида `chatlist` и псевдопапка `all` не правятся и не удаляются | Удаление общей папки это выход из чатов (`chatlists.leaveChatlist`), он вне плана (Q6) | предложено планировщиком |
| 2026-11-15 | Разбор `t.me/addlist/` в `chatref.Parse` даёт ошибку | Сейчас ссылка ведёт на публичный `@addlist`, это ошибка безопасности | предложено планировщиком |
| 2026-11-15 | Q1. Что разрешает архив, закрепление, мьют и уведомления диалога и запись папок: вариант A | A. Цена: правка `internal/access` и документа о доступе; агент с `dialogs: true` может замьютить или убрать в архив любой видимый чат, и владелец пропустит уведомления. Цена B: чтобы разложить 50 кана | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q2. Что разрешает завершать сессии: вариант A | A. Цена: ещё одно поле в `access.json`; без него команда всегда код 9 | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q3. Как агент указывает сессию для завершения: вариант A | A. Цена: одна функция и сопоставление по свежему `getAuthorizations` перед завершением; В3 не меняется. Цена C: номер сдвигается, когда кто-то входит или выходит | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q4. Объём записей 16.2 и 16.9: вариант A | A. Цена: все остальные сессии агент завершает по одной; закрепить подчат Saved Messages нельзя. B опасен: одна команда выкидывает телефон и компьютер владельца | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q5. Объём уведомлений 16.3: вариант A | A. Цена: глобальные настройки владелец меняет сам в приложении; темы форума мьютятся только целым чатом | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q6. Что делать с общей папкой по ссылке: вариант A | A. Цена: вступать в чаты общей папки агент будет по одному через `chat join` | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q8. Что проверять записью вживую (шаг 8): вариант A | A. Задать перед шагом 8. Цена B: запись не проверена на живом аккаунте, все пункты останутся «не проверено» | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-15 | Q7. Как назвать команды: вариант A | A. Цена: `chat pin` (диалог) рядом с `message pin` (сообщение), различие объясняет справочник | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |

## Step 0. Fix decisions before changes

- [ ] Ответы на Q1 до Q7 записаны в Decision log, контракт поправлен под ответы.
- [ ] Исполнитель Go-кода (Codex Terra) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [x] Ветка `feature/20261115_0439_dialogs_manage` создана от `main` b8330a8 в worktree `../tg-cli-dialogs_manage`.

## Step 1. Audit and baseline (исполнитель Codex Terra)

Отчёт в `var/agents/plans/20261115_0439_dialogs_manage/audit.md` (в git не входит), итог в «Known facts».

- [ ] `just check` и `just build` на чистом дереве ветки; результат в Known facts.
- [ ] gotd v0.166.0: точные сигнатуры и ответы `folders.editPeerFolders`, `messages.toggleDialogPin`, `account.getNotifySettings`, `account.updateNotifySettings` (`InputPeerNotifySettings`: что значит отсутствующее поле), `messages.getDialogFilters`, `messages.updateDialogFilter`, `messages.updateDialogFiltersOrder`, `chatlists.checkChatlistInvite` (`ChatlistInvite` и `ChatlistInviteAlready`), `messages.getSavedDialogs`, `messages.getSavedHistory`, `messages.getSavedReactionTags`, `messages.search` (`saved_peer_id`, `saved_reaction`), `messages.updateSavedReactionTag`, `account.getAuthorizations`, `account.resetAuthorization`; какие из них возвращают `Updates` и нужно ли применять их к менеджеру обновлений.
- [ ] Таблица ошибок Telegram и кодов выхода: `PINNED_DIALOGS_TOO_MUCH`, `DIALOG_FILTERS_TOO_MUCH`, `FILTER_INCLUDE_TOO_MUCH`, `FILTER_INCLUDE_EMPTY`, `FILTER_TITLE_EMPTY`, `FILTER_ID_INVALID`, `INVITE_SLUG_EXPIRED`, `INVITE_SLUG_EMPTY`, `PREMIUM_ACCOUNT_REQUIRED`, `FRESH_RESET_AUTHORISATION_FORBIDDEN`, `HASH_INVALID`, `PEER_ID_INVALID`; где их переводит `mapError`.
- [ ] Пределы без Premium: число папок, чатов в папке, закреплённых диалогов, длина названия папки, допустимые `color`.
- [ ] Saved Messages: работает ли `message history <свой id>` и `message react <свой id> ...` (по коду разрешения ссылок и кэшу пиров); как выглядит peer скрытого автора в `getSavedDialogs`; нужен ли псевдоним `me` (если без него своё чат не разрешается, записать в Open questions).
- [ ] Удаление папки с пригласительными ссылками: что делает Telegram (по документации TL и исходникам клиентов, без живого вызова).
- [ ] Место в `daemon.go`, где строится `st.Access`; как `owner_state.go` выдаёт `Session` из базы (чтобы посчитать `id` для `--local`).
- [ ] Подтвердить, что схема базы достаточна; если нет, записать какие колонки и почему (тогда миграция `0008`).
- [ ] Записать итоги в Known facts, непокрытое в Risks и Open questions.

## Implementation steps

Каждый шаг кода выполняет Codex Terra, проверяет Claude (тесты, дифф, `just check`), коммитит Claude.

- [ ] 2. Основа: поля `dialogs`, `sessions` в `internal/access` с тестами разбора; поля статуса в `rpc.AccessStatus` и `daemon.go`; `internal/rpc/dialogs.go` (константы 18 методов, типы, проверка параметров) с тестами; `chatref.ParseChatlist` и отказ `addlist/` в `parseLinkPath` с тестами; префиксы в `accountMethod`; строка в карте `classes`; интерфейс `dialogsTelegram` и строка в `Telegram`, заглушки в `dialogs_fake_test.go`; запросы в `writeRequest`; файлы в `readFiles`. Проверка: `just check`.
- [ ] 3. Диалоги 16.3: `tgclient/dialogs_write.go` (архив, закрепление, мьют, уведомления с чтением текущих настроек, перечитывание состояния), `daemon/dialogs.go` (ссылка, видимость, `dialogs`, `--dry-run`, сохранение `dialog_state`), `cli/dialogs_cmd.go` (семь команд, взаимоисключение `--for`/`--until`, хотя бы один флаг у `notify`, до 100 чатов). Тесты: код 9 без `dialogs` и ноль вызовов записи, `not_found` закрытого, инвариант 4, отсутствие повтора на FLOOD_WAIT, `--dry-run` без записи.
- [ ] 4. Папки 16.1: `tgclient/folders_write.go` и `tgclient/chatlist_read.go`, `daemon/folders_write.go` и `daemon/account_journal.go`, `cli/folder_write_cmd.go` (пять команд). Тесты: инвариант 3 (невидимые чаты и правила байт в байт), выбор свободного id, отказ для `chatlist` и `all`, `--yes` у `delete`, журнал `intent` до запроса и остановка при ошибке журнала, `folder order` с неполным набором даёт код 2, `invite-info` при непустом `allow` закрыт и без `block`-чатов.
- [ ] 5. Saved Messages 16.2: `tgclient/saved_read.go`, `tgclient/saved_write.go`, `daemon/saved.go`, `cli/saved_cmd.go` (группа `saved`, пять команд, строка в `cli.go`). Тесты: свой чат закрыт, все команды `not_found`; закрытые источники убраны из `saved list`; `saved history` сохраняет сообщения; `tag-rename` без своего id в `write` даёт код 9; поиск без запроса и тега даёт код 2.
- [ ] 6. Сессии 16.9: `Session.ID` (SHA-256 от `hash`, 16 hex) в выводе и `--local`; `tgclient/sessions_write.go`, `daemon/sessions.go`, `cli/sessions_cmd.go` (`account terminate`). Тесты: текущая сессия код 2, неизвестный `id` код 7, без `sessions` код 9 и ноль вызовов, `--yes` обязателен, журнал до и после, `FRESH_RESET_AUTHORISATION_FORBIDDEN` код 5.
- [ ] 7. Документы по `docs_chain.md` (`docwriter`, проверка `glavred`): `docs/cli/chats.md` (раздел «Dialog organization»), `docs/cli/folders-and-account.md` (пять команд папок, `id` сессии, `account terminate`), новый `docs/cli/saved.md` и ссылки в `docs/README.md` и `overview.md`, `docs/cli/access.md` (поля `dialogs`, `sessions`), `docs/contracts.md` (абзац потока), `FEATURES.md` (подпункты 16.1, 16.2, 16.3, 16.9 из контракта, строки статуса и «не проверено»), `README.md`, `docs/skill/tg-cli/SKILL.md` (барьеры, `--yes`, предупреждение про мьют и завершение сессий), `AGENTS.md` (Running: одна строка про `dialogs` и `sessions`), решение и `_TOC.md`. Таблицы флагов пересобирает Codex Terra: `go test ./internal/cli -run TestDocsGenerated -update-docs`; тест полноты справочника требует раздела на каждую команду и флаг. Механические правки на `glavred` не идут.
- [ ] 8. Живая проверка Claude по ответу Q8. Чтение (разрешено правилами, с паузами между запросами): `folder list`, `chat state` нескольких чатов, `account sessions` (есть `id`), `saved list`, `saved history` одного подчата, `saved tags`, `saved search --tag`, `folder invite-info` по ссылке, которую даст владелец (без ссылки пропустить с записью причины). Отказы без записи: код 9 без `dialogs` и `sessions`, `--dry-run` всех записей, `account terminate` текущей сессии (код 2). Записи только по Q8.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/access/... ./internal/chatref/... ./internal/rpc/... ./internal/tgclient/... ./internal/daemon/... ./internal/cli/...` | зелёный | not_run |
| Без барьера запроса нет | тесты `internal/daemon` | счётчик вызовов записи подмены равен нулю, код 9 | not_run |
| Закрытый чат | тесты `internal/daemon` | `not_found`, нет в `saved list` и `invite-info`, не сохранён | not_run |
| Невидимые чаты папки целы | тест `folder edit` | `tg.DialogFilter` до и после совпадает вне названных полей | not_run |
| Уведомления не сбрасываются | тест `chat mute` | неизменённые поля `InputPeerNotifySettings` равны прочитанным | not_run |
| Текущая сессия | тест `account terminate` | код 2, вызова `resetAuthorization` нет | not_run |
| Нет повтора записи | тесты `internal/tgclient` | один вызов на FLOOD_WAIT | not_run |
| Только чтение | `go test ./internal/tgclient -run TestReadFilesCallNothingThatWrites` | `chatlist_read.go`, `saved_read.go` без вызовов записи | not_run |
| `addlist` не разбирается как username | `go test ./internal/chatref` | `Parse("t.me/addlist/x")` возвращает ошибку | not_run |
| Полнота схемы и справочника | `go test ./internal/cli -run 'TestDocs'` | разделы на 18 новых команд и все флаги, ссылки целы | not_run |
| Классы методов | `go test ./internal/daemon -run TestRegisteredMethodsHaveAccessClass` | у 18 методов есть класс | not_run |
| Миграций нет | `git diff --stat main -- internal/store/migrations` | пусто | not_run |
| Живая проверка | шаг 8 | числами: папки, состояния, сессии, подчаты, коды отказов | not_run |

## Deploy / rollback

- [ ] Выкладки нет. После слияния перезапустить Daemon (`tg daemon stop`, `tg daemon start`).
- [ ] Владелец добавляет `"dialogs": true` и, если нужно, `"sessions": true` в `access.json` вручную; без них новые записи выключены.
- [ ] Откат: откат ветки. Схема базы не меняется. Внимание: `access.json` с полями `dialogs` или `sessions` на старой версии закроет все чаты (неизвестное поле), поэтому при откате владелец убирает эти поля.

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261115_0439_dialogs_manage/`.
- [ ] Временные файлы не в git.
- [ ] Изменения живой проверки возвращены: тестовая папка удалена, «Браин Тест» разархивирован, откреплён, размьючен; временные правки `access.json` владелец вернул.

## Definition of done

- [ ] Код изменён только в рамках плана и раздела «Файлы потока».
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.
- [ ] Решение записано в `.agents/decisions/`, `_TOC.md`, `FEATURES.md`, `AGENTS.md` и справочник обновлены.

## Known facts

- `tg schema` на `main` b8330a8: 78 команд. Записи диалогов, папок, Saved Messages и сессий нет; чтение есть: `folder list`, `chat state`, `account sessions|blocked|notify`, `chat unread --archived`. Ближайшие образцы записи: `chat join|leave|kick|ban` (`account_write`, `leave`, `kick`, `ban` `dangerous`), `user block`, `chat mark-read` (запись под `write`).
- `access.json` знает поля `allow`, `block`, `write`, `admin`, `create` (булево), `files`; неизвестное поле закрывает все чаты (`parse`, `default: unknown field`).
- `daemon status` показывает `access` при `Enabled() || WriteCount() > 0 || AdminCount() > 0 || CreateAllowed()` (`daemon.go:376`).
- `accountMethod` (`account_guard.go`) охватывает `search`, `subscribe`, `unsubscribe` и префиксы `chat.`, `message.`, `note.`, `history_sync.`; `folder.` и `account.` не охвачены.
- `Session.Hash` хранится в `account_sessions.hash` и не выводится (`json:"-"`).
- `writeRequest` (`throttle.go:76`) не содержит запросов этого плана.
- `readonly_test.go` считает записью вызовы с глаголами `Toggle`, `Update`, `Reorder`, `Set`, `Mark`, `Terminate` и др.; поэтому запись живёт в `*_write.go`.
- `chatref.parseLinkPath` разбирает `t.me/addlist/<slug>` как `@addlist` (код прочитан, тестом не подтверждено).
- В gotd v0.166.0 есть все нужные методы: `FoldersEditPeerFolders`, `MessagesToggleDialogPin`, `AccountGetNotifySettings`, `AccountUpdateNotifySettings`, `MessagesUpdateDialogFilter`, `MessagesUpdateDialogFiltersOrder`, `ChatlistsCheckChatlistInvite`, `MessagesGetSavedDialogs`, `MessagesGetSavedHistory`, `MessagesGetSavedReactionTags`, `MessagesUpdateSavedReactionTag`, `AccountResetAuthorization`, а также не используемые планом `AuthResetAuthorizations`, `AccountSetAuthorizationTTL`, `ChatlistsJoinChatlistInvite`, `MessagesDeleteSavedHistory`, `MessagesToggleSavedDialogPin`.
- Параллельно в третьей волне от того же `main` открыты ветки `feature/20261115_0439_channels_profile`, `..._message_extras`, `..._media_export`; их планов на момент написания нет.

## Open questions

Каждый вопрос задаётся отдельно. Рекомендуемый вариант первый. Владелец делегировал решения менеджеру; менеджер принимает рекомендуемые.

| # | Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| Q1 | Что разрешает архив, закрепление, мьют и уведомления диалога и запись папок | Определяет проверку до Telegram и правку `internal/access` | A: новое булево поле `dialogs` в `access.json`, действует на любой видимый чат; B: чат в `write`, как у `chat mark-read` (папка целиком и её чаты тоже по `write`); C: новый список `dialogs: [id...]` | A. Цена: правка `internal/access` и документа о доступе; агент с `dialogs: true` может замьютить или убрать в архив любой видимый чат, и владелец пропустит уведомления. Цена B: чтобы разложить 50 каналов по папкам, владелец открывает в них и отправку сообщений | принято |
| Q2 | Что разрешает завершать сессии | Ошибка выкидывает владельца с его устройства | A: отдельное булево поле `sessions` плюс `--yes`, `dangerous` и журнал; B: только `--yes` и `dangerous`; C: то же поле, что в Q1 | A. Цена: ещё одно поле в `access.json`; без него команда всегда код 9 | принято |
| Q3 | Как агент указывает сессию для завершения | `hash` сессии сейчас не выводится (решение В3 потока папок) | A: поле `id`: 16 hex-символов SHA-256 от `hash`, Daemon сопоставляет по свежему списку; B: выводить сам `hash` Telegram (пересмотр В3, нужна запись решения); C: номер строки в списке | A. Цена: одна функция и сопоставление по свежему `getAuthorizations` перед завершением; В3 не меняется. Цена C: номер сдвигается, когда кто-то входит или выходит | принято |
| Q4 | Объём записей 16.2 и 16.9 | Определяет число команд и рисков | A: 16.9 только завершение одной чужой сессии; 16.2 только переименование тега (тег ставит `message react`); B: плюс `account terminate --all-others` (`auth.resetAuthorizations`) и закрепление подчата Saved Messages; C: плюс срок жизни сессий, веб-сессии, удаление подчата | A. Цена: все остальные сессии агент завершает по одной; закрепить подчат Saved Messages нельзя. B опасен: одна команда выкидывает телефон и компьютер владельца | принято |
| Q5 | Объём уведомлений 16.3 | Глобальные настройки меняют поведение всех чатов сразу | A: мьют и флаги `silent`, `previews` одного чата; B: плюс глобальные настройки по областям (личные, группы, каналы); C: плюс мьют темы форума | A. Цена: глобальные настройки владелец меняет сам в приложении; темы форума мьютятся только целым чатом | принято |
| Q6 | Что делать с общей папкой по ссылке | Вступление по ссылке вступает сразу во многие чаты | A: только просмотр (`folder invite-info`); B: плюс вступление (`chatlists.joinChatlistInvite`) под `dialogs` и `--yes`; C: плюс выход из общей папки и создание ссылок на свою | A. Цена: вступать в чаты общей папки агент будет по одному через `chat join` | принято |
| Q7 | Как назвать команды | Имена попадают в схему, справочник и навык | A: `chat archive\|unarchive\|pin\|unpin\|mute\|unmute\|notify`, `folder create\|edit\|delete\|order\|invite-info`, группа `saved list\|history\|tags\|search\|tag-rename`, `account terminate`; B: всё про диалог под новой группой `dialog` (`dialog archive`, `dialog pin` ...); C: Saved Messages под `chat saved ...` | A. Цена: `chat pin` (диалог) рядом с `message pin` (сообщение), различие объясняет справочник | принято |
| Q8 | Что проверять записью вживую (шаг 8) | Записи меняют реальный аккаунт; без явной цели записи нет | A: архив, закрепление, мьют, уведомления на «Браин Тест» с возвратом; временная папка с «Браин Тест», созданная и удалённая в проверке; переименование тега на своём аккаунте (если есть Premium); завершение сессии только временного второго профиля `tg`, вход в который владелец сделает сам; B: только `--dry-run` и коды отказов | A. Задать перед шагом 8. Цена B: запись не проверена на живом аккаунте, все пункты останутся «не проверено» | принято |
