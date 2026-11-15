# Пошаговый план: транскрипция голосовых, статистика каналов и boosts, автопереключение на WSS и HTTP-прокси, поиск по номеру

ID: 20261115_1747_small_features
Created: 2026-11-15
Owner: Мязин Евгений
Branch: feature/20261115_1747_small_features
PR: —

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: draft
- Last update: 2026-11-15
- Next safe step: менеджер принимает рекомендованные варианты Q1 до Q12 из «Open questions» (владелец делегировал решения), записывает их в Decision log, ставит `approved`; затем шаг 1 (аудит, Codex Terra в worktree `../tg-cli-small_features`).

## Context and sources

- Read: `AGENTS.md`, `INTENT.md`, `FEATURES.md` (пункты 1.8, 1.9, 7.5, 10.7, 15.2, 17.4, 18.1, 19.1, строки статуса и списки «не проверено на живом аккаунте»), `.agents/rules/plans.md`, решение `20261115_1800_remaining_could_features_are_split_into_take_wont_and_defer.md`, план-образец `20261115_0439_media_export.md`, план `20261115_0241_net_transport.md` (решения Q1, Q4, Q6, «вне плана: HTTP CONNECT прокси»), план `20261114_2244_users_contacts.md` (решение Q4 про добавление по номеру), `docs/contracts.md` («Network and reconnect»).
- Разведка `pathfinder` по коду (отчёт в этой сессии, выборочно перепроверен чтением): `internal/netconf/netconf.go`, `internal/cli/network_flags.go`, `internal/tgclient/network.go`, `internal/tgclient/connection.go`, `internal/cli/users_cmd.go`, `internal/tgclient/users_read.go`, `internal/tgclient/peers.go:68`, `internal/chatref/chatref.go`, `internal/daemon/users.go`, `internal/tgclient/updates_events.go:159`, `internal/daemon/events.go`, `internal/cli/channels_profile_cmd.go` (образец команды чтения `message readers`), `internal/daemon/access_test.go:63` (карта классов), `internal/tgclient/readonly_test.go:23` (`readFiles`), `internal/cli/client.go:95` (код RPC в код выхода), `internal/exitcode/exitcode.go` (коды 0 до 10).
- Сборка ветки и `tg schema` (бинарь собран из этого worktree в scratchpad): есть `user profile|block|unblock`, `contact list|add|delete|search` (у `search` флаги `limit`, `local`), `daemon start` с `--transport` и `--proxy`, `message readers`, `account profile`; команд `chat stats`, `chat boosts`, `message transcribe` нет.
- gotd v0.166.0 (`go.mod`), прочитано в `GOMODCACHE`: есть `MessagesTranscribeAudioRequest{Peer, MsgID}`, ответ `MessagesTranscribedAudio{Pending, TranscriptionID, Text, TrialRemainsNum, TrialRemainsUntilDate}`, `UpdateTranscribedAudio{Pending, Peer, MsgID, TranscriptionID, Text}`; `StatsGetBroadcastStatsRequest`, `StatsGetMegagroupStatsRequest`, `StatsLoadAsyncGraphRequest{Token, X}`, `StatsGraph{JSON, ZoomToken}`, `StatsGraphAsync`, `StatsGraphError`; `PremiumGetBoostsStatusRequest`, `PremiumBoostsStatus{Level, CurrentLevelBoosts, Boosts, GiftBoosts, NextLevelBoosts, PremiumAudience, BoostURL, MyBoost}`, `PremiumGetBoostsListRequest`; `ContactsResolvePhoneRequest{Phone}`. `telegram/invoke.go:90`: ошибки `STATS_MIGRATE_X` и `FILE_MIGRATE_X` gotd сам переводит на нужный DC (`invokeSub`). `telegram/dcs/websocket.go`: `WebsocketOptions.DialOptions` принимает свой `http.Client`; у WSS нет `MediaOnly` и `CDN`.
- Соседний поток волны 4: worktree `tg-cli-history_export` (`feature/20261115_1747_history_export`) от того же `82d4c6a`; его плана на момент записи нет, пересечения в общих файлах не проверены.
- Not verified: `just check` целиком не запускался (шаг 1). Не проверено, какую ошибку Telegram даёт `messages.transcribeAudio` аккаунту без Premium после исчерпания пробных попыток (ожидается `PREMIUM_ACCOUNT_REQUIRED` или `TRANSCRIPTION_FAILED`), тратит ли повторный вызов по тому же сообщению пробную попытку, приходит ли `UpdateTranscribedAudio` через менеджер обновлений gotd в общем потоке, видят ли статистику администраторы без права `view_stats`, отдаёт ли `premium.getBoostsStatus` данные не администратору, как часто Telegram позволяет `contacts.resolvePhone`, требует ли `listMigrations` номера подряд.

## Goal

Агент получает текст голосового сообщения и кружка средствами Telegram и понятный структурный отказ, когда платформа не даёт транскрипцию. Агент читает статистику канала или супергруппы, отдельные графы и состояние boosts, ничего не меняя. Daemon сам переходит с TCP на WSS, когда TCP не проходит, никогда не уходя мимо заданного прокси, и умеет подключаться к WSS через HTTP-прокси. Агент находит пользователя по номеру телефона без добавления в контакты.

## Problem and contract

Тип плана: product (новые команды, новый вид прокси, новое значение транспорта, одна миграция, новое поле конверта ошибки).

### Что уже есть (по коду и `tg schema` на 2026-11-15)

| Пункт | Состояние | Где |
| --- | --- | --- |
| 15.2, контакты по именам | уже есть: `contact list` (полный список, сортировка по имени и фамилии), `contact search <запрос> --local` ищет по FTS5 в `people_fts` по имени, фамилии, username, цифрам телефона и bio только среди контактов; без `--local` идёт `contacts.search` с фильтром чатов | `internal/cli/users_cmd.go:37-51`, `internal/daemon/users.go:236-275`, `:372-393`, `internal/store/people.go:81-150`, `internal/store/telegram.go:358` |
| 15.2, имя контакта как ссылка на чат | нет и не планируется: голый текст разбирается как username (`chatref.parseUsername`), имя неоднозначно, а ошибка в записи уводит сообщение не тому человеку; рецепт `contact search --local` → id описывается в навыке | `internal/chatref/chatref.go` |
| 15.2, поиск по номеру без добавления | частично: ссылка `+79…` находит только номер из контактов или из кэша пиров (`peers.ResolvePhone` в gotd → `contacts.getContacts`), иначе код 7; `contacts.resolvePhone` и `contacts.importContacts` в коде не вызываются; контакт при этом не добавляется | `internal/tgclient/peers.go:68-70`, `internal/chatref/chatref.go:85`, `:152-165` |
| 17.4 | нет: ни `stats.*`, ни `premium.*`; права администратора читаются только для внутренних проверок (`ChatOwnAdminRights`) | `internal/tgclient/chat_admin.go:404-442` |
| 18.1 | нет: ни `messages.transcribeAudio`, ни `UpdateTranscribedAudio`; признак Premium есть в `account profile` и `user profile` (`premium`); `media_type` различает `voice` и `video_note` | `internal/rpc/channels_profile.go:40`, `internal/tgclient/message.go:114-158` |
| 19.1, WSS | частично: `--transport tcp|wss` и SOCKS5 для обоих, MTProxy только с TCP, настройка запоминается в профиле; автоматического перехода нет по решению Q6 плана `20261115_0241_net_transport` («обход прокси невозможен»); HTTP-прокси отвергается разбором (`http://` в отрицательных тестах) | `internal/netconf/netconf.go`, `internal/cli/network_flags.go:25-117`, `internal/tgclient/network.go:15-70`, `docs/contracts.md:39` |

Для 15.2 код пишется только для поиска по номеру. Часть «по именам» уже сделана; план предлагает правку статуса (шаг 6): разбить 15.2 на подпункты 15.2.1 «контакты по именам: `contact list`, `contact search --local`» со статусом `[0]` (сделано в 7.5 и 10.7, на живом аккаунте не проверено) и 15.2.2 «поиск по номеру без добавления» по итогам этого плана.

### Контракт для рекомендованных вариантов

Другой ответ на вопрос меняет отмеченные места.

**Транскрипция (18.1).**

- Команда: `tg message transcribe <чат> <id> [--wait ДЛИТЕЛЬНОСТЬ]` или `tg message transcribe <ссылка t.me на сообщение>`. Класс безопасности `read` (Q5), метод JSON-RPC `message.transcribe`, класс доступа `chat-ref`: закрытый чат отвечает `not_found` до Telegram.
- Daemon получает сообщение по id (существующий путь `message get`), проверяет `media_type`: только `voice` и `video_note`, иначе код 2 `message has no voice or round video` без вызова `messages.transcribeAudio`.
- Premium до запроса не проверяется (Q2): без Premium Telegram даёт недельные пробные попытки. Отказ Telegram переводится в структурную ошибку (Q1): код выхода 5, в конверте `error.code: "telegram"` и новое необязательное поле `error.reason` со значением `premium_required` (ошибка Premium или исчерпаны пробные попытки) или `transcription_failed`; при пробном лимите добавляются `error.trial_remains` и `error.trial_resets_at` (RFC 3339), если Telegram их дал. Точные имена ошибок Telegram фиксирует шаг 1.
- Ожидание (Q3): если ответ `pending`, Daemon ждёт `UpdateTranscribedAudio` по этому сообщению до `--wait` (по умолчанию 30s, от 0 до 90s; вне диапазона код 2). Не дождался: успешный ответ с `pending: true`, код 0, подсказка в stderr повторить команду. Тайм-аут метода 100 секунд.
- Хранение (Q4): миграция `0008_transcriptions.sql`, таблица `transcriptions` (`chat_id`, `message_id`, `transcription_id`, `text`, `pending`, `updated_at`, ключ `chat_id, message_id`). Готовая транскрипция отдаётся из базы без запроса к Telegram (`cached: true`), чтобы повтор не тратил пробные попытки. Обновление `UpdateTranscribedAudio` пишется в таблицу только для строки, которую создал запрос этого Daemon. В FTS не индексируется.
- Ответ `TranscriptionResult`: `chat_id`, `id`, `transcription_id`, `text` (`untrusted`), `pending`, `cached`, `trial_remains` (если есть). Событие в `message watch` не публикуется.
- Транскрипция не уходит во внешние сервисы: только запрос к Telegram (решение `20261115_1800`, пункт 6.8 `[Wont]`).

**Статистика и boosts (17.4).**

- Команды (Q8): `tg chat stats <чат> [--graph ИМЯ]` и `tg chat boosts <чат>`. Обе класса `read`, методы `chat.stats` и `chat.boosts`, класс доступа `chat-ref`. Права (Q7): нужен только видимый по `allow` и `block` чат; список `admin` не требуется, потому что ничего не пишется. Отказ Telegram (`CHAT_ADMIN_REQUIRED`, `BROADCAST_REQUIRED`, `MEGAGROUP_REQUIRED`, ошибки нехватки участников) даёт код 5 с текстом причины. Обычная группа (не супергруппа) и личный чат дают код 2 до Telegram.
- `chat stats` по типу чата вызывает `stats.getBroadcastStats` (канал) или `stats.getMegagroupStats` (супергруппа). `STATS_MIGRATE_X` обрабатывает gotd. Ответ `ChatStats`: `chat_id`, `kind` (`broadcast` или `megagroup`), `period` (`min_date`, `max_date`), числовые показатели с текущим и предыдущим значением (`followers`, `views_per_post`, `shares_per_post`, `reactions_per_post`, `views_per_story`, `shares_per_story`, `reactions_per_story`, `enabled_notifications_percent` у канала; `members`, `messages`, `viewers`, `posters` у супергруппы), `recent_posts` (id сообщения или истории, просмотры, пересылки, реакции) у канала, `top_posters`, `top_admins`, `top_inviters` (id пользователя, имя `untrusted`, счётчики) у супергруппы, `graphs` (список `{name, state}`, `state`: `ready`, `async`, `error`).
- Графы (Q6): без `--graph` данные графов не печатаются. `--graph ИМЯ` (имена из `graphs`, snake_case от поля TL: `growth`, `followers`, `top_hours`, `languages` и так далее) печатает один граф: `{chat_id, name, data}`, где `data` это JSON графа Telegram как объект (`untrusted`, в нём подписи); граф `async` догружается одним `stats.loadAsyncGraph`. Неизвестное имя код 2 со списком допустимых.
- `chat boosts` вызывает `premium.getBoostsStatus`: `level`, `current_level_boosts`, `boosts`, `gift_boosts`, `next_level_boosts`, `premium_audience_percent`, `boost_url`, `my_boost`. Список тех, кто поддержал, не читается (Q8).
- Статистика в базу не пишется, людей из `top_*` Daemon сохраняет в `people` существующим путём, как участников.

**Сеть (19.1).**

- HTTP-прокси (Q10): `--proxy http://[user:password@]host:port` и то же в `TG_CLI_PROXY`. Допустим только с `--transport wss` и `auto`; с `tcp` код 2 `an HTTP proxy works only with wss or auto`. WSS идёт через `http.Transport{Proxy: http.ProxyURL(адрес)}`, то есть туннелем `CONNECT` к `*.web.telegram.org:443`. Переменные окружения `HTTP_PROXY` и `HTTPS_PROXY` по-прежнему не читаются. `https://`-прокси (TLS до прокси) не поддерживается: схема `https://t.me/proxy` уже занята MTProxy. Пароль прокси хранится в существующем файле `<profile>/proxy` (0600), `Redacted` показывает `http://host:port`.
- Автопереключение (Q9): новое значение `--transport auto`, запоминается в профиле, как остальные. По умолчанию остаётся `tcp`, поведение существующих профилей не меняется. Резолвер `auto` сначала подключается по TCP; ошибка соединения или рукопожатия транспорта по TCP сразу ведёт к попытке по WSS для того же DC. Удачное WSS-подключение закрепляет WSS до перезапуска Daemon (без качелей). Если TCP-соединение установлено, а основное соединение не стало `ready` за 10 секунд, Daemon помечает TCP плохим и переподключается по WSS; реализуемость этой ветки проверяет шаг 1, без неё остаётся только переход по ошибке соединения, и это пишется в справочник.
- Прокси при `auto` (инвариант 3): тот же прокси для обоих транспортов. SOCKS5 используется и для TCP, и для WSS; с HTTP-прокси `auto` сразу работает как `wss`; MTProxy с `auto` даёт код 2, как сейчас с `wss`. Прямого подключения при заданном прокси нет никогда.
- `daemon status` (поля `ConnectionInfo`): `transport` показывает выбранное (`auto`), новое поле `active_transport` (`tcp` или `wss`, пусто до первого подключения) и счётчик `transport_switches`.
- Это уточняет решение Q6 плана `20261115_0241_net_transport`: переход разрешён только между транспортами по тому же пути через прокси и только по явному `auto`. Записывается новым решением в `.agents/decisions/` (шаг 6).

**Поиск по номеру (15.2).**

- Команда (Q11): `tg user profile <+номер>` ищет номер среди контактов, как сейчас, а если не нашёл, вызывает `contacts.resolvePhone` и затем читает профиль. Контакт не добавляется, `contacts.importContacts` не вызывается, номер владельца не раскрывается. Остальные команды с телефонной ссылкой не меняются.
- Ответ прежний `UserProfile`; человек сохраняется в `people` с `is_contact = 0`. `PHONE_NOT_OCCUPIED` и скрытый настройками приватности номер дают код 7. Найденный пользователь подчиняется фильтру `allow` и `block`, как любой другой.
- Предел (Q12): не больше 20 поисков номера через `contacts.resolvePhone` за скользящие 24 часа на профиль (счётчик в памяти Daemon, сброс при перезапуске); сверх предела код 6 с `retry_after_seconds` до освобождения окна, без запроса к Telegram. Поиск среди контактов в предел не входит.

**Релиз и откат.** Один слой; откат ветки. Таблица `transcriptions` после отката остаётся в базе и не мешает старому коду (номер миграции 0008 старый код не знает; шаг 1 проверяет, не отвергает ли `store` базу с неизвестной миграцией). Профиль с `transport` = `auto` или `proxy` = `http://…` после отката не разбирается старым кодом: перед откатом владелец запускает `daemon start --transport tcp --proxy none` (или прежний прокси).

## Файлы потока

Новые файлы, которыми владеет поток (другие потоки их не трогают):

- `internal/tgclient/network_auto.go`, `internal/tgclient/network_auto_test.go`: резолвер `auto` (TCP, затем WSS, закрепление), учёт `active_transport` и `transport_switches`.
- `internal/tgclient/transcribe.go`, `internal/tgclient/transcribe_test.go`: `messages.transcribeAudio`, разбор ответа и ошибок, приёмник `UpdateTranscribedAudio` (обратный вызов, без публикации в поток событий).
- `internal/tgclient/stats_read.go`, `internal/tgclient/stats_read_test.go`: `stats.getBroadcastStats`, `stats.getMegagroupStats`, `stats.loadAsyncGraph`, `premium.getBoostsStatus`, перевод графов и показателей.
- `internal/tgclient/phone_read.go`, `internal/tgclient/phone_read_test.go`: `contacts.resolvePhone`.
- `internal/rpc/transcribe.go`, `internal/rpc/stats.go`: константы методов, параметры, результаты, проверка параметров.
- `internal/daemon/transcribe.go`, `internal/daemon/transcribe_test.go`, `internal/daemon/stats.go`, `internal/daemon/stats_test.go`, `internal/daemon/phone_limit.go`, `internal/daemon/phone_limit_test.go`, `internal/daemon/small_features_fake_test.go` (заглушки `fakeTelegram` для новых методов).
- `internal/store/transcriptions.go`, `internal/store/transcriptions_test.go`, `internal/store/migrations/0008_transcriptions.sql`.
- `internal/cli/transcribe_cmd.go`, `internal/cli/transcribe_cmd_test.go`, `internal/cli/stats_cmd.go`, `internal/cli/stats_cmd_test.go`.
- `.agents/decisions/20261115_HHMM_daemon_falls_back_from_tcp_to_wss_only_through_the_same_proxy.md` (имя уточняется в шаге 6).

Общие файлы, каждая правка одной локальной вставкой (новые строки рядом, без переформатирования соседних):

| Файл | Вставка |
| --- | --- |
| `internal/netconf/netconf.go` | `Auto Transport = "auto"`, `HTTP ProxyKind = "http"`; `case u.Scheme == "http":` в `ParseProxy`; две проверки в `Validate` (HTTP с `tcp`, MTProxy с `auto`) |
| `internal/netconf/netconf_test.go` | `http://` из отрицательных случаев в положительные, новые случаи в конце таблицы |
| `internal/tgclient/network.go` | ветка `netconf.HTTP` (WSS через `http.ProxyURL`) и вызов резолвера `auto` в `resolverForDial` |
| `internal/tgclient/connection.go` | заполнение `ActiveTransport` и `TransportSwitches` в `connectionState`; хук «не стало `ready` за 10 секунд» (если шаг 1 подтвердит) |
| `internal/tgclient/updates_events.go` | одна строка `d.OnTranscribedAudio(...)` в `updateDispatcher` |
| `internal/tgclient/client.go` | сеттер приёмника транскрипций рядом с `SetEventSink` |
| `internal/tgclient/peers.go` | откат на `resolvePhone` только для пути `user profile` (точку вставки уточняет шаг 1; общий `resolve` для остальных команд не меняется) |
| `internal/tgclient/readonly_test.go` | `"stats_read.go"`, `"phone_read.go"`, `"transcribe.go"` в конец `readFiles` |
| `internal/rpc/api.go` | два поля `ActiveTransport`, `TransportSwitches` в конце `ConnectionInfo` |
| `internal/rpc/message.go` | при варианте Q1 = A: необязательные `Reason`, `TrialRemains`, `TrialResetsAt` в данных ошибки (место уточняет шаг 1) |
| `internal/cli/client.go` | перенос `reason` и полей пробного лимита из `rpc.Error.Data` в конверт ошибки |
| `internal/output` (структура конверта ошибки) | необязательное поле `reason` и поля пробного лимита (место уточняет шаг 1) |
| `internal/cli/network_flags.go` | `auto` в допустимых значениях `--transport`, проверка HTTP-прокси с транспортом |
| `internal/cli/daemon_cmd.go` | `auto` в `Choices` флага `--transport`, текст помощи |
| `internal/cli/read_cmd.go` | `a.messageTranscribeCommand()` в `cmd.AddCommand` группы `message` |
| `internal/cli/chat_cmd.go` | `a.chatStatsCommands()...` в группу `chat` (место уточняет шаг 1) |
| `internal/cli/cli_test.go` | заглушки новых методов в подменном Daemon, если тесту они нужны |
| `internal/cli/untrusted_schema_test.go` | строки для `text`, `data`, имён в `top_*` |
| `internal/daemon/telegram.go` | одна строка на встроенный интерфейс (`transcribeTelegram`, `statsTelegram`, `phoneTelegram`) в конце `Telegram` |
| `internal/daemon/daemon.go` | `d.handle(...)` для `message.transcribe`, `chat.stats`, `chat.boosts`; `SetMethodTimeout(message.transcribe, 100*time.Second)`; подключение приёмника транскрипций рядом с `SetEventSink` |
| `internal/daemon/users.go` | вызов предела `phone_limit` перед откатом на `resolvePhone` в `user.profile` |
| `internal/daemon/access_test.go` | три строки в карте `classes` (`chat-ref`) |
| `internal/daemon/connection.go` | перенос двух новых полей в `fillConnection` |
| `docs/cli/messages-read.md`, `docs/cli/chats.md`, `docs/cli/daemon-and-auth.md`, `docs/cli/users-and-contacts.md`, `docs/cli/overview.md` | свой раздел или абзац в шаге 6 |
| `AGENTS.md`, `docs/contracts.md`, `FEATURES.md`, `README.md`, `docs/skill/tg-cli/SKILL.md`, `.agents/decisions/_TOC.md` | своя строка или свой абзац в шаге 6 |

Изменения общих реестров (полный список):

1. Методы JSON-RPC: `message.transcribe` (тайм-аут 100 с), `chat.stats`, `chat.boosts`. Поиск по номеру идёт через существующий `user.profile`.
2. Интерфейс `daemon.Telegram`: три встроенных интерфейса (сигнатуры уточняет шаг 1).
3. Классы доступа `TestRegisteredMethodsHaveAccessClass`: три метода, все `chat-ref`.
4. `readFiles`: `stats_read.go`, `phone_read.go`, `transcribe.go`.
5. Команды CLI и схема: `message transcribe` (`read`), `chat stats` (`read`), `chat boosts` (`read`); значение `auto` у `daemon start --transport`; поле `reason` в конверте ошибки.
6. `access.json`: новых ключей нет (Q5, Q7).
7. Миграции: `0008_transcriptions.sql`.
8. Коды выхода: новых нет (Q1 = A).
9. Обновления: `UpdateTranscribedAudio` в диспетчере, в поток `message watch` не попадает.

## Scope

- В плане: 18.1, 17.4 (статистика канала и супергруппы, графы по одному, состояние boosts), 19.1 (`auto` и HTTP-прокси для WSS), 15.2 в части поиска по номеру, правка статуса 15.2 по именам, схема, тесты на подменном клиенте, документы.
- Вне плана: статистика отдельного сообщения и истории (`stats.getMessageStats`, `stats.getStoryStats`), список поддержавших (`premium.getBoostsList`, если Q8 = A), транскрипция произвольного файла (`messages.transcribeAudioFile`), оценка транскрипции (`messages.rateTranscribedAudio`), транскрипция в локальном поиске (если Q4 = A), событие транскрипции в `message watch`, перевод (18.2), имя контакта как ссылка на чат, добавление контакта по номеру (`contacts.importContacts`, решение Q4 плана `users_contacts` в силе), `https://`-прокси, цепочки прокси, автопереход с прокси на прямое подключение, TCP через HTTP-прокси (если Q10 = A), сохранение выбранного `auto` транспорта между запусками, признак Premium в `auth status`, живые проверки.
- Затрагиваются: файлы из раздела «Файлы потока».

## Main invariant

1. При заданном прокси ни один запрос Daemon не уходит в Telegram напрямую, в том числе после автопереключения; без `auto` транспорт не меняется сам.
2. Профили с `tcp`, `wss`, SOCKS5 и MTProxy работают как сегодня: `telegram.Options` без новых настроек совпадает с сегодняшним, разбор старых значений тот же.
3. Новые команды только читают: `readFiles` с новыми файлами проходит `TestReadFilesCallNothingThatWrites`, `contacts.importContacts` и любые записи в аккаунт не вызываются.
4. Закрытый списками доступа чат отвечает как неизвестный для `message transcribe`, `chat stats`, `chat boosts`, до запроса к Telegram; найденный по номеру пользователь подчиняется тем же спискам.
5. Повторная транскрипция готового сообщения не обращается к Telegram.
6. Остальные команды, ответы и коды выхода не меняются; поле `error.reason` необязательное и не появляется в старых ошибках.
7. `internal/cli` не импортирует `tgclient`, gotd, `store`.
8. Ни тест, ни проверка по умолчанию не обращаются к живому Telegram и не открывают сокеты наружу; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: `auto` маскирует проблему сети и тихо меняет путь. Стратегия: только по явному `auto`; `active_transport` и `transport_switches` в статусе; строка в журнале Daemon при переключении (без адресов прокси); тест, что при SOCKS5 оба транспорта идут через прокси и прямых соединений 0.
- Риск: блокировка по DPI пропускает TCP-соединение, но рвёт MTProto, и резолвер ошибку не видит. Стратегия: ветка «не `ready` за 10 секунд» через состояние соединения; если gotd не даёт переподключить клиент изнутри, шаг 1 фиксирует это, ветка не делается, ограничение пишется в справочник и в вопрос менеджеру.
- Риск: `coder/websocket` через `http.ProxyURL` не шлёт `CONNECT` или читает окружение. Стратегия: тест с локальным HTTP-прокси на `httptest`, который принимает только `CONNECT` и считает туннели, плюс `t.Setenv("HTTPS_PROXY", …)` с проверкой, что окружение не используется.
- Риск: повторный `messages.transcribeAudio` тратит пробные попытки аккаунта без Premium. Стратегия: готовый результат из таблицы; повтор к Telegram только для строки `pending` без пришедшего обновления; неизвестное поведение сервера записывается в «не проверено на живом аккаунте».
- Риск: `UpdateTranscribedAudio` не доходит до диспетчера (менеджер обновлений может отбросить его как не связанный с `pts`). Стратегия: шаг 1 читает `updates.Manager`; если обновление не доходит, ожидание заменяется повтором запроса раз в 3 секунды до `--wait` (ограничение пишется в справочник).
- Риск: массовый поиск номеров похож на перебор и ведёт к ограничению или блокировке аккаунта. Стратегия: предел Q12, один номер за вызов, предупреждение в навыке; `contacts.importContacts` не используется.
- Риск: граф Telegram большой (десятки килобайт JSON). Стратегия: графы только по одному через `--graph`, предел кадра 1 MiB уже действует; в справочнике пример `--fields`.
- Риск: миграция 0008 при свободном 0007 (если соседний поток `history_export` не займёт 0007) или конфликт номера. Стратегия: шаг 1 проверяет, требует ли `listMigrations` номера подряд; при требовании номер согласует менеджер при слиянии, файл переименовывается без изменения содержимого.
- Риск: правки `internal/rpc/api.go`, `daemon.go`, `telegram.go`, `access_test.go`, `read_cmd.go` пересекаются с потоком `history_export`. Стратегия: вставки отдельными строками в конце блоков; при конфликте сохраняются обе стороны.
- Риск: новое поле конверта ошибки ломает агента, который строго разбирает `error`. Стратегия: поле необязательное, только в новых отказах; описано в `overview.md`; тест, что старые ошибки байт в байт прежние.

## Decision log

| Date | Decision | Reason | Confirmed |
| --- | --- | --- | --- |
| 2026-11-15 | 15.2 в части имён не реализуется заново: `contact list` и `contact search --local` уже ищут по имени; план только меняет статус | Проверено по коду и `tg schema` | no |
| 2026-11-15 | Имя контакта не становится ссылкой на чат | Имя неоднозначно, ошибка в записи уводит сообщение не тому человеку; рецепт через `contact search --local` закрывает задачу | no |
| 2026-11-15 | `contacts.importContacts` не используется; решение Q4 плана `20261114_2244_users_contacts` в силе | Импорт добавляет контакт и раскрывает номер владельца; `contacts.resolvePhone` находит без добавления | no |
| 2026-11-15 | Статистика и boosts в базу не пишутся, миграция только для транскрипций | Статистика быстро устаревает и читается заново за один запрос | no |
| 2026-11-15 | `STATS_MIGRATE` не обрабатывается в коде проекта | gotd v0.166.0 сам переводит запрос на DC статистики (`telegram/invoke.go:90`) | no |
| 2026-11-15 | Живых проверок в плане нет, непроверенное идёт в список «не проверено на живом аккаунте» `FEATURES.md` | Владелец отменил живые проверки для волны 4 (поручение планировщику) | yes |
| 2026-11-15 | Q1: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q2: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q3: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q4: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q5: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q6: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q7: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q8: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q9: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q10: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q11: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |
| 2026-11-15 | Q12: принят вариант A | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |

## Step 0. Fix decisions before changes

- [x] Ответы на Q1 до Q12 записаны в Decision log (все по рекомендации, контракт не меняется).
- [x] Если Q9 или Q10 приняты не по рекомендации, план возвращён планировщику: меняется главный инвариант 1.
- [ ] Исполнитель Go-кода (Codex Terra) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [x] Ветка `feature/20261115_1747_small_features` создана от `main` (`82d4c6a`) в worktree `../tg-cli-small_features`.

## Step 1. Audit and baseline (исполнитель Codex Terra)

Отчёт в `var/agents/plans/20261115_1747_small_features/audit.md` (в git не входит), итог в «Known facts».

- [ ] `just check` и `just build` на чистом дереве ветки; результат в Known facts.
- [ ] Подтвердить таблицу «Что уже есть»: 15.2 (имена), отсутствие `stats.*`, `premium.*`, `transcribeAudio`, `resolvePhone`, HTTP-прокси и `auto`.
- [ ] Транскрипция: по TL-схеме и документации Telegram (core.telegram.org/api/transcribe) список ошибок `messages.transcribeAudio` (Premium, пробный лимит, неподходящее медиа, слишком длинное аудио); тратит ли повтор по тому же сообщению пробную попытку; доходит ли `UpdateTranscribedAudio` через `updates.Manager` gotd до `updateDispatcher` (чтение исходников `telegram/updates`).
- [ ] Статистика: где брать тип чата (канал или супергруппа) без лишнего запроса; ошибки `stats.*` для не администратора и для малой супергруппы; формат `StatsGraph.JSON` и `ZoomToken`; проходит ли `invokeSub` через дроссель (`Options.Middlewares`) или мимо него.
- [ ] Boosts: доступен ли `premium.getBoostsStatus` не администратору (по документации).
- [ ] Сеть: может ли резолвер или хук состояния заставить gotd переподключиться по другому транспорту без перезапуска клиента; как `coder/websocket` v1.8.15 использует `HTTPClient` при `wss://` (туннель `CONNECT`); как прокинуть Basic-авторизацию прокси (`url.User` в `ProxyURL`).
- [ ] Номер: где в `user.profile` вставить откат на `resolvePhone`, не меняя общий `resolve`; ошибки `contacts.resolvePhone` (`PHONE_NOT_OCCUPIED`, приватность) и как они сейчас переводятся в коды.
- [ ] Конверт ошибки: где структура `error` в `internal/output`, как добавить необязательное поле, не меняя старые ошибки; не противоречит ли это тестам схемы.
- [ ] Миграции: требует ли `listMigrations` номера подряд; что делает старый код с базой, где есть миграция с большим номером.
- [ ] Где регистрируются подкоманды `chat`; нужен ли `cli_test.go` подменный метод для новых команд; обязателен ли `schema_fields_test.go` для новых команд.
- [ ] Записать итоги в Known facts, непокрытое в Risks и Open questions.

## Implementation steps

Каждый шаг кода выполняет Codex Terra, проверяет Claude (тесты, дифф, `just check`), коммитит Claude. Шаги 2 до 5 независимы и идут по порядку одним кодером потока.

- [ ] 2. Сеть (19.1): `netconf` (`auto`, `http`, `Validate`), `network.go` (WSS через `http.ProxyURL`, `Proxy: nil` для остальных), `network_auto.go`, поля `ConnectionInfo` и `fillConnection`, `network_flags.go` и `daemon_cmd.go`. Тесты: разбор `http://` с паролем и без, `http://` с `tcp` код 2, MTProxy с `auto` код 2, `Redacted` без пароля; локальный HTTP-прокси на `httptest` принимает `CONNECT` и считает туннели, WSS идёт через него, `HTTPS_PROXY` из окружения не используется; `auto`: отказ TCP-слушателя ведёт к WSS для того же DC, `active_transport` = `wss`, `transport_switches` = 1, следующий вызов сразу WSS; при SOCKS5 оба транспорта через локальный SOCKS5, прямых соединений 0; без новых настроек `telegram.Options` прежний. Проверка: `go test -race ./internal/netconf/... ./internal/tgclient/... -run 'Network|Resolver|Auto|Proxy'`.
- [ ] 3. Номер (15.2): `phone_read.go`, откат в пути `user.profile`, `phone_limit.go`, вызов в `daemon/users.go`. Тесты на подменном invoker и подменных часах: номер из контактов не вызывает `resolvePhone`; номер вне контактов вызывает его один раз и не вызывает `importContacts`; `PHONE_NOT_OCCUPIED` код 7; 21-й поиск за 24 часа код 6 без вызова Telegram, после сдвига часов снова разрешён; закрытый `allow` пользователь `not_found`; человек в базе с `is_contact = 0`; другие команды с телефонной ссылкой `resolvePhone` не вызывают.
- [ ] 4. Статистика (17.4): `rpc/stats.go`, `stats_read.go`, `daemon/stats.go`, `cli/stats_cmd.go`, регистрация, `access_test.go`, `readFiles`. Тесты на подменном invoker: канал вызывает `getBroadcastStats`, супергруппа `getMegagroupStats`, обычная группа и личный чат код 2 без вызова; `CHAT_ADMIN_REQUIRED` код 5; закрытый чат `not_found` без вызова; `graphs` перечисляет имена и состояния; `--graph` с `StatsGraphAsync` вызывает `loadAsyncGraph` один раз; `StatsGraphError` даёт код 5 с текстом; неизвестное имя код 2 со списком; `boosts` переводит все поля; люди из `top_*` сохранены.
- [ ] 5. Транскрипция (18.1): миграция `0008`, `store/transcriptions.go`, `rpc/transcribe.go`, `tgclient/transcribe.go` и строка диспетчера, приёмник в `client.go`, `daemon/transcribe.go` (ожидание по ключу чат и id с отменой по контексту), поле `reason` в конверте, `cli/transcribe_cmd.go`. Тесты: сообщение без `voice` и `video_note` код 2 без `transcribeAudio`; готовый ответ сохраняется, повтор отдаёт `cached: true` без вызова; `pending` и обновление через 50 мс дают текст; `pending` без обновления до `--wait` даёт `pending: true` и код 0; обновление для чужой строки не пишется; ошибка Premium даёт код 5 и `error.reason: premium_required` с полями пробного лимита; старые ошибки без `reason`; закрытый чат `not_found`; `--wait` вне диапазона код 2; миграция применяется на пустой и на существующей базе.
- [ ] 6. Документы через `docwriter` и `glavred` по `docs_chain.md`: `docs/cli/messages-read.md` (`message transcribe`), `docs/cli/chats.md` (`chat stats`, `chat boosts`), `docs/cli/daemon-and-auth.md` (`auto`, `http://`, `active_transport`, ограничение ветки DPI, если она не сделана), `docs/cli/users-and-contacts.md` (поиск по номеру, предел), `docs/cli/overview.md` (`error.reason`), `docs/contracts.md` (абзацы «Network and reconnect», «Transcription», «Statistics»), таблицы пересобрать `go test ./internal/cli -run TestDocsGenerated -update-docs`; `FEATURES.md`: подпункты 15.2.1 и 15.2.2, 17.4.x, 18.1.x, 19.1.x, приёмочные строки, строки статуса `[0]` (сделано, на живом аккаунте не проверено), убрать 15.2, 17.4, 18.1, 19.1 из строки «Не начато, Could», добавить пункты в список «не проверено на живом аккаунте» (перечень ниже); `README.md` не меняется (документирует только проверенное вживую); `docs/skill/tg-cli/SKILL.md` (транскрипция и `pending`, статистика и `--graph`, когда выбирать `auto`, поиск по номеру и предел, рецепт «контакт по имени»); `AGENTS.md` (строка в Running про `auto` и `http://`; Security: поиск по номеру не чаще предела); решение по автопереключению в `.agents/decisions/` и строка в `_TOC.md`.
- [ ] 7. Итоговая проверка Claude: таблица «Verification», дифф только в файлах потока, нет временных файлов в `.agents/plans`, статус плана `done` или отклонения записаны.

Перечень для «не проверено на живом аккаунте» (шаг 6): транскрипция голосового и кружка у аккаунта с Premium и без, пробные попытки и их расход на повтор, приход `UpdateTranscribedAudio`, длинное аудио; статистика канала и супергруппы у администратора и не администратора, асинхронные графы, `STATS_MIGRATE` на чужой DC через WSS; `chat boosts` у администратора и подписчика; `auto` при реальной блокировке TCP, ветка «не `ready` за 10 секунд», WSS через реальный HTTP-прокси с паролем и без; `user profile <+номер>` для номера вне контактов, скрытого приватностью, пустого номера, и частота, при которой Telegram даёт FLOOD_WAIT.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/netconf/... ./internal/rpc/... ./internal/tgclient/... ./internal/store/... ./internal/daemon/... ./internal/cli/...` | зелёный | not_run |
| Прокси не обходится | тесты шага 2 | при SOCKS5 и HTTP прямых соединений 0, в том числе после переключения | not_run |
| Старые настройки сети не изменились | тесты шага 2 | `telegram.Options` без новых настроек совпадает с прежним, старые значения разбираются так же | not_run |
| Только чтение Telegram | `TestReadFilesCallNothingThatWrites` | новые файлы без вызовов записи | not_run |
| Повтор транскрипции из базы | тесты шага 5 | второй вызов не обращается к Telegram | not_run |
| Предел поиска номеров | тесты шага 3 | 21-й вызов код 6 без запроса | not_run |
| Полнота схемы, справочника и классов | `go test ./internal/cli`, `TestRegisteredMethodsHaveAccessClass` | разделы для `message transcribe`, `chat stats`, `chat boosts`, флагов `--wait`, `--graph`; классы у трёх методов | not_run |
| Таблицы справочника | `go test ./internal/cli -run TestDocsGenerated -update-docs`, затем `go test ./internal/cli -run TestDocsGenerated` | таблицы пересобраны, повторный запуск без флага зелёный | not_run |
| Миграция одна | `git diff --stat main -- internal/store/migrations` | только `0008_transcriptions.sql` | not_run |
| Сокетов наружу нет | просмотр тестов шага 2 | только `127.0.0.1` и `httptest` | not_run |

## Deploy / rollback

- [ ] Выкладки нет. После слияния перезапустить Daemon (`tg daemon stop`, `tg daemon start`); миграция 0008 применяется при старте.
- [ ] Откат: откат ветки. Перед откатом владелец возвращает профиль к `--transport tcp` или `wss` и прокси SOCKS5, MTProxy или `none`, иначе старый код не разберёт файлы `<profile>/transport` и `<profile>/proxy`. Таблица `transcriptions` остаётся в базе (поведение старого кода с неизвестной миграцией проверяет шаг 1).
- [ ] Кэшей, очередей и фоновых задач вне Daemon нет.

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261115_1747_small_features/`.
- [ ] Временные файлы не в git (`/var/` в `.gitignore`).

## Definition of done

- [ ] Код изменён только в рамках плана и раздела «Файлы потока».
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.
- [ ] Решение по автопереключению записано в `.agents/decisions/` и `_TOC.md`; `FEATURES.md`, `AGENTS.md`, справочник, контракты и навык обновлены; непроверенное перечислено в `FEATURES.md`.

## Known facts

- Коды выхода 0 до 10 (`internal/exitcode/exitcode.go`), 10 это `offline`. Конверт ошибки сейчас несёт `code` (строка по коду выхода) и `message`; `retry_after_seconds` у FLOOD_WAIT попадает только в текст (`internal/cli/client.go:110-118`) и в элементы пакетных ошибок (`internal/rpc/batch.go:21`).
- `rpc.Error.Data` (`internal/rpc/message.go:68`) уже есть и может нести поля причины.
- `user profile` принимает телефон (`userRef(args[0], true)`, `internal/cli/users_cmd.go:62`), `contact add` нет (`:72`).
- `ParseProxy` принимает `socks5://`, `tg://proxy`, `https://t.me/proxy`; `http://` отвергается; WSS собирается с `http.Transport{Proxy: nil, DialContext: dial}` (`internal/tgclient/network.go:64`), при SOCKS5 `dial` идёт через прокси.
- Фоновый процесс Daemon не получает `TG_CLI_PROXY` (`internal/daemon/launch_unix.go:47-49`) и читает прокси из файла профиля.
- `daemon status` отдаёт `transport`, `proxy`, `connection`, `dc`, `reconnects` (`rpc.ConnectionInfo`, `internal/rpc/api.go:91-97`); запрос ждёт связь до 15 секунд (`internal/daemon/connection.go:63`).
- Дроссель классифицирует всё, кроме отправок, как `General` (`internal/tgclient/throttle.go:17-47`).
- Общего механизма «ждать конкретное обновление» нет; есть только барьер догрузки (`internal/tgclient/connection.go:98-208`) и ожидание связи.
- Миграции `0001` до `0006` в `internal/store/migrations/`; 0007 предположительно займёт поток `history_export`.
- Ключи `access.json`: `allow`, `block`, `write`, `admin`, `create`, `profile`, `dialogs`, `sessions`, `files`, `downloads`; неизвестный ключ закрывает все чаты.

## Open questions

Каждый вопрос задаётся отдельно. Рекомендуемый вариант первый. Владелец делегировал решения менеджеру, он принимает рекомендуемые. Оценки времени даны для кодера Codex Terra вместе с тестами.

| # | Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| Q1 | 18.1. Как сообщать, что транскрипция недоступна (нет Premium, исчерпаны пробные попытки) | Определяет конверт и коды выхода, которые разбирает агент | A: код выхода 5, новое необязательное поле `error.reason` (`premium_required`, `transcription_failed`) и поля пробного лимита в конверте; B: новый код выхода 11 `premium_required`; C: успешный ответ `{available: false, reason}` с кодом 0 | A. Цена: одно необязательное поле в общем конверте, правка `internal/output` и `overview.md`, около 2 часов. B требует согласовать номер с соседним потоком и расширяет таблицу кодов ради одной команды. C агент пропустит, если смотрит только на код выхода | решён: A |
| Q2 | 18.1. Проверять ли Premium до запроса | Без Premium Telegram даёт недельные пробные попытки | A: не проверять, решает Telegram, отказ переводится по Q1; B: отказывать до запроса, если у своего аккаунта нет `premium` | A. Цена: на аккаунте без Premium и без попыток уходит один лишний запрос. B лишает агента пробных попыток | решён: A |
| Q3 | 18.1. Как ждать готовый текст | Telegram отвечает `pending` и досылает текст обновлением | A: Daemon ждёт обновление до `--wait` (по умолчанию 30s, от 0 до 90s), иначе `pending: true` и код 0, агент повторяет команду; B: всегда сразу `pending`, агент опрашивает сам; C: ждать без предела | A. Цена: свой механизм ожидания обновления по ключу (новый, около 3 часов). B заставляет агента опрашивать и тратит запросы. C держит слот RPC до тайм-аута 120 с | решён: A |
| Q4 | 18.1. Хранить ли транскрипции | Повтор может тратить пробные попытки; агенту полезен офлайн-поиск | A: таблица `transcriptions` (миграция 0008), повтор из базы, без индекса поиска; B: то же плюс индекс FTS, `search` находит текст голосовых; C: не хранить | A. Цена: одна миграция, около 2 часов. B добавляет правку общего поиска (`store/search.go`) и триггеров FTS, около 4 часов, пересечение с потоком `history_export` вероятно. C каждый повтор идёт в Telegram | решён: A |
| Q5 | 18.1. Нужно ли новое право в `access.json` | Запрос тратит пробные попытки, но ничего не пишет в чат | A: нет, класс `read`, нужен только видимый чат; B: новый ключ `transcribe: true` | A. Цена: агент может израсходовать недельные пробные попытки без ведома владельца. B добавляет ключ, который владелец должен знать, и правку `access.go`, около 1 часа; откат ветки с ключом закроет все чаты | решён: A |
| Q6 | 17.4. Как отдавать графы | Граф это большой JSON, часть графов асинхронные | A: в сводке только список графов с состоянием, `--graph ИМЯ` печатает один граф и догружает асинхронный; B: все графы сразу в ответе; C: графы не отдавать | A. Цена: два вызова для графа, около 2 часов на имена и догрузку. B даёт ответ в сотни килобайт и до десятка запросов `loadAsyncGraph` на вызов. C теряет большую часть статистики | решён: A |
| Q7 | 17.4. Какие права нужны для статистики | Статистика доступна только администраторам с правом `view_stats` | A: только видимый чат, `admin` в `access.json` не нужен, отказ Telegram код 5 с причиной; B: чат должен быть в `admin` | A. Цена: агент читает статистику любого чата, где аккаунт администратор, если чат виден. B смешивает «можно менять» и «можно читать», владелец будет добавлять каналы в `admin` ради чтения | решён: A |
| Q8 | 17.4. Какие команды и нужен ли список поддержавших | Список boosts это идентификаторы людей | A: `chat stats <чат> [--graph ИМЯ]` и `chat boosts <чат>` только с состоянием, без списка; B: то же и `chat boosts --list` (`premium.getBoostsList`, только администратор); C: одна команда `chat stats --boosts` | A. Цена: кто поддержал канал, агент не узнает. B добавляет курсор и людей в ответ, около 2 часов. C смешивает два запроса с разными правами | решён: A |
| Q9 | 19.1. Как Daemon переключается с TCP на WSS | Решение Q6 плана `net_transport` запретило автопереход, чтобы трафик не ушёл мимо прокси | A: новое значение `--transport auto` по явному выбору, сначала TCP, при сбое WSS для того же DC, тот же прокси для обоих, прямого пути при прокси нет, закрепление до перезапуска; по умолчанию остаётся `tcp`; новое решение уточняет Q6; B: `auto` по умолчанию для новых профилей; C: автоперехода не делать, только HTTP-прокси | A. Цена: новый резолвер и два поля статуса, около 5 часов; ветка DPI зависит от аудита. B меняет поведение без ведома владельца. C оставляет 19.1 сделанным наполовину | решён: A |
| Q10 | 19.1. Какой HTTP-прокси поддерживать | Решение `net_transport` вынесло HTTP CONNECT за рамки | A: `http://[user:password@]host:port` только для WSS (и `auto`, который с ним работает как WSS); с `tcp` код 2; B: ещё и TCP через `CONNECT` к адресам DC на 443; C: не делать | A. Цена: около 3 часов; пароль идёт до прокси открытым текстом (Basic), это пишется в справочник. B многие корпоративные прокси пропускают только TLS на 443, TCP через них ненадёжен, ещё около 2 часов. C не закрывает пункт | решён: A |
| Q11 | 15.2. Как искать по номеру без добавления в контакты | Определяет команду и охват | A: `user profile <+номер>` при промахе по контактам вызывает `contacts.resolvePhone`, остальные команды не меняются; B: отдельная команда `user resolve-phone <+номер>`; C: все команды с телефонной ссылкой откатываются на `resolvePhone` | A. Цена: `user profile` по номеру начинает делать до двух запросов, около 2 часов. B ещё одна команда ради одного запроса. C даёт писать незнакомым людям по номеру (запись всё равно ограничена `write`, но перебор номеров идёт любой командой); не рекомендую | решён: A |
| Q12 | 15.2. Ограничивать ли частоту поиска номеров | Перебор номеров похож на сбор базы и ведёт к блокировке аккаунта | A: не больше 20 поисков через `resolvePhone` за скользящие 24 часа на профиль, сверх предела код 6 без запроса; B: только общий дроссель и предупреждение в навыке | A. Цена: счётчик в памяти Daemon, около 1,5 часа; после перезапуска Daemon счёт начинается заново. B защищает только от FLOOD_WAIT, не от зацикленного агента | решён: A |
