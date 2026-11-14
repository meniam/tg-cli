# Пошаговый план: папки и состояние аккаунта владельца в локальной базе

ID: 20261114_2244_folders_account_state
Created: 2026-11-14
Owner: Мязин Евгений
Branch: feature/20261114_2244_folders_account_state (создана заранее в worktree `../tg-cli-folders_account_state` от `main` 8e388be, код не менялся)
PR: —

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: draft
- Last update: 2026-11-14
- Next safe step: менеджер задаёт владельцу открытые вопросы В1 до В9 по одному; после ответов план переходит в `approved`, Codex начинает шаг 1 (аудит).

## Context and sources

- Read: `AGENTS.md`, `INTENT.md`, `FEATURES.md` (10.5, 10.6, 10.10, 4.6, 4.9, 7.5, 16.1, 16.3, 16.9, статус), `.agents/rules/plans.md`, `.agents/rules/project/contracts.md` (оглавление пунктов), план-образец `20261114_1933_reactions_pins_polls.md`, черновик плана соседнего потока `../tg-cli-sched/.agents/plans/20261114_2232_scheduled_drafts_typing.md` (В5 о черновиках, правило миграции 0007).
- Код: `internal/store/migrations/0002_schema.sql` (таблицы `folders`, `folder_chats`, `account_state`), `internal/store/account_state.go`, `internal/store/store.go` (`listMigrations`), `internal/store/schema_test.go`, `internal/daemon/daemon.go` (`register`, `chatList`), `internal/daemon/telegram.go` (интерфейс `Telegram`), `internal/daemon/chat_lists.go` (`listChat`), `internal/daemon/persist.go`, `internal/daemon/access.go`, `internal/rpc/api.go`, `internal/tgclient/client.go` (`chats`), `internal/tgclient/convert.go` (`toChat`), `internal/tgclient/updates_events.go` (диспетчер), `internal/tgclient/update_state.go` (`StateKV`), `internal/tgclient/throttle.go` (`classifyRequest`, `writeRequest`), `internal/cli/cli.go` (корень команд), `internal/cli/chat_cmd.go`, `internal/cli/spec.go` (классы безопасности).
- gotd v0.166.0 в `/Users/eugene/go/pkg/mod/github.com/gotd/td@v0.166.0/tg`: `MessagesGetDialogFilters`, `MessagesDialogFilters`, `DialogFilter`, `DialogFilterChatlist`, `DialogFilterDefault`, `Dialog`, `PeerNotifySettings`, `MessagesGetPeerDialogsRequest`, `MessagesPeerDialogs`, `AccountGetAuthorizationsRequest`, `AccountAuthorizations`, `Authorization`, `ContactsGetBlockedRequest`, `ContactsBlocked`, `ContactsBlockedSlice`, `PeerBlocked`, `AccountGetNotifySettingsRequest`, `tl_input_notify_peer_gen.go`, обновления `UpdateDialogFilter`, `UpdateDialogFilterOrder`, `UpdateDialogFilters`, `UpdatePeerBlocked`, `UpdateNotifySettings`, `UpdateReadHistoryInbox|Outbox`, `UpdateReadChannelInbox|Outbox`, `UpdateDialogUnreadMark`, `UpdateDialogPinned`, `UpdatePinnedDialogs`, `UpdateFolderPeers`, `UpdateNewAuthorization`.
- Verified by commands: `rg 'folders|folder_chats' internal` (таблицы пишет только `schema_test.go`, код их не трогает); `git worktree list` (соседние потоки `tg-cli-idem`, `tg-cli-users_contacts`, `tg-cli-sched`, `tg-cli-chat_admin`); `grep -n blocked /opt/homebrew/lib/node_modules/@miolamio/tg-cli/README.md` (у старой утилиты команда `tg user blocked [--limit N]`).
- Not verified: реальный ответ `getDialogFilters` на аккаунте владельца (число папок, есть ли общие папки `chatlist`), сколько диалогов и заблокированных у владельца, нужен ли `InputPeerSelf` в папках, как часто Telegram даёт FLOOD_WAIT на `account.getAuthorizations` и `contacts.getBlocked`. Это проверяет шаг 1 по исходникам и шаг 7 вживую.

## Goal

Daemon по команде агента читает из Telegram папки, состояние диалогов (маркеры прочитанного, счётчики, мьют, архив, закрепление), список сессий, список заблокированных и общие настройки уведомлений, сохраняет их в локальную базу и отдаёт агенту; те же данные читаются из базы без Telegram флагом `--local`. Записи в аккаунт нет.

## Problem and contract

Тип плана: product (новые команды чтения и новые таблицы).

FEATURES 10.6 и 10.10 записаны как `[Must]`, но таблицы `folders` и `folder_chats` созданы пустыми, а состояния аккаунта в базе нет: таблица `account_state` это ключ-значение для курсоров потока обновлений (`updates.state.*`, `events.high_water`), её не трогаем. Агент сегодня видит только `unread` в `chat list` и не знает, в каких папках чат, что замьючено, какие сессии открыты и кто заблокирован.

Контракт ниже записан с рекомендованными ответами на В1 до В9; другой ответ меняет отмеченный пункт.

- `tg folder list [--local]` (метод `folder.list`, класс `read`): папки в порядке Telegram, в том числе позиция «Все чаты» (`DialogFilterDefault`). Поля папки: `id`, `kind` (`filter`, `chatlist`, `all`), `position`, `title`, `emoticon`, `color`, правила (`contacts`, `non_contacts`, `groups`, `broadcasts`, `bots`, `exclude_muted`, `exclude_read`, `exclude_archived`), списки `pinned`, `include`, `exclude` из Bot API id чатов (название берётся из базы, если чат там есть), `synced_at`. Фактический состав по правилам не вычисляется (В2).
- `tg chat state <чат> [--local]` и `tg chat state --limit N [--local]` (метод `chat.state`, класс `read`, В1): состояние диалога из `messages.getPeerDialogs` для одного чата или из `messages.getDialogs` для первых N (от 1 до 500, по умолчанию 100). Поля: `chat_id`, `read_inbox_max_id`, `read_outbox_max_id`, `unread`, `unread_mentions`, `unread_reactions`, `unread_mark`, `pinned`, `archived` (`folder_id == 1`), `top_message`, `ttl_period`, `mute_until`, `silent`, `show_previews`, `synced_at`. Черновик не входит (В4).
- `tg account sessions [--local]` (метод `account.sessions`, класс `read`): сессии из `account.getAuthorizations`: `current`, `official_app`, `password_pending`, `unconfirmed`, `device_model`, `platform`, `system_version`, `api_id`, `app_name`, `app_version`, `date_created`, `date_active`, `country`, `region` (поле `ip` по В3), плюс `authorization_ttl_days`. `hash` сессии хранится в базе как ключ строки и в ответ не выводится.
- `tg account blocked [--limit N] [--cursor C] [--local]` (метод `account.blocked`, класс `read`, В5): заблокированные из `contacts.getBlocked` страницами (`--limit` от 1 до 1000, по умолчанию 100): `peer_id` (Bot API id), `type`, `title`, `username`, `date`, `next_cursor`.
- `tg account notify [--local]` (метод `account.notify`, класс `read`, В8): общие настройки уведомлений для личных чатов, групп и каналов (`account.getNotifySettings` с `InputNotifyUsers`, `InputNotifyChats`, `InputNotifyBroadcasts`): `scope`, `mute_until`, `silent`, `show_previews`, `synced_at`. Настройки отдельного чата отдаёт `chat state`.
- Режимы чтения (В1): без `--local` команда идёт в Telegram, сохраняет ответ в базу и отдаёт его; с `--local` читает только базу, работает без входа и без соединения (как `search` и `note`), а пустая база даёт пустой список и `synced_at: null`, не ошибку. Живого обновления базы событиями потока нет (В6), свежесть видна по `synced_at`.
- Запись в базу: снимок заменяет прежний целиком в одной транзакции для папок, сессий, общих настроек и полного списка заблокированных (папка или сессия, исчезнувшая у Telegram, исчезает из базы); состояние диалога обновляется построчно (upsert); неполная страница заблокированных делает upsert без удаления.
- Списки доступа (решение 6 и FEATURES 4.9): закрытый чат в `chat state <чат>` даёт `not_found` до обращения к Telegram и после ответа; в `chat state --limit`, `folder list` (списки `pinned`, `include`, `exclude`) и `account blocked` закрытые чаты отбрасываются до ответа и в базу не пишутся; число отброшенных не выводится. Режим `--local` проходит тот же фильтр.
- Недоверенный текст: `title` папки (общую папку `chatlist` называет чужой человек), `device_model`, `platform`, `system_version`, `app_name`, `app_version` сессии (их задаёт чужое приложение), `title` заблокированного помечены `untrusted` в `tg schema`.
- Журнал Daemon без параметров и данных ответа (действует `Server.call`). IP и названия устройств в журнал не попадают.
- Запросы идут через общий дроссель как чтение; в `writeRequest` ничего не добавляется. FLOOD_WAIT дольше порога возвращается кодом 6.
- Коды выхода: без входа код 4 (кроме `--local`), закрытый или неизвестный чат 7, неверные флаги 2, `--local` вместе с `--cursor` код 2.
- Релиз одним слоем; откат: откат ветки. Миграция 0006 только добавляет столбцы и таблицы, откат ветки после применения миграции на живой базе оставляет лишние таблицы без вреда, но `store.Open` старой сборки откажет на `user_version` 6 (это уже правило `store.Open`).

## Scope

- В плане: миграция `0006`, методы хранилища, `rpc` (методы и типы), `tgclient` (пять чтений), Daemon (обработчики, фильтр доступа, сохранение, `--local`), CLI (`folder list`, `chat state`, `account sessions|blocked|notify`), схема, тесты, документы, живая проверка только чтением.
- Вне плана: любые записи (создание и правка папок, блокировка и разблокировка 7.5.2, завершение сессий 16.9, мьют и архив 16.3), вычисление фактического состава папки по правилам (В2), черновики 10.10.2 (В4), живое обновление базы событиями потока (В6), исключения уведомлений `account.getNotifyExceptions` (В8), маркеры прочитанного по темам форума, папка «Архив» как отдельная сущность (она видна как `archived` у диалога), настройки звуков и историй в уведомлениях (только в `raw`), автоматическая синхронизация при старте Daemon (В1).
- Затрагиваются: `internal/{store,rpc,tgclient,daemon,cli}`, `AGENTS.md`, `FEATURES.md`, `README.md`, `docs/skill/tg-cli/`, `.agents/rules/project/contracts.md`, `.agents/decisions/`.

## Main invariant

1. Ни один запрос записи в Telegram не уходит: план добавляет только методы `get*`; тест проверяет, что подмена Telegram получила только запросы чтения.
2. Закрытый чат не виден ни в ответе, ни в базе; `chat state` закрытого чата отвечает как неизвестный.
3. Таблица `account_state` и ключи потока обновлений не меняются; существующие команды и их ответы не меняются.
4. Миграция 0006 только добавляет (`ALTER TABLE ... ADD COLUMN`, `CREATE TABLE`), ничего не удаляет; номера 0004, 0005, 0007 не заняты этим потоком.
5. `internal/cli` не импортирует `tgclient`, gotd, `store`.
6. Ни тест, ни проверка по умолчанию не обращаются к живому Telegram; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: `listMigrations` требует номера подряд с 1, а 0004 и 0005 ещё не в `main`; ветка с одним 0006 ломает `store.Open` и `just check`. Стратегия по В9.
- Риск: прогон Daemon из ветки с временными номерами на живом профиле поднимет `user_version`, и настоящие 0004 и 0005 потом не применятся никогда. Стратегия: живая проверка (шаг 7) только после перебазирования на `main`, где есть 0004 и 0005, и удаления временных файлов; перед живой проверкой Claude сверяет `ls internal/store/migrations` и `PRAGMA user_version` профиля.
- Риск: `InputPeer` в папках несут `access_hash`. Стратегия: в базу и `raw` идут только Bot API id; `raw` папки собирается без `access_hash`; тест на отсутствие `access_hash` в сохранённом JSON.
- Риск: `getDialogFilters` не возвращает сущности, у чатов папки нет названий. Стратегия: названия из базы, неизвестный чат выводится только с id; кэш пиров этим методом не пополняется.
- Риск: `chat state --limit 500` листает диалоги и тратит лимит запросов. Стратегия: пачки по 100 через тот же дроссель, предел 500, как у `chat list`.
- Риск: сессии раскрывают IP и место входа. Стратегия по В3; журнал без данных ответа.
- Риск: пересечение с потоками `users_contacts` (заблокированные, `tg user blocked` у старой утилиты) и `scheduled_drafts_typing` (черновики). Стратегия по В4 и В5, итог согласует менеджер до шага 2.
- Риск: все пять потоков правят интерфейс `Telegram`, `register`, корень CLI и фейки тестов. Стратегия: раздел «Файлы потока», одна вставка на общий файл, методы фейков в своих тестовых файлах.

## Decision log

| Date | Decision | Reason | Confirmed by |
| --- | --- | --- | --- |
| 2026-11-14 | За потоком зарезервирована миграция store 0006; 0004, 0005, 0007 заняты другими потоками | Номера идут подряд и не должны совпасть | задание менеджера |
| 2026-11-14 | Только чтение и хранение; записи в аккаунт нет | Условие задания | задание менеджера |
| 2026-11-14 | `account_state` (ключ-значение потока обновлений) не используется для 10.10, данные идут в новые таблицы | Таблицу пишет менеджер обновлений gotd, смешение ключей опасно | предложено планировщиком, подтверждается на ревью шага 2 |
| 2026-11-14 | `folders` и `folder_chats` расширяются `ALTER TABLE ADD COLUMN`, а не пересоздаются | Код их не пишет, но `schema_test.go` проверяет текущие столбцы; добавление проще слить и откатить | предложено планировщиком, подтверждается на ревью шага 2 |
| 2026-11-14 | Закрытые чаты отбрасываются из ответов и базы, `chat state` закрытого чата это `not_found` | Прецедент решения 6 и FEATURES 4.9, `chat common` | правило проекта |
| 2026-11-14 | Название папки, строки сессии и название заблокированного помечены `untrusted` | Их задают чужие люди и приложения | предложено планировщиком по правилу решения 6 |
| 2026-11-14 | В1. Когда Daemon получает папки и состояние: Б) А плюс `chat state --limit N` без чата: состояние первых N диалогов своей итерацией `getDialogs`, без правки общих файлов: плюс около 3 часов. | Б: 10.10.1 закрыт для всех чатов, общие файлы не трогаются | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | В2. Что хранить как состав папки (10.6.1): А) правила папки (контакты, группы, каналы, боты, исключения) и явные списки `pinned`, `include`, `exclude`: дёшево. | А: так хранит Telegram, агент видит правила | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | В3. Какие поля сессий хранить и показывать (10.10.4): Б) без `ip`, страна и регион остаются: ничего не стоит. | Б: агент видит устройство, приложение и время, а адрес входа остаётся у владельца | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | В4. Чей пункт 10.10.2 (черновики в базе): А) черновики хранит поток 0007 вместе со своими командами `chat draft`; этот план их не трогает: 0. | А: номер 0007 зарезервирован под «отложенные и черновики», один владелец у черновиков | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | В5. Чей пункт 10.10.3 (заблокированные): А) этот план: таблица `blocked_peers` и `tg account blocked`; `users_contacts` делает только блокировку и разблокировку, обновление строки после записи отдельной правкой после слияния: около 3 часов здесь. | А: 10.10.3 записан в 10.10, таблица идёт в 0006; согласовать с менеджером | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | В6. Обновлять ли базу событиями потока: А) нет, данные обновляет команда, свежесть по `synced_at`: 0. | А: отдельной задачей после слияния всех потоков | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | В7. Имена команд: А) `tg folder list`, `tg chat state`, `tg account sessions|blocked|notify` | папки это отдельная сущность, состояние относится к чату, остальное к аккаунту | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | В8. Объём настроек уведомлений (10.10.5): А) настройки чата в `chat state` плюс три общих (личные, группы, каналы): 3 запроса. | Меняет число запросов | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | В9. Как разрабатывать миграцию 0006, пока 0004 и 0005 не в `main`: Б) в ветке временные пустые `0004_placeholder_do_not_merge.sql` и `0005_placeholder_do_not_merge.sql`; при перебазировании настоящие файлы дают дубль версии, и `listMigrations` падает, пока заглушки не удалены; живой запуск только после удаления: 0, нужна дисц | Б: поток не ждёт, ошибка заглушек ловится сама; живая проверка строго после перебазирования | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |

## Step 0. Fix decisions before changes

- [ ] Владелец ответил на В1 до В9 (менеджер задаёт по одному), ответы записаны в Decision log, контракт поправлен.
- [ ] Менеджер согласовал с потоками `users_contacts` и `scheduled_drafts_typing` границу по заблокированным и черновикам (В4, В5).
- [ ] Исполнитель Go-кода (Codex Terra) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [ ] Ветка `feature/20261114_2244_folders_account_state` в `../tg-cli-folders_account_state` начинается от текущего `main`; если `main` ушёл вперёд, ветка перебазируется до шага 1.

## Step 1. Audit and baseline

Исполнитель: Codex Terra. Код не меняется. Отчёт в `var/agents/plans/20261114_2244_folders_account_state/audit.md` (вне git).

- [ ] `just check` и `just build` на чистом дереве; результат в Known facts.
- [ ] Папки: `MessagesGetDialogFilters` возвращает `*MessagesDialogFilters{TagsEnabled, Filters}`; порядок `Filters` и место `DialogFilterDefault`; поля `DialogFilter` и `DialogFilterChatlist`; `Title` как `TextWithEntities` (берём `Text`); перевод `InputPeerUser|Chat|Channel|Self|*FromMessage` в Bot API id; как получить id владельца для `InputPeerSelf`.
- [ ] Диалоги: `MessagesGetPeerDialogsRequest{Peers []InputDialogPeerClass}` с `InputDialogPeer`; поля `tg.Dialog` (маркеры, счётчики, `UnreadMark`, `Pinned`, `FolderID`, `NotifySettings`, `TTLPeriod`, `TopMessage`); `DialogFolder` в ответе `getDialogs` пропускается; можно ли переиспользовать итерацию `query.GetDialogs` из `tgclient/client.go` без правки общего файла (своя функция в своём файле).
- [ ] Сессии: `AccountGetAuthorizations` и поля `Authorization`, `AuthorizationTTLDays`.
- [ ] Заблокированные: `ContactsGetBlockedRequest{Offset, Limit}`, ответы `ContactsBlocked` и `ContactsBlockedSlice{Count}`, предел `Limit` у сервера, перевод `PeerBlocked.PeerID` в Bot API id, пополнение кэша пиров из `Users` и `Chats` (как `m.Apply` в `chats`).
- [ ] Уведомления: `AccountGetNotifySettings` с `InputNotifyUsers`, `InputNotifyChats`, `InputNotifyBroadcasts`; поля `PeerNotifySettings`; что значит отсутствие флага `MuteUntil` и `Silent`.
- [ ] Как `classifyRequest` в `tgclient/throttle.go` относит эти запросы (ожидается обычное чтение, правки не нужно).
- [ ] Как фейки `fakeTelegram` в `internal/daemon/daemon_test.go` и `internal/cli/cli_test.go` дополняются методами из отдельного тестового файла; как встроить свой интерфейс `ownerStateTelegram` в `Telegram` одной строкой.
- [ ] Как `--local` у `search` и `note` обходит требование входа (`requireAccount`) и как это повторить.
- [ ] Как `tg schema` и тест полноты требуют класс, тип результата и пометки `untrusted` у новых команд.
- [ ] Записать итоги в Known facts, непокрытое в Risks и Open questions.

## Файлы потока

Новые файлы, которыми владеет поток (другие потоки их не трогают):

| Слой | Файлы | Что внутри |
| --- | --- | --- |
| `internal/store` | `migrations/0006_folders_account_state.sql`, `folders.go`, `owner_state.go`, `folders_test.go`, `owner_state_test.go` | столбцы папок, таблицы `dialog_state`, `account_sessions`, `blocked_peers`, `notify_defaults`; запись снимков и чтение для `--local` |
| `internal/rpc` | `owner_state.go`, `owner_state_test.go` | константы методов `folder.list`, `chat.state`, `account.sessions`, `account.blocked`, `account.notify`; параметры, результаты, проверка `limit` и `cursor` |
| `internal/tgclient` | `folders.go`, `owner_state.go`, `folders_test.go`, `owner_state_test.go` | `Folders`, `ChatStates`, `Sessions`, `Blocked`, `NotifyDefaults`; перевод peers в Bot API id, пополнение кэша пиров |
| `internal/daemon` | `owner_state.go` (интерфейс `ownerStateTelegram`, `registerOwnerState`, обработчики), `owner_state_test.go` (методы `fakeTelegram` для daemon) | фильтр доступа, сохранение, `--local` |
| `internal/cli` | `folder_cmd.go`, `account_cmd.go`, `chat_state_cmd.go`, `owner_state_cmd_test.go` (методы `fakeTelegram` для cli) | команды, флаги, `commandSpec`, вывод |
| решения | `.agents/decisions/20261114_HHMM_folders_and_owner_state_are_read_snapshots_in_the_store.md` | решение плана |
| временное | `var/agents/plans/20261114_2244_folders_account_state/` | отчёт аудита, вне git |

Общие файлы. Поток вносит в них только локальную вставку рядом с соседними строками того же вида, без переупорядочивания, переименований и правки чужих строк. Это полный список изменений общих реестров:

| Файл | Вставка |
| --- | --- |
| `internal/rpc/api.go` | не меняется: константы методов лежат в `rpc/owner_state.go` |
| `internal/daemon/daemon.go` | одна строка `d.registerOwnerState()` в конце `register()` |
| `internal/daemon/telegram.go` | одна строка `ownerStateTelegram` (встроенный интерфейс) в конце `Telegram` |
| `internal/cli/cli.go` | две строки `a.folderCommand(),` и `a.accountCommand(),` в `root.AddCommand` перед `a.noteCommand()` (по В7) |
| `internal/cli/chat_cmd.go` | одно имя `a.chatStateCommand()` в конце `cmd.AddCommand(...)` |
| `internal/store/schema_test.go` | новые таблицы в списке ожидаемых таблиц (строка 85) и строки проверок новых столбцов `folders` |
| `FEATURES.md` | строки 10.6, 10.10 (подпункты по ответам), сценарии 10.6 и 10.10, статус: 10.6 и 10.10 из «Не сделано, Must», заметка к 10.5.2 (`archived`, `pinned`, мьют видны в `chat state`) |
| `AGENTS.md` | строка в «Decisions made», команды в «Running», `folders.go` и `owner_state.go` в «Layout» у `store` |
| `.agents/rules/project/contracts.md` | один пункт «Folders and owner state» |
| `.agents/decisions/_TOC.md` | одна строка решения |
| `README.md`, `docs/skill/tg-cli/SKILL.md` | по одному абзацу о новых командах |

Если шаг 1 покажет, что встроенный интерфейс невозможен (например, фейк создаётся литералом с проверкой `var _ Telegram`), поток добавляет методы прямо в `Telegram` одной группой строк в конце и пишет это в Known facts.

## Implementation steps

Код начинается здесь. Каждый шаг выполняет Codex Terra в worktree `../tg-cli-folders_account_state`, проверяет Claude (тесты, дифф, `just check`), коммитит Claude.

- [ ] 2. `internal/store`: миграция `0006_folders_account_state.sql` (по В9: временные заглушки 0004 и 0005 или ожидание слияния). Состав: `folders` плюс столбцы `kind TEXT NOT NULL DEFAULT 'filter' CHECK (kind IN ('filter','chatlist','all'))`, `position INTEGER`, `emoticon TEXT`, `color INTEGER`, `rules TEXT CHECK (rules IS NULL OR json_valid(rules))`, `synced_at INTEGER`; `folder_chats` плюс `kind TEXT NOT NULL DEFAULT 'include' CHECK (kind IN ('pinned','include','exclude'))`; новые `STRICT` таблицы `dialog_state` (ключ `chat_id`), `account_sessions` (ключ `hash`), `blocked_peers` (ключ `peer_id`), `notify_defaults` (ключ `scope`), значение `authorization_ttl_days` и время снимков в `account_sessions`. Методы `ReplaceFolders`, `Folders`, `UpsertDialogStates`, `DialogState`, `DialogStates`, `ReplaceSessions`, `Sessions`, `ReplaceBlocked`, `UpsertBlocked`, `Blocked`, `ReplaceNotifyDefaults`, `NotifyDefaults` с фильтром `ChatFilter`. Тесты: миграция на пустой и на базе версии 5, замена снимка удаляет исчезнувшее, upsert, фильтр закрытых чатов, отсутствие `access_hash` в `raw`.
- [ ] 3. `internal/rpc` и `internal/tgclient`: типы и константы методов; клиентские `Folders`, `ChatStates` (один чат через `getPeerDialogs`, N чатов своей итерацией `getDialogs`), `Sessions`, `Blocked` (страница и курсор), `NotifyDefaults`; перевод peers, `InputPeerSelf` в id владельца, пропуск `DialogFolder`; пополнение кэша пиров там, где ответ несёт сущности. Тесты на подмене Telegram: каждый запрос с параметрами, порядок папок и позиция «Все чаты», общая папка, папка с `InputPeerSelf`, пустые ответы, `ContactsBlockedSlice` и курсор, FLOOD_WAIT кодом 6, без входа код 4, только запросы чтения.
- [ ] 4. `internal/daemon`: интерфейс `ownerStateTelegram`, `registerOwnerState`, обработчики пяти методов: проверка параметров, `requireAccount` кроме `--local`, `listChat` для `chat state <чат>`, повторная проверка доступа после ответа, отбрасывание закрытых чатов, сохранение через `d.save`, режим `--local` из базы. Тесты без сокетов: закрытый чат `not_found` без вызова Telegram, закрытые чаты вычищены из папок и заблокированных и не попали в базу, `--local` без входа, `--local` с пустой базой, снимок заменяет прежний.
- [ ] 5. `internal/cli`: `tg folder list`, `tg chat state`, `tg account sessions|blocked|notify` с флагами, `commandSpec` (класс `read`, тип результата, `untrusted`), форматы `json`, `jsonl` для списков, `human`; тесты по образцу `internal/cli` и тест полноты схемы.
- [ ] 6. Документы через `docwriter` и `glavred` по `docs_chain.md`: `FEATURES.md`, `README.md`, навык `docs/skill/tg-cli/`, `contracts.md`, решение в `.agents/decisions/` и строка в `_TOC.md`, `AGENTS.md` (Claude, без цепочки).
- [ ] 7. Живая проверка Claude только чтением, после перебазирования на `main` с 0004 и 0005 (сверить `ls internal/store/migrations` и `PRAGMA user_version`), с паузами между вызовами: `folder list` (число папок совпадает с приложением владельца, позиция «Все чаты» верна), `chat state` для «Браин Тест» и `--limit 20`, `account sessions` (текущая сессия `MacBook Pro M1 BRN` отмечена `current`), `account blocked`, `account notify`; затем каждая команда с `--local` при остановленном соединении; сверка числами по `sqlite3` (строки в таблицах, нет закрытых чатов); IP, названия устройств и тексты в отчёт и журнал не выводятся.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/store/... ./internal/rpc/... ./internal/tgclient/... ./internal/daemon/... ./internal/cli/...` | зелёный | not_run |
| Только чтение | тесты `tgclient` | подмена получила только `get*` запросы | not_run |
| Закрытые чаты | тесты `daemon` и `store` | нет в ответе и в базе, `chat state` даёт `not_found` | not_run |
| Миграция | тест `store` | применяется к пустой базе и к базе версии 5, `account_state` не изменена | not_run |
| Без `access_hash` | тест `store` | сохранённый `raw` папки не содержит `access_hash` | not_run |
| Полнота схемы | тест `internal/cli` | у новых команд класс `read`, тип результата, пометки `untrusted` | not_run |
| Временных миграций нет | `ls internal/store/migrations` перед слиянием | только `0001` до `0006` настоящие, заглушек нет | not_run |
| Живая проверка | шаг 7 | числом: папок, диалогов, сессий, заблокированных, строк в таблицах | not_run |

## Deploy / rollback

- [ ] Выкладки нет; бинарник собирается `just build`.
- [ ] Порядок слияния: 0004, затем 0005, затем эта ветка (0006), затем 0007.
- [ ] Откат: откат ветки до слияния; после применения 0006 на живой базе старая сборка не откроет базу (`user_version` 6), откат тогда это сборка с этой веткой либо удаление `store.db` профиля (история перечитывается из Telegram, пометки теряются, поэтому только с согласия владельца).

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261114_2244_folders_account_state/`.
- [ ] Временные файлы не в git (`/var/` в `.gitignore`).
- [ ] Временные заглушки миграций (если В9 = Б) удалены до слияния.

## Definition of done

- [ ] Код изменён только в рамках плана и раздела «Файлы потока».
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.
- [ ] Решение записано в `.agents/decisions/`, `_TOC.md` и `FEATURES.md` обновлены.

## Known facts

- Миграции: в `main` файлы `0001_init.sql`, `0002_schema.sql`, `0003_events.sql`; `listMigrations` в `internal/store/store.go` требует версии подряд с 1 (`migration versions must start at 1 and be consecutive`), `applyMigrations` применяет только версии больше `PRAGMA user_version`. Файлы встраиваются через `//go:embed migrations/*.sql`, реестра в коде нет.
- `0002_schema.sql` уже создаёт `folders(id, title, raw, first_seen_at, last_seen_at)` и `folder_chats(folder_id, chat_id, position, PK(folder_id, chat_id))`; код их не пишет, `schema_test.go` (строки 85, 91, 189, 205, 210, 211) проверяет их столбцы.
- `account_state(key, value JSON, updated_at)` используют `tgclient/update_state.go` (`updates.state.<user>`, `updates.channel.<user>.<channel>`, `updates.channels.<user>`) и `store/events.go` (`events.high_water`).
- `chats.unread` уже пишется из `chat list` (`UnreadCount` в `toChat`); остальные поля `tg.Dialog` сегодня теряются.
- Поток событий уже выдаёт `read` (`OnAnyReadInbox`, `OnAnyReadOutbox`), но в базу маркеры не пишет; `UpdateDialogFilter*`, `UpdatePeerBlocked`, `UpdateNotifySettings` не обрабатываются.
- gotd: `DialogFilter{Contacts, NonContacts, Groups, Broadcasts, Bots, ExcludeMuted, ExcludeRead, ExcludeArchived, TitleNoanimate, ID, Title TextWithEntities, Emoticon, Color, PinnedPeers, IncludePeers, ExcludePeers []InputPeerClass}`; `DialogFilterChatlist{HasMyInvites, TitleNoanimate, ID, Title, Emoticon, Color, PinnedPeers, IncludePeers}`; `DialogFilterDefault{}`; `MessagesDialogFilters{TagsEnabled, Filters}`.
- gotd: `Dialog{Pinned, UnreadMark, ViewForumAsMessages, Peer, TopMessage, ReadInboxMaxID, ReadOutboxMaxID, UnreadCount, UnreadMentionsCount, UnreadReactionsCount, UnreadPollVotesCount, NotifySettings, Pts, Draft, FolderID, TTLPeriod}`; `PeerNotifySettings{ShowPreviews, Silent, MuteUntil, *Sound, Stories*}`; `MessagesPeerDialogs{Dialogs, Messages, Chats, Users, State}`.
- gotd: `Authorization{Current, OfficialApp, PasswordPending, EncryptedRequestsDisabled, CallRequestsDisabled, Unconfirmed, Hash, DeviceModel, Platform, SystemVersion, APIID, AppName, AppVersion, DateCreated, DateActive, IP, Country, Region}`; `AccountAuthorizations{AuthorizationTTLDays, Authorizations}`.
- gotd: `ContactsGetBlockedRequest{MyStoriesFrom, Offset, Limit}`; ответы `ContactsBlocked{Blocked, Chats, Users}` и `ContactsBlockedSlice{Count, ...}`; `PeerBlocked{PeerID, Date}`.
- Интерфейс `Telegram` в `internal/daemon/telegram.go` реализуют `tgclient.Client` и два фейка `fakeTelegram` (`internal/daemon/daemon_test.go`, `internal/cli/cli_test.go`).
- Старая утилита: `tg user blocked [--limit N]`.

## Open questions

Решает владелец, менеджер задаёт по одному. Рекомендованный вариант первым.

| № | Question | Why it blocks | Options и стоимость | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| В1 | Когда Daemon получает папки и состояние | Определяет шаги 3 и 4 и полноту 10.10.1 | А) только по команде: команды чтения идут в Telegram и сохраняют, `--local` читает базу; `chat state` только для одного чата: около дня на все команды. Б) А плюс `chat state --limit N` без чата: состояние первых N диалогов своей итерацией `getDialogs`, без правки общих файлов: плюс около 3 часов. В) Б плюс `chat list` попутно пишет состояние выведенных чатов: плюс 2 часа и вставка в общие `tgclient/client.go` и `daemon.go` (`chatList`), риск конфликта с соседями. Г) В плюс полная синхронизация при старте Daemon: плюс день, лишние запросы на каждом старте, риск FLOOD_WAIT | Б: 10.10.1 закрыт для всех чатов, общие файлы не трогаются | принято |
| В2 | Что хранить как состав папки (10.6.1) | Меняет схему и объём работы | А) правила папки (контакты, группы, каналы, боты, исключения) и явные списки `pinned`, `include`, `exclude`: дёшево. Б) А плюс фактический состав по правилам: обход всех диалогов и признака контакта, плюс около дня, состав устаревает при каждом новом чате | А: так хранит Telegram, агент видит правила | принято |
| В3 | Какие поля сессий хранить и показывать (10.10.4) | Приватность владельца | А) все, включая `ip`: ничего не стоит. Б) без `ip`, страна и регион остаются: ничего не стоит. В) без `ip`, страны и региона. Во всех вариантах `hash` хранится, но не выводится | Б: агент видит устройство, приложение и время, а адрес входа остаётся у владельца | принято |
| В4 | Чей пункт 10.10.2 (черновики в базе) | Пересечение с потоком `scheduled_drafts_typing` (миграция 0007, его В5) | А) черновики хранит поток 0007 вместе со своими командами `chat draft`; этот план их не трогает: 0. Б) этот план пишет `Dialog.Draft` в `dialog_state`: плюс 2 часа, два читателя черновика в двух потоках. В) отдельная задача позже | А: номер 0007 зарезервирован под «отложенные и черновики», один владелец у черновиков | принято |
| В5 | Чей пункт 10.10.3 (заблокированные) | Пересечение с потоком `users_contacts` (7.5.2 блокировка, миграция 0005, у старой утилиты `tg user blocked`) | А) этот план: таблица `blocked_peers` и `tg account blocked`; `users_contacts` делает только блокировку и разблокировку, обновление строки после записи отдельной правкой после слияния: около 3 часов здесь. Б) всё о заблокированных у `users_contacts` (команда `tg user blocked`), 10.10.3 уходит из плана: минус 3 часа здесь, плюс там. В) А, но команда называется `tg user blocked` в группе соседа: вставка в чужой файл, конфликт | А: 10.10.3 записан в 10.10, таблица идёт в 0006; согласовать с менеджером | принято |
| В6 | Обновлять ли базу событиями потока | Свежесть данных против конфликтов | А) нет, данные обновляет команда, свежесть по `synced_at`: 0. Б) да: `UpdateDialogFilter*`, `UpdatePeerBlocked`, `UpdateNotifySettings`, события `read` правят базу: плюс день, правка общих `tgclient/updates_events.go` и `daemon/events.go` | А: отдельной задачей после слияния всех потоков | принято |
| В7 | Имена команд | Схема и навык | А) `tg folder list`, `tg chat state`, `tg account sessions|blocked|notify`: две новые группы. Б) всё в одной группе `tg account folders|state|sessions|blocked|notify`: одна новая группа, но состояние чата уходит из группы `chat`. В) `tg chat folders`, `tg chat state`, `tg account ...` | А: папки это отдельная сущность, состояние относится к чату, остальное к аккаунту | принято |
| В8 | Объём настроек уведомлений (10.10.5) | Меняет число запросов | А) настройки чата в `chat state` плюс три общих (личные, группы, каналы): 3 запроса. Б) А плюс список исключений `account.getNotifyExceptions`: плюс 2 часа, дублирует `chat state` | А | принято |
| В9 | Как разрабатывать миграцию 0006, пока 0004 и 0005 не в `main` | `listMigrations` требует номера подряд, иначе `just check` падает | А) миграция и код хранилища пишутся только после слияния 0004 и 0005; шаги 3 до 5 идут раньше на подмене хранилища: поток ждёт, плюс полдня на подмену. Б) в ветке временные пустые `0004_placeholder_do_not_merge.sql` и `0005_placeholder_do_not_merge.sql`; при перебазировании настоящие файлы дают дубль версии, и `listMigrations` падает, пока заглушки не удалены; живой запуск только после удаления: 0, нужна дисциплина слияния. В) менять механизм миграций на таблицу применённых версий: общая инфраструктура, не для этого потока | Б: поток не ждёт, ошибка заглушек ловится сама; живая проверка строго после перебазирования | принято |
