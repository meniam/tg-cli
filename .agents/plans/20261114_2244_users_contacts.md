# Пошаговый план: профиль пользователя, блокировка и контакты

ID: 20261114_2244_users_contacts
Created: 2026-11-14
Owner: Мязин Евгений
Branch: feature/20261114_2244_users_contacts
PR: —

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: draft
- Last update: 2026-11-14
- Next safe step: менеджер задаёт владельцу вопросы Q1 до Q8 из «Open questions» по одному; после ответов статус `approved`, затем шаг 1 (аудит, Codex Terra в worktree `../tg-cli-users_contacts`). Q9 задаётся перед шагом 7.

## Context and sources

- Read: `INTENT.md`; `FEATURES.md` пункты 3.9, 7.4.6, 7.5, 7.7.3, 9.2, 9.4, 10.7, 10.9, 10.10 и строки статуса 10.7 и 10.9; `AGENTS.md`; `.agents/rules/plans.md`; план `20261114_1933_reactions_pins_polls.md` как образец; решение `20261114_0900_access_lists_hide_chats_and_untrusted_text_is_marked.md` (раздел «Не покрыты»: люди не фильтруются); план идемпотентной записи в worktree `../tg-cli-idem` (охват `--expect-user`, список безопасных чтений).
- Код: `internal/store/migrations/0002_schema.sql` (таблица `people`, `people_fts`), `internal/store/store.go` (`listMigrations`, `applyMigrations`), `internal/store/telegram.go` (`Person`, `mergePerson`, `UpsertPeople`), `internal/store/search.go` (`searchPeople`), `internal/daemon/persist.go` (`chatPerson`, `saveMembers`, `convertStoredMessages`), `internal/daemon/chat_lists.go` (`chatCommon` как образец работы с пользователем), `internal/daemon/write.go` (`writeReference`), `internal/daemon/notes.go` (`resolveStoredReference`, `search`), `internal/daemon/telegram.go` (интерфейс `Telegram`), `internal/daemon/daemon.go` (`register`), `internal/daemon/daemon_test.go` (`fakeTelegram`), `internal/daemon/access_test.go` (`TestRegisteredMethodsHaveAccessClass`), `internal/tgclient/peers.go` (`resolve`, `toChatOf`), `internal/tgclient/throttle.go` (`writeRequest`), `internal/tgclient/readonly_test.go` (`readFiles`), `internal/tgclient/client.go` (`peers.InmemoryStorage`), `internal/cli/cli.go`, `internal/cli/spec.go`, `internal/cli/chat_lists_cmd.go`.
- gotd v0.166.0 (`go list -m -f '{{.Dir}}' github.com/gotd/td`): `tg.ContactsGetContactsRequest{Hash}` и ответы `ContactsContacts{Contacts, SavedCount, Users}` / `ContactsContactsNotModified`; `ContactsAddContactRequest{AddPhonePrivacyException, ID, FirstName, LastName, Phone, Note}`; `ContactsDeleteContacts(ctx, []InputUserClass)`; `ContactsImportContacts(ctx, []InputPhoneContact)`; `ContactsSearchRequest{Q, Limit, Broadcasts, Bots}` и `ContactsFound{MyResults, Results, Chats, Users}`; `ContactsBlockRequest{MyStoriesFrom, ID}` и `ContactsUnblockRequest`; `ContactsResolvePhone(ctx, phone)`; `UsersGetFullUser(ctx, InputUserClass)` и `UserFull` (`About`, `Blocked`, `CommonChatsCount`, `Birthday`, `PersonalChannelID`, `Note`); `tg.User` (`Contact`, `MutualContact`, `Deleted`, `Bot`, `Verified`, `Premium`, `Scam`, `Fake`, `Phone`, `Status`, `Usernames`); `telegram/peers`: `User.Block`, `User.Unblock`, `User.FullRaw`, `Manager.ResolvePhone`.
- Сравнение с установленной `tg` 0.5.1: `tg user profile|block|unblock|blocked`, `tg contact list [--limit --offset]|add <username-or-phone> [--first-name --last-name]|delete <user>|search <query> [--limit --global]`.
- Not verified: тесты и `just check` не запускались (это шаг 1). Не проверено, какие имена Telegram отдаёт для контакта (сохранённые владельцем или собственные имена пользователя), как часто сервер позволяет `contacts.importContacts`, какие ошибки дают `contacts.addContact` и `contacts.block` для бота, удалённого аккаунта и самого себя.

## Goal

Агент читает профиль пользователя, блокирует и разблокирует его, получает, добавляет, удаляет и ищет контакты. Daemon хранит людей с раздельными именем и фамилией, телефоном, био и признаком контакта, а локальный поиск находит людей и контакты без Telegram.

## Problem and contract

Тип плана: product (две новые группы команд, изменение правила сохранения людей).

Сегодня (`FEATURES.md` 10.7 `[~]`) люди попадают в `people` только из личных чатов, ботов, авторов сообщений и участников, и всегда одним отображаемым именем в `first_name`; `last_name`, `phone`, `bio` и `is_contact` пусты. Команд для профиля, блокировки и контактов нет (7.5 не начат). Поиск людей (`tg search --scope people`) работает, но не фильтруется списками доступа.

Контракт ниже записан для рекомендованных вариантов Q1 до Q8; другой ответ владельца меняет отмеченные места.

- Команды (Q1): группа `tg user`: `profile <user>`, `block <user>`, `unblock <user>`; группа `tg contact`: `list`, `add <user>`, `delete <user>`, `search <query>`. `<user>` это `@username`, положительный id или телефон контакта (как ссылка на чат, `chatref`); группа, канал и отрицательный id дают код 2.
- `tg user profile <user>` (класс `read`): один запрос `users.getFullUser`. Ответ `UserProfile`: `id`, `type` (`user` или `bot`), `first_name`, `last_name`, `username`, `usernames` (дополнительные активные), `phone` (когда Telegram его отдаёт), `bio`, `is_contact`, `mutual_contact`, `blocked`, `verified`, `premium`, `scam`, `fake`, `deleted`, `status` (`online`, `offline`, `recently`, `last_week`, `last_month`, `hidden`) и `last_seen` для `offline`, `common_chats_count`. Поля имён, `username`, `usernames` и `bio` помечены `untrusted`. Человек сохраняется в `people` с раздельными именами, телефоном, био и признаком контакта. Аватар, день рождения, бизнес-поля, личный канал и заметка Telegram к контакту вне плана.
- `tg user block <user> [--dry-run]` и `tg user unblock <user> [--dry-run]` (класс `account_write`, не `dangerous`, без `--yes`, Q3): `contacts.block` / `contacts.unblock` в основной список блокировки (`my_stories_from` выключен). Ответ `{user_id, blocked, dry_run?}`. Повторная блокировка уже заблокированного не ошибка, если так ответит Telegram (уточняет шаг 1). Состояние блокировки в базе не хранится: список заблокированных принадлежит потоку состояния аккаунта (10.10.3).
- `tg contact list [--limit N] [--offset N] [--local]` (класс `read`): без `--local` один запрос `contacts.getContacts` с `hash = 0`, полный список сохраняется в базу (у вернувшихся `is_contact = 1`, у сохранённых контактов, которых в ответе нет, `is_contact = 0`), страница режется локально; `--local` читает только базу (`is_contact = 1`) и не требует входа. `--limit` по умолчанию 100, от 1 до 1000; `next_offset`, пока есть продолжение; `--format jsonl` печатает по контакту в строке (`ItemPath: contacts`). Элемент: `id`, `first_name`, `last_name`, `username`, `phone` (Q8), `mutual` (только без `--local`), `deleted`. Порядок: по `first_name`, `last_name`, `id` без учёта регистра.
- `tg contact add <user> [--first-name ИМЯ] [--last-name ФАМИЛИЯ] [--phone НОМЕР] [--dry-run]` (класс `account_write`, не `dangerous`, без `--yes`): `contacts.addContact` по `@username` или id (Q4: по одному телефону контакт не добавляется). Имя по умолчанию берётся из текущего имени пользователя, пустое итоговое имя даёт код 2 до Telegram; `add_phone_privacy_exception` всегда выключен (номер владельца не раскрывается). Ответ `{user_id, first_name, last_name, is_contact: true, mutual}`; человек сохраняется с `is_contact = 1`.
- `tg contact delete <user> [--yes] [--dry-run]` (класс `account_write`, `dangerous`, Q3): без `--yes` и без `--dry-run` код 2 и Daemon не вызывается. `contacts.deleteContacts` с одним пользователем. Ответ `{user_id, deleted: true}`; в базе `is_contact = 0`, остальные данные человека остаются.
- `tg contact search <query> [--limit N] [--local]` (класс `read`, Q6): без `--local` один запрос `contacts.search`; в ответе только пользователи и боты, сначала свои (`my_results`), затем глобальные (`results`), поле `source` (`contacts` или `global`); чаты и каналы из ответа отбрасываются. `--limit` по умолчанию 20, от 1 до 100. Найденные люди сохраняются. С `--local` поиск по базе среди `is_contact = 1` той же нормализацией, что `tg search` (регистр, «ё», безопасный FTS5), без входа.
- Запись (Q2): `block`, `unblock`, `contact add`, `contact delete` разрешены только для пользователя, чей id стоит в `write` файла `access.json`; иначе код 9 без запроса в Telegram. `--dry-run` проверяет ссылку, видимость и `write` и не вызывает методы записи Telegram. Записи не повторяются: четыре запроса добавляются в `writeRequest` дросселя, FLOOD_WAIT даёт код 6.
- Списки доступа (Q5): человек считается чатом со своим id. Закрытый пользователь в `user profile|block|unblock` и `contact add|delete` даёт тот же `not_found`, что неизвестный (как `chat common`, 7.7.3); из `contact list`, `contact search` и `tg search --scope people` закрытые люди убираются до ответа и не сохраняются в базу (9.2.6).
- Синхронизация контактов (Q7): кроме `contact list`, Daemon один раз после входа (при старте с готовой сессией или после `auth login`) загружает контакты в фоне: один запрос `contacts.getContacts` в нижней очереди дросселя, ошибка пишется в журнал и не мешает работе. Так `contact list --local`, `contact search --local` и `tg search --scope people` находят контакты без ручного вызова.
- Имена людей в базе: полные источники (профиль, контакты, поиск контактов, добавление) пишут `first_name` и `last_name` раздельно. Частичные источники (`chat list`, `chat resolve|info`, участники, авторы сообщений, `chat common`) передают только отображаемое имя в новом поле `store.Person.DisplayName`. Правило слияния: если сохранённые `first_name` и `last_name`, склеенные через пробел, равны отображаемому имени, они не меняются; иначе `first_name = DisplayName`, `last_name = NULL`. Так поле не мигает между «Иван Петров» и «Иван» + «Петров».
- Телефон (Q8): номер хранится в `people.phone` и печатается в ответах профиля и списка контактов; в журнале Daemon, `--verbose` и текстах ошибок его нет (9.5).
- Журнал Daemon пишет метод и длительность, без имён, телефонов и запросов поиска.
- Миграция: схема не меняется. Колонки `first_name`, `last_name`, `phone`, `bio`, `is_contact` и индекс `people_is_contact_idx` уже есть в `0002_schema.sql`. Номер `0005` остаётся за потоком и используется, только если ответ владельца потребует новых колонок (например хранить `mutual` или блокировку); тогда шаг 2 добавляет `internal/store/migrations/0005_people_contacts.sql` и учитывает ловушку нумерации из «Risks».
- Релиз одним слоем; откат: откат ветки, база совместима в обе стороны (схема не меняется).

## Файлы потока

Новые файлы, которыми владеет поток (другие потоки их не трогают):

- `internal/rpc/users.go`, `internal/rpc/users_test.go`: типы параметров и ответов (`UserRefParams`, `UserProfile`, `BlockParams`, `BlockResult`, `ContactListParams`, `ContactListResult`, `Contact`, `ContactAddParams`, `ContactAddResult`, `ContactDeleteParams`, `ContactDeleteResult`, `ContactSearchParams`, `ContactSearchResult`, `ContactHit`), проверка параметров.
- `internal/tgclient/users_read.go`, `internal/tgclient/users_write.go`, `internal/tgclient/users_test.go`: `UserProfile`, `Contacts`, `SearchContacts` (чтение) и `AddContact`, `DeleteContact`, `Block`, `Unblock` (запись), перевод `tg.User`/`UserFull` в типы `rpc`, перевод ошибок.
- `internal/daemon/users.go`, `internal/daemon/users_test.go`, `internal/daemon/users_fake_test.go`: интерфейс `usersTelegram`, обработчики шести методов, фоновая синхронизация контактов, заглушки методов `fakeTelegram` в своём файле и обёртка с поведением для тестов потока.
- `internal/store/people.go`, `internal/store/people_test.go`: `ReplaceContacts` (полная синхронизация признака контакта в одной транзакции), `SetContact`, `ListContacts`, `SearchContacts`.
- `internal/cli/users_cmd.go`, `internal/cli/users_cmd_test.go`: группы `user` и `contact`, флаги, аннотации схемы.
- `.agents/decisions/20261114_HHMM_users_and_contacts_are_writes_under_write_and_people_follow_access_lists.md` (имя уточняется в шаге 6).

Общие файлы, каждая правка одной локальной вставкой (новые строки рядом, без переформатирования соседних):

| Файл | Вставка |
| --- | --- |
| `internal/rpc/api.go` | блок из семи констант `MethodUserProfile = "user.profile"`, `MethodUserBlock = "user.block"`, `MethodUserUnblock = "user.unblock"`, `MethodContactList = "contact.list"`, `MethodContactAdd = "contact.add"`, `MethodContactDelete = "contact.delete"`, `MethodContactSearch = "contact.search"` отдельной группой в конце `const` |
| `internal/daemon/telegram.go` | одна строка `usersTelegram` (встроенный интерфейс) в конце `Telegram` |
| `internal/daemon/daemon.go` | одна строка `d.registerUsers()` в конце `register()`; одна строка запуска фоновой синхронизации контактов после готовности сессии (место уточняет шаг 1) |
| `internal/daemon/access_test.go` | одна новая строка в карте `classes` для семи методов |
| `internal/daemon/persist.go` | три замены `FirstName:` на `DisplayName:` в `chatPerson`, `saveMembers`, `convertStoredMessages` |
| `internal/store/telegram.go` | поле `DisplayName *string` в `Person` и правило имён в `mergePerson` |
| `internal/store/search.go` | условие списков доступа в `searchPeople` (параметр `ChatFilter`, как у `searchChats`) |
| `internal/tgclient/throttle.go` | одна строка в `writeRequest`: `*tg.ContactsAddContactRequest, *tg.ContactsDeleteContactsRequest, *tg.ContactsBlockRequest, *tg.ContactsUnblockRequest` |
| `internal/tgclient/readonly_test.go` | `"users_read.go"` в конец `readFiles` |
| `internal/cli/cli.go` | две строки `a.userCommand(), a.contactCommand()` в `root.AddCommand` |
| `AGENTS.md`, `.agents/rules/project/contracts.md`, `FEATURES.md`, `README.md`, `docs/skill/tg-cli/SKILL.md`, `.agents/decisions/_TOC.md` | своя строка или свой абзац в шаге 6 |

Изменения общих реестров (полный список):

1. Методы JSON-RPC: семь новых имён в `internal/rpc/api.go`, регистрация в `internal/daemon/daemon.go`.
2. Интерфейс `daemon.Telegram`: встроенный `usersTelegram` с методами `UserProfile`, `Contacts`, `SearchContacts`, `AddContact`, `DeleteContact`, `Block`, `Unblock`.
3. Классы доступа в тесте `TestRegisteredMethodsHaveAccessClass`: `user.profile`, `user.block`, `user.unblock`, `contact.add`, `contact.delete` как `user-ref` (новый класс: ссылка на человека, закрытый даёт `not_found`), `contact.list` и `contact.search` как `chat-search` (список с отбором).
4. Список записей дросселя `writeRequest`: четыре запроса `contacts.*` выше.
5. Список файлов только для чтения `readFiles`: `users_read.go`.
6. Команды CLI и схема: группы `user` и `contact` в корне; классы безопасности `read`, `account_write`; `dangerous` только у `contact delete`.
7. Хранилище: поле `Person.DisplayName` и правило слияния имён; фильтр доступа у поиска людей. Миграций нет (0005 зарезервирован и не занят).
8. Ключи `account_state`: не используются.

## Scope

- В плане: `internal/{rpc,tgclient,daemon,store,cli}`, схема `tg schema`, тесты, документы, живая проверка чтением и, после Q9, записью.
- Вне плана: `tg user blocked` (список заблокированных и его хранение, 10.10.3, поток папок и состояния аккаунта); добавление по телефону через `contacts.importContacts` (если Q4 решён так); `add_phone_privacy_exception`; заметки Telegram к контакту (`Note`), аватары, день рождения, бизнес-поля; правка своего профиля (`account.updateProfile`); несколько пользователей в одном вызове; блокировка только для историй (`my_stories_from`); обработка обновлений `UpdatePeerBlocked` и смены имён в живом потоке; хэш `contacts.getContacts`.
- Затрагиваются: файлы из раздела «Файлы потока».

## Main invariant

1. Без видимости пользователя и без его id в `write` ни один запрос записи (`contacts.addContact`, `contacts.deleteContacts`, `contacts.block`, `contacts.unblock`) в Telegram не уходит; закрытый пользователь отвечает как неизвестный.
2. Записи не повторяются; FLOOD_WAIT на них не повторяется.
3. Существующие команды и их ответы не меняются; `tg search --scope people` меняется только отбором закрытых людей (Q5).
4. `--dry-run` и `--local` не вызывают методы записи Telegram; `--local` не вызывает Telegram вообще.
5. Схема базы не меняется; данные `people` других путей не теряются (слияние только дополняет).
6. `internal/cli` не импортирует `tgclient`, gotd, `store`.
7. Ни тест, ни проверка по умолчанию не обращаются к живому Telegram; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: раннер миграций требует номера подряд с 1 (`listMigrations`: «migration versions must start at 1 and be consecutive»), а `applyMigrations` пропускает версии не больше `user_version`. Поток с `0005` без `0004` в ветке не откроет ни одну базу, и тесты store упадут; живая база, получившая `0005` раньше `0004`, навсегда пропустит `0004`. Стратегия: этот поток обходится без миграции. Если ответ владельца потребует колонок, шаг миграции ждёт слияния `0004` в `main`, живая проверка на ветке с миграцией идёт только после копии `store.db`. Риск касается потоков `0006` и `0007` (см. пересечения).
- Риск: правка `mergePerson` меняет имена у всех путей сохранения людей. Стратегия: поле `DisplayName` вместо `FirstName` в трёх местах `persist.go`, таблица тестов на слияние (полное после частичного, частичное после полного с тем же и с новым именем, без фамилии).
- Риск: агент блокирует или удаляет не того человека. Стратегия: `write` по id, `--dry-run`, `--yes` у удаления, `unblock` и `add` для отката.
- Риск: `contact list` при каждом вызове тянет весь список контактов. Стратегия: один запрос на вызов, `--local` для повторных страниц; хэш вне плана, если список окажется большим (шаг 7 меряет размер).
- Риск: фоновая синхронизация при каждом старте Daemon (с `--idle-timeout` стартов может быть много). Стратегия: один запрос в нижней очереди дросселя на запуск; ошибка только в журнал.
- Риск: телефоны и имена чужих людей в выводе. Стратегия: поля `untrusted`, номера нет в журнале и ошибках, решение Q8 у владельца.
- Риск: слияние с параллельными потоками в общих реестрах. Стратегия: вставки отдельными строками, список выше, при конфликте сохраняются обе стороны.

## Decision log

| Date | Decision | Reason | Confirmed by |
| --- | --- | --- | --- |
| 2026-11-14 | Миграция не нужна, номер 0005 зарезервирован и не занят | Колонки уже есть в `0002_schema.sql`; номер подряд обязателен, ранняя `0005` ломает ветку без `0004` | предложено планировщиком |
| 2026-11-14 | Частичные пути пишут отображаемое имя в `DisplayName`, полные пишут `first_name` и `last_name`; совпадающая склейка не перезаписывается | Иначе имя мигает между путями | предложено планировщиком |
| 2026-11-14 | `contact list` берёт полный список без хэша, `--local` читает базу | Простота и предсказуемость; хэш при необходимости позже | предложено планировщиком |
| 2026-11-14 | Блокировка в базе не хранится | Список заблокированных (10.10.3) у потока состояния аккаунта | предложено планировщиком, связано с потоком 0006 |
| 2026-11-14 | `add_phone_privacy_exception` всегда выключен | Номер владельца не раскрывается без отдельного решения | предложено планировщиком |
| 2026-11-14 | Q1. Как назвать команды: b)  | block\ | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | Q2. Что разрешает запись о людях (`block`, `unblock`, `contact add`, `contact delete`): A) id пользователя в `write` файла `access.json`, как у чатов | A. Цена: чтобы заблокировать спамера или добавить контакт, владелец сначала вписывает его id в `write`; агент сам расширить доступ не может | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | Q3. Какие команды требуют `--yes` и метку `dangerous`: A) только `contact delete` (теряется сохранённое владельцем имя) | A. Цена: блокировка проходит без подтверждения, откат через `unblock` | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | Q4. Добавлять ли контакт по одному номеру телефона: A) только по `@username` или id, номер можно сохранить флагом `--phone` | A. Цена: человека, известного только по номеру, агент не добавит | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | Q5. Действуют ли списки доступа на людей: A) человек это чат со своим id: закрытый даёт `not_found` в командах и пропадает из `contact list`, `contact search` и `search --scope people` | A. Цена: при непустом `allow` список контактов покажет только людей из `allow`; меняется вывод существующего поиска людей | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | Q6. Что ищет `contact search`: A) Telegram `contacts.search` (свои, затем глобальные, только люди и боты), `--local` ищет среди контактов в базе | A. Цена: один запрос на вызов без `--local` | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | Q7. Когда контакты попадают в базу: A) при каждом `contact list` и один раз в фоне после входа (один запрос на запуск Daemon) | A. Цена: один запрос `getContacts` при каждом старте Daemon | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |
| 2026-11-14 | Q8. Хранить и показывать ли номера телефонов: A) хранить и показывать в профиле и списке контактов, но не в журнале и ошибках | A. Цена: номера контактов доступны агенту; без них не работает ссылка на чат по телефону (3.9.1) | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана |

## Step 0. Fix decisions before changes

- [ ] Ответы владельца на Q1 до Q8 записаны в Decision log, контракт поправлен под ответы.
- [ ] Если ответы требуют колонок, решение о миграции 0005 и порядке слияния с потоком 0004 записано.
- [ ] Исполнитель Go-кода (Codex Terra) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [x] Ветка `feature/20261114_2244_users_contacts` создана от `main` в worktree `../tg-cli-users_contacts`.

## Step 1. Audit and baseline (исполнитель Codex Terra)

Отчёт в `var/agents/plans/20261114_2244_users_contacts/audit.md` (в git не входит), итог в «Known facts».

- [ ] `just check` и `just build` на чистом дереве ветки; результат в Known facts.
- [ ] gotd: точные сигнатуры и ответы `users.getFullUser`, `contacts.getContacts`, `contacts.addContact`, `contacts.deleteContacts`, `contacts.search`, `contacts.block`, `contacts.unblock`; что возвращают `Updates` у записи и нужно ли применять их к `peers.Manager` (`Apply`), чтобы кэш знал новый контакт.
- [ ] Какие имена `tg.User` отдаёт для контакта: сохранённые владельцем или собственные (по исходникам и документации TL; если не выяснить, пометить для шага 7).
- [ ] Перевод `tg.UserStatusClass` в `status` и `last_seen`; что значит `Phone` у не-контакта.
- [ ] Ошибки Telegram и коды: `CONTACT_ID_INVALID`, `CONTACT_NAME_EMPTY`, `USER_ID_INVALID`, `PEER_ID_INVALID`, `USER_PRIVACY_RESTRICTED`, `SEARCH_QUERY_EMPTY`, `QUERY_TOO_SHORT`, блокировка самого себя и бота; где их переводит `mapError`.
- [ ] `peers.User.Block|Unblock` оборачивают ошибку (`errors.Wrap`): проверить, что `mapError` видит `tgerr`, иначе вызывать `ContactsBlock` напрямую.
- [ ] Место запуска фоновой синхронизации в `daemon.go` (после `Ready` и авторизации, в том числе после `auth login`) и как поставить запрос в нижнюю очередь дросселя (`throttle.WithBackground`).
- [ ] Все места, где сохраняются люди (`persist.go`, `chat_lists.go`, `notes.go`, `events.go`), и какое имя каждое передаёт.
- [ ] Подтвердить, что без миграции хватает схемы; если нет, записать какие колонки и почему.
- [ ] Записать итоги в Known facts, непокрытое в Risks и Open questions.

## Implementation steps

Каждый шаг кода выполняет Codex Terra, проверяет Claude (тесты, дифф, `just check`), коммитит Claude.

- [ ] 2. `store`: поле `Person.DisplayName` и правило имён в `mergePerson`; `ReplaceContacts` (одна транзакция: вернувшимся `is_contact = 1`, прочим сохранённым контактам `0`), `SetContact`, `ListContacts` (порядок, `limit`, `offset`, фильтр доступа), `SearchContacts` (FTS по `people_fts` среди `is_contact = 1`, фильтр доступа); фильтр доступа в `searchPeople`. Тесты: слияние имён (таблица случаев), полная синхронизация с удалённым контактом, закрытые люди не видны в списке и поиске, «ё» и регистр, синтаксис FTS5 в запросе.
- [ ] 3. `rpc` и `tgclient`: типы и проверка параметров в `internal/rpc/users.go`, константы в `api.go`; `users_read.go` (`UserProfile`, `Contacts`, `SearchContacts`) и `users_write.go` (`AddContact`, `DeleteContact`, `Block`, `Unblock`), перевод `tg.User`, `UserFull`, статуса; четыре запроса в `writeRequest`; `users_read.go` в `readFiles`; перевод ошибок в коды. Тесты на подмене invoker: параметры каждого запроса, `my_stories_from` и `add_phone_privacy_exception` выключены, отбор чатов из `contacts.search`, порядок `my_results` перед `results`, статусы, FLOOD_WAIT записи без повтора.
- [ ] 4. Daemon: `usersTelegram` и одна строка в `Telegram`; обработчики семи методов в `users.go` (разбор ссылки на человека, видимость, `write`, `--dry-run`, `--yes` проверяет CLI, сохранение людей, отбор закрытых); фоновая синхронизация контактов один раз после входа; заглушки `fakeTelegram` в `users_fake_test.go`; строка в карте классов `access_test.go`; три замены в `persist.go`. Тесты без сокетов: пользователь вне `write` код 9 и ноль вызовов записи, закрытый пользователь `not_found` до Telegram, группа вместо пользователя код 2, `--dry-run` без вызова записи, `--local` без вызова Telegram, синхронизация ставит и снимает `is_contact`, ошибка фоновой синхронизации не роняет Daemon.
- [ ] 5. CLI: `internal/cli/users_cmd.go`, группы `user` и `contact`, флаги и проверки (`--yes` у `contact delete`, пределы `--limit`, `--offset` не меньше 0), аннотации (`read`, `account_write`, `dangerous` у `contact delete`, `ItemPath`), две строки в `cli.go`; тесты по образцу `chat_lists_cmd_test.go`, тест полноты схемы проходит.
- [ ] 6. Документы через `docwriter` и `glavred` по `docs_chain.md`: `AGENTS.md` (Running: две группы; Decisions made: одна строка со ссылкой на решение; Layout при необходимости), `contracts.md` (абзац «пользователи и контакты»), `FEATURES.md` (7.5 и подпункты, 10.7, 10.9.2, 9.2.1 про людей, статус и «не проверено»), `README.md`, `docs/skill/tg-cli/SKILL.md` (предупреждение про блокировку и удаление контакта), решение в `.agents/decisions/` и строка в `_TOC.md`; в решении `20261114_0900` отметить, что вопрос «люди не фильтруются» закрыт.
- [ ] 7. Живая проверка Claude. Чтение (разрешено правилами): `user profile` для себя, контакта и бота; `contact list` и `--local` (число контактов, время ответа), `contact search` по своему контакту и по публичному `username`, `contact search --local`, `tg search --scope people` по имени контакта; закрытый пользователь через временный `access.json` с `block` (затем удалить) даёт `not_found` и пропадает из списков; база: у контактов заполнены `first_name`, `last_name`, `phone`, `is_contact`. Запись только по Q9: `--dry-run` всех четырёх команд, пользователь вне `write` код 9, затем `block`/`unblock` и `add`/`delete --yes` на цели из ответа Q9, проверка через `user profile` и `contact list`, состояние возвращено. Имена и номера в вывод отчёта не попадают, только числа и коды.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/rpc/... ./internal/store/... ./internal/tgclient/... ./internal/daemon/... ./internal/cli/...` | зелёный | not_run |
| Без `write` запроса нет | тесты `internal/daemon` | счётчик вызовов записи подмены равен нулю, код 9 | not_run |
| Закрытый человек | тесты `internal/daemon`, `internal/store` | `not_found`, нет в списках и поиске, не сохранён | not_run |
| Нет повтора записи на FLOOD_WAIT | тесты `internal/tgclient` | один вызов | not_run |
| Слияние имён | тесты `internal/store` | таблица случаев проходит, имя не мигает | not_run |
| Только чтение | `TestReadFilesCallNothingThatWrites` | `users_read.go` без вызовов записи | not_run |
| Полнота схемы и классов | тесты `internal/cli`, `TestRegisteredMethodsHaveAccessClass` | у семи методов класс, у команд класс безопасности | not_run |
| Миграций нет | `git diff --stat main -- internal/store/migrations` | пусто | not_run |
| Живая проверка | шаг 7 | числами: профили, контакты, найдено, коды ошибок | not_run |

## Deploy / rollback

- [ ] Выкладки нет. Откат: откат ветки; схема не меняется, база совместима.
- [ ] После слияния перезапустить Daemon (`tg daemon stop`, `tg daemon start`), чтобы заработала фоновая синхронизация.

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261114_2244_users_contacts/`.
- [ ] Временные файлы не в git (`/var/` в `.gitignore`).
- [ ] Временный `access.json` живой проверки удалён, тестовые изменения контактов и блокировок возвращены.

## Definition of done

- [ ] Код изменён только в рамках плана и раздела «Файлы потока».
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.
- [ ] Решение записано в `.agents/decisions/`, `_TOC.md`, `FEATURES.md` и `AGENTS.md` обновлены.

## Known facts

- Таблица `people` (`0002_schema.sql`) уже содержит `username`, `first_name`, `last_name`, `phone`, `bio`, `is_bot`, `is_contact`, `raw`, `search_text`; есть индекс `people_is_contact_idx` и FTS `people_fts`; `search_text` собирается из имён, `username`, цифр телефона и био (`mergePerson`).
- Все пути сохранения людей передают одно отображаемое имя в `FirstName`: `chatPerson` (`persist.go:31`), `saveMembers` (`persist.go:74`), `convertStoredMessages` (`persist.go:179`); `last_name`, `phone`, `bio`, `is_contact` сегодня никто не пишет.
- `mergeText` и `mergeBool` не затирают сохранённое значение пустым входом, поэтому снять `is_contact` можно только явным `false` или отдельным методом.
- `searchPeople` (`search.go:127`) не применяет `ChatFilter`, в отличие от `searchChats` и `searchMessages`; решение `20261114_0900` называет людей непокрытыми.
- Раннер миграций (`store.go`, `listMigrations`) требует номера подряд с 1, `applyMigrations` применяет только версии больше `user_version`.
- `peers.Manager.ResolvePhone` ищет номер только среди контактов (через `contacts.getContacts`) и не вызывает `contacts.resolvePhone`; кэш пиров в памяти (`peers.InmemoryStorage`, `client.go:83`).
- `writeRequest` (`throttle.go:72`) не содержит запросов `contacts.*`.
- `readonly_test.go` считает записью любой вызов, начинающийся с `Block`, `Unblock`, `Contacts(Block|Import|Delete...)`; поэтому запись живёт в отдельном файле `users_write.go`.
- `fakeTelegram` реализует весь интерфейс `Telegram` в `daemon_test.go`; методы можно добавить в другом файле пакета, не трогая этот.
- `chat common` уже отвечает `not_found` на закрытого пользователя (7.7.3), образец для Q5.
- В установленной `tg` 0.5.1 команды называются `tg user profile|block|unblock|blocked` и `tg contact list|add|delete|search`.

## Open questions

Каждый вопрос задаётся владельцу отдельно. Рекомендуемый вариант первый.

| # | Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| Q1 | Как назвать команды | Имена попадают в схему, навык и документы | A: `tg user profile\|block\|unblock` и `tg contact list\|add\|delete\|search` (как в `tg` 0.5.1); B: всё под `tg contact`; C: группа `tg people` | A. Цена: две новые группы в корне; поток идемпотентности должен добавить их в охват `--expect-user` | принято |
| Q2 | Что разрешает запись о людях (`block`, `unblock`, `contact add`, `contact delete`) | Определяет проверку до Telegram и правку решения о записи | A: id пользователя в `write` файла `access.json`, как у чатов; B: без списка, только `--dry-run` и `--yes`; C: новое поле в `access.json` (например `contacts: true`) | A. Цена: чтобы заблокировать спамера или добавить контакт, владелец сначала вписывает его id в `write`; агент сам расширить доступ не может | принято |
| Q3 | Какие команды требуют `--yes` и метку `dangerous` | Защита от случайного действия | A: только `contact delete` (теряется сохранённое владельцем имя); B: `contact delete` и `user block`; C: ни одна (всё обратимо) | A. Цена: блокировка проходит без подтверждения, откат через `unblock` | принято |
| Q4 | Добавлять ли контакт по одному номеру телефона | `contacts.importContacts` раскрывает, есть ли у номера Telegram, и сервер ограничивает частоту импорта | A: только по `@username` или id, номер можно сохранить флагом `--phone`; B: ещё и по номеру через `importContacts` | A. Цена: человека, известного только по номеру, агент не добавит | принято |
| Q5 | Действуют ли списки доступа на людей | Меняет `search --scope people` и закрывает вопрос решения `20261114_0900` | A: человек это чат со своим id: закрытый даёт `not_found` в командах и пропадает из `contact list`, `contact search` и `search --scope people`; B: фильтр только для прямых ссылок, списки как есть; C: людей не фильтровать | A. Цена: при непустом `allow` список контактов покажет только людей из `allow`; меняется вывод существующего поиска людей | принято |
| Q6 | Что ищет `contact search` | Определяет, нужен ли Telegram | A: Telegram `contacts.search` (свои, затем глобальные, только люди и боты), `--local` ищет среди контактов в базе; B: только база; C: только Telegram | A. Цена: один запрос на вызов без `--local` | принято |
| Q7 | Когда контакты попадают в базу | Без загрузки офлайн-поиск их не видит (10.7.2) | A: при каждом `contact list` и один раз в фоне после входа (один запрос на запуск Daemon); B: только при `contact list`; C: отдельная команда `contact sync` | A. Цена: один запрос `getContacts` при каждом старте Daemon | принято |
| Q8 | Хранить и показывать ли номера телефонов | Номер чужого человека это личные данные | A: хранить и показывать в профиле и списке контактов, но не в журнале и ошибках; B: хранить, показывать только с флагом `--phones`; C: не хранить и не показывать | A. Цена: номера контактов доступны агенту; без них не работает ссылка на чат по телефону (3.9.1) | принято |
| Q9 | На ком проверять запись вживую (шаг 7) | Блокировка и контакты меняют отношения с реальным человеком; без явной цели записи нет | A: тестовый аккаунт или бот, которого назовёт владелец, его id временно в `write`; B: только `--dry-run` и код 9, без реальной записи | A. Задать перед шагом 7. Цена B: запись не проверена на живом аккаунте | open |
