# Step-by-step plan: догонка чатов после простоя Daemon

ID: 20261122_2245_chat_catchup_after_downtime
Created: 2026-11-22
Owner: Мязин Евгений
Branch: feature/20261122_2245_chat_catchup_after_downtime (worktree `../tg-cli-catchup`, от `feat/history-limits`)
PR: —

Тип плана: Product / refactoring (меняется наблюдаемое поведение: новая команда, новая настройка, новое значение `source`, новые поля ответа, миграция схемы).

## Status

- Current status: in_progress
- Last update: 2026-11-23
- Next safe step: волна C (шаги 8, 9, 10: проход дыры ручным `start`, RPC и CLI, экспорт) и два недостающих теста; шаги 1-7 готовы, коммиты `d44cc85` и `648a27d`. Владелец утвердил план и сказал «Делай» 2026-11-23; ответы на Q1-Q11 и подтверждение D-A1-D-A8 внесены; открытых вопросов нет.

## Зависимость

План опирается на работу ветки `feat/history-limits`, которой нет в `main` (проверено `git log main..HEAD` и `git status --short`, 2026-11-23: три коммита ниже, в рабочем дереве кроме этого плана только `.claude/settings.local.json`; `main` на `805ba3c`):

- `755fefe feat(history): stop full loads at a date or a count and add per-chat message limits`: `history_limit.*`, `MarkHistoryLimitReached`, `MessageBounds`, лимит чата (`chat limit`), правка `export_month.go`.
- `7f811a0 feat(sync): queue named chats, load new chats automatically, route all loads through the queue`: `chat sync queue <чат>...`, `queueNamedChats`, `startAuto`, `autoSyncNewChat`, `chatIsNew`, `new_chat_sync.messages`.
- `bd457af docs(skill): queue first, never poll Telegram, add talk-to-contact recipe`: правки навыка, которые план меняет дальше.

До начала:

- [x] правки навыка и запись changelog закоммичены их владельцем (`bd457af`, проверено 2026-11-23);
- [-] `feat/history-limits` влита в `main`: пропущено по решению менеджера 2026-11-23, слияние в `main` без команды владельца не делается; ветка плана создана от `feat/history-limits` (`bd457af`), где вся зависимость уже есть;
- [x] ветка плана `feature/20261122_2245_chat_catchup_after_downtime` создана 2026-11-23 в worktree `../tg-cli-catchup` от `feat/history-limits`. Слияние в `main` порядком: сначала `feat/history-limits`, затем эта ветка, по команде владельца.

## Context and sources

- Read (документы): `docs/contracts.md` (Deep history load, строки 12-17; Live event stream, строка 27; `message watch`, строка 28; Network and reconnect, строки 85-91; The full-data job, строка 178 и далее), `docs/cli/chats.md` (раздел «Loading a whole chat», `chat sync start`, `chat sync status`, `chat sync queue`, `chat state`), `docs/cli/overview.md:337-360` (настройки `background_sync.*`, `new_chat_sync.messages`), `docs/cli/settings.md:15`, `FEATURES.md` (10.12, 10.13, 10.16, 10.17, 10.20), `.agents/rules/plans.md`, `.agents/rules/decisions.md` (раздел Reading).
- Read (решения): `20261114_1000_live_events_through_gotd_updates_with_stored_cursor`, `20261120_1651_background_history_sync_runs_in_a_daily_window_and_is_off_by_default`, `20261122_1639_new_chat_gets_its_latest_100_messages_queued_automatically`, `20261122_1410_full_history_load_stops_at_a_date_or_a_message_count`.
- Read (код): `internal/store/sync_state.go`, `internal/store/account_state.go`, `internal/store/background_sync.go:45-70`, `internal/store/web.go:83-101`, `internal/store/migrations/0002_schema.sql`, `0005_folders_account_state.sql`, `0010_first_message_date.sql`, `internal/daemon/history_sync.go`, `internal/daemon/history_sync_depth.go`, `internal/daemon/history_sync_queue.go`, `internal/daemon/new_chat_sync.go`, `internal/daemon/background_sync.go`, `internal/daemon/chat_limit.go:140-192`, `internal/daemon/settings.go:41-175`, `internal/daemon/daemon.go:150-530, 810-906`, `internal/daemon/export_month.go:115-140`, `internal/tgclient/convert.go`, `internal/tgclient/client.go:205-260`, `internal/tgclient/history_page.go`, `internal/tgclient/throttle.go:112-140`, `internal/tgclient/updates_events.go:157-240`, `internal/tgclient/owner_state.go`, `internal/config/settings.go:17-140, 230-302`, `internal/config/config.go:36-37, 249`, `internal/config/settings_test.go`, `internal/cli/background_sync_config.go`, `internal/cli/daemon_profile_database.go:20-45`, `internal/rpc/api.go` (`Chat`, `HistoryPage`, `HistorySyncJob`, источники `:516-520`), `internal/rpc/dashboard.go:124-133`, `internal/rpc/export_month.go`, `internal/rpc/owner_state.go`.
- Verified by commands: `git log main..HEAD`, `git diff --stat main..HEAD`, `ls internal/store/migrations`, `rg -n 'sync_state' internal`, `rg -n 'SyncState\(|SaveHistoryPage|MarkHistoryLimitReached' internal`, `rg -n 'LastMessageDate|TopMessage' internal`, `rg -n 'WithBackground' internal`, `rg -n 'new_chat_sync.messages'`, `rg -n 'startAuto|startDepth|enqueueLocked|HistorySyncManual|HistorySyncBackground|cancelBackground' internal`, `rg -n 'background_sync.enabled' internal docs`, `bin/atlas query ... --scope decision`.
- Not verified: поведение `updates.Manager` после 10 суток простоя на живом аккаунте (владелец отказался от живых проверок по умолчанию); входят ли архивные диалоги в обход `messages.getDialogs` без `folder_id` (тот же обход у `chat sync queue`, по коду не видно); реальная частота `FLOOD_WAIT` на сотнях `messages.getHistory` подряд; реальная длительность догонки.

## Problem and why

Daemon не работал 10 дней, потом запущен. Пропущенное за простой сейчас догоняет только менеджер обновлений gotd (`updates.Manager`): он сам зовёт `getDifference` и `getChannelDifference`, состояние лежит в `account_state` (`docs/contracts.md:27`). Своей догонки у Daemon нет:

1. Когда менеджер не может закрыть пропуск, он шлёт `gap` (`channel_too_long`, `channel_gap`, `channel_inaccessible`, `too_long`; `store` при сбое записи), и пропущенное никто не догружает: агент читает историю сам (`internal/tgclient/updates_events.go:228-232`, решение `20261114_1000`, раздел Cost).
2. Загрузка истории идёт только вниз от `oldest_id` (`internal/daemon/history_sync.go:172-174`, `offset = *state.OldestID`); повторный `start` загруженного чата сообщений новее `newest_id` не берёт (`docs/cli/chats.md`, «Loading a whole chat»).
3. Автозагрузка 100 последних срабатывает только для чата без сообщений и без строки `sync_state` (`chatIsNew`, `internal/daemon/new_chat_sync.go:27-34`).
4. Поведение менеджера после 10 суток простоя на живом аккаунте не проверено.

Итог: после долгого простоя в активных чатах остаются дыры, о которых ни агент, ни база не знают.

## Goal

После простоя Daemon дольше 10 минут Daemon сам, при включённых `catchup.enabled` и `background_sync.enabled` (оба по умолчанию `true`), догружает в каждый видимый чат с сохранёнными сообщениями и новыми сообщениями в диалоге историю от верха вниз до момента остановки (с запасом 10 минут), не глубже 1000 сообщений на чат. Задачи догонки видны в `chat sync status` с `source: catchup`, стоят в очереди за ручными и впереди глубокой загрузки, в одной полосе с автозагрузкой нового чата в порядке прихода. Чат, где предел 1000 сработал раньше, получает отметку дыры `gap_from`/`gap_to`, видимую в `chat sync status` и `chat overview`; ручной `tg chat sync start` такого чата сначала проходит дыру: без флагов глубины целиком, с флагом до даты или до N загруженных сообщений, и тогда дыра сужается. Ручной запуск догонки: `tg chat sync catchup [--dry-run]`.

## Scope

In scope:

- метка `last_alive_at` в `account_state`: запись раз в минуту и при чистом выходе, только при входе, готовом клиенте и без `auth_lost` (Q4); чтение при старте;
- настройка `catchup.enabled` (bool, умолчание `true`, режим `restart`, переменная окружения); догонка работает только при включённых `catchup.enabled` и `background_sync.enabled` (Q5);
- выбор чатов для догонки по списку диалогов Telegram и локальной базе;
- новый вид задачи в `historySyncManager` с `source: catchup` (Q3): от верха чата вниз до границы по времени или до 1000 сообщений, пауза `pagePause` перед каждой страницей (Q6);
- очередь с приоритетами вместо FIFO: ручные, затем догонка вместе с автозагрузкой нового чата, затем фоновые (Q2, Q10);
- колонки `sync_state.gap_from`, `sync_state.gap_to` (миграция `0014`), правило `newest_id` при дыре, закрытие и сужение дыры;
- проход дыры ручным `chat sync start` (Q1) и его ограничение явными флагами `--messages`, `--days`, `--months`, `--since` (Q9);
- показ дыры в `chat sync start|status|cancel` и `chat overview`, учёт дыры в `export-month` (`sync.month_start_covered`);
- метод RPC и команда `tg chat sync catchup` с `--dry-run`, без `--since` (Q7);
- документы: `docs/cli/chats.md`, `docs/cli/overview.md`, `docs/cli/settings.md`, `docs/contracts.md` (включая исправление «off by default» на «on by default» для `background_sync.enabled`), `FEATURES.md` (10.21), навык `docs/skill/tg/`, решения в `.agents/decisions/`, запись `.agents/changelog/`.

Out of scope:

- Расширение «событие `gap` менеджера ставит догонку конкретного чата». Оценка: `gapEvent` уже несёт `chat_id` канала для `channel_too_long`, `channel_gap`, `channel_inaccessible` (`internal/tgclient/updates_events.go:157-166`), `handleEvent` в Daemon мог бы звать постановку догонки одного чата так же, как зовёт `autoSyncNewChat`. Нерешённое: граница по времени для такого чата (метки простоя нет), `too_long` без чата, повторные `gap` одного канала. Кандидат на отдельный план после этого.
- Догонка после долгого обрыва связи при работающем Daemon (Daemon жив, вход есть, сети нет). Метка по Q4 пишется по признакам «вход выполнен, клиент готов, нет `auth_lost`»; признак готовности клиента закрывается один раз и обрыв связи не отражает (см. Known facts), поэтому метка пишется и при обрыве, и при следующем старте простоя не видно. Такой обрыв догонку при старте не включит; его закроет отдельный план.
- Правки и удаления за время простоя. `messages.getHistory` отдаёт текущее состояние сообщений; удалённое за простой сообщение в ответ не попадает и в базе не помечается `deleted_at`, а правка обновит строку без записи версии через событие. Их возвращает только `getDifference`. Известное ограничение, пишется в контракт.
- Учёт `chat.history_mode = skip` догонкой (Q8: не учитывается).
- Отдельный блок догонки в `daemon status`, флаг `--since` у `tg chat sync catchup` (Q7), настройки для порога, запаса, предела и периода метки (Q5: константы).
- Изменение поведения при отсутствии простоя, расписание глубокой загрузки, `history_limit.*`, лимиты чатов.

Файлы и модули: `internal/store` (`sync_state.go`, `account_state.go` или новый файл, миграция `0014`, `overview.go`), `internal/config` (`settings.go`, `config.go`, тесты), `internal/daemon` (`daemon.go`, новый `catchup.go`, `history_sync.go`, `export_month.go`, `overview.go`), `internal/rpc` (`api.go`, `dashboard.go`, `overview.go`, `export_month.go` если меняется поле), `internal/cli` (`chat_sync_cmd.go`, `daemon_profile_database.go`, разбор переменной окружения рядом с `background_sync_config.go`, тесты), документы из раздела выше.

## Main invariant

- Без простоя поведение не меняется: при простое не больше 10 минут и при первом старте без метки Daemon не ставит ни одной задачи догонки, а очередь, расписание, автозагрузка нового чата, `chat sync start|queue|cancel` для чатов без дыры работают как до плана (тесты существующих пакетов зелёные без правок ожиданий, кроме новых полей).
- Если выключен любой из двух выключателей, `catchup.enabled` или `background_sync.enabled`, догонка не делает ни одного запроса к Telegram: ни списка диалогов, ни страниц истории, ни при старте, ни по команде. Команда `tg chat sync catchup` отвечает кодом 2 с подсказкой, какой ключ включить (как `voice transcription is off`, `internal/daemon/voice_rpc.go:207`). `catchup.enabled = true` не обходит выключенный `background_sync.enabled`.
- Закрытые списками доступа чаты не читаются и не пишутся: фильтр `policy.Allowed` в обходе диалогов (`client.Chats(..., policy.Allowed)`), перед каждой страницей и после неё (как в `runTask`, `internal/daemon/history_sync.go:469-488`).
- Строка `sync_state` чата без дыры после догонки совпадает с прежней по `oldest_id`, `complete`, `first_message_date`; `newest_id` догонка не поднимает (D-A2).
- Ручной `start` и конец окна планировщика задачи догонки не отменяют; задачи `source: background` (расписание и автозагрузка нового чата) они отменяют как до плана, в какой бы полосе очереди задача ни стояла.
- `chat sync start` с флагами глубины у чата без дыры ведёт себя как до плана: `--messages N` значит «N сохранённых сообщений всего».

## Risks and strategy

- Риск: всплеск запросов. Очередь берёт задачи подряд без паузы между ними (`historySyncManager.run`, `internal/daemon/history_sync.go:366-396`). Решение владельца (Q6): пауза `pagePause` (2-8 секунд) перед каждой страницей задачи догонки, первой тоже, поэтому 300 чатов в одну страницу не идут подряд на скорости дросселя.
- Риск: догонка включена у всех по умолчанию. `catchup.enabled` и `background_sync.enabled` по умолчанию `true` (решение владельца, Q5). Известное следствие: после деплоя догонка срабатывает у каждого профиля при первом простое больше 10 минут, без явного включения. Первый запуск нового бинарника метки не находит и догонки не ставит. Отключение: `tg settings set catchup.enabled false` и перезапуск Daemon (см. «Deploy / rollback»).
- Риск: ложная дыра в активном чате. Живые события не пишут `sync_state`, поэтому `newest_id` отстаёт (`internal/store/sync_state.go:111-120`); остановка по `newest_id` упёрлась бы в предел. Решение владельца: остановка по времени (D3).
- Риск: ручной `start` или окно планировщика отменяет задачи догонки. `cancelBackgroundLocked` отменяет все задачи с `source: background` (`internal/daemon/history_sync.go:224-233`). Решение владельца: у догонки `source: catchup` (Q3), отмена по значению `background` её не задевает (Q2).
- Риск: проход дыры ручным `start` обрывается проверками глубины. `depth.satisfied` и `historyLimit.reached` смотрят на число и дату всех сохранённых сообщений чата (`MessageBounds`), и у чата с историей ниже дыры они обычно уже выполнены (Known facts). Решение: проход дыры эти проверки не выполняет; явные флаги `start` ограничивают сам проход по своему правилу (Q9).
- Риск: у флага `--messages N` два смысла в трёх случаях: у чата без дыры «N сохранённых всего»; при проходе дыры и в продолжении той же задачи от `oldest_id` у чата с дырой «N сообщений этой задачи» (Q9, Q11). Агент может ждать одного, а получить другое. Решение: все три случая явно в `docs/cli/chats.md`, контракте и навыке (шаг 11).
- Риск: задача автозагрузки нового чата ждёт за всей догонкой (до 40-50 минут). Решение владельца (Q10): она стоит в полосе догонки, в порядке прихода; ждёт только задачи, поставленные раньше.
- Риск: миграция `0014` необратима для старых бинарников (`store.Open` отказывает на базе новее, `internal/store/store.go:69`). Решение: копия базы перед деплоем (см. «Deploy / rollback»).
- Риск: менеджер обновлений и догонка пишут одни сообщения одновременно. Решение: запись идёт upsert по `(chat_id, id)` (`upsertMessagesTx`), дубликатов не будет; порядок «сначала догонка, потом менеджер» не нужен.

## Decision log

| Date | Decision | Reason | Confirmed |
| --- | --- | --- | --- |
| 2026-11-22 | D1. Глубина: до метки времени, но не глубже 1000 сообщений на чат. Если предел сработал раньше, чат получает отметку дыры. | Короткая догонка в типичном чате, предсказуемая цена в канале с потоком. | yes |
| 2026-11-22 | D2. Дыра: колонки `gap_from` и `gap_to` в `sync_state`, одна дыра на чат; вторая расширяет первую до общего интервала. `SaveHistoryPage` не поднимает `newest_id` выше `gap_from`, пока дыра есть. Дыра видна в `chat sync status` и там, где отдаётся состояние загрузки чата (по коду это `chat overview`, см. Known facts). | Помечено больше, чем потеряно, но ничего не теряется молча. | yes |
| 2026-11-22 | D3. Остановка: Daemon пишет `last_alive_at` в `account_state` раз в минуту и при чистом выходе. Догонка идёт от верха чата вниз, пока дата сообщения новее `last_alive_at` минус 10 минут. | Запас покрывает аварийный выход; повторно сохранённые сообщения пишутся как обновления. | yes |
| 2026-11-22 | D4. Чаты: видимые по спискам доступа, с хотя бы одним сообщением в базе и с новым в диалоге (дата последнего сообщения диалога новее `last_alive_at` минус запас). Чужие и неизвестные базе чаты не трогаем; новые чаты получают автозагрузку 100 при первом живом сообщении, как раньше. | Догонка восстанавливает то, что база уже вела. | yes |
| 2026-11-22 | D5. Порог простоя: больше 10 минут. Меньше порога догонку не ставим. | Короткий перезапуск закрывает менеджер обновлений. | yes |
| 2026-11-22 | D6. Очередь: догонка стоит впереди глубокой загрузки и не подчиняется дневному окну. Пересмотрено 2026-11-23 ответом на Q5: догонка работает только при включённых обоих выключателях, `catchup.enabled` и `background_sync.enabled`; первый не обходит второй. Пауза `pagePause` и фоновая полоса дросселя те же, что у автозагрузки нового чата, пауза стоит и перед первой страницей (Q6). | Отдельный выключатель догонки при общем выключателе собственных запросов Daemon за историей (решение `20261122_1639`). | yes |
| 2026-11-22 | D7. Запуск: автоматически при старте Daemon (при простое больше порога) и командой `tg chat sync catchup` с `--dry-run`. | Ручной повтор после отмены и проверка выбора без запросов за историей. | yes |
| 2026-11-23 | Q1. Ручной `chat sync start` чата с дырой сначала проходит дыру сверху вниз до `gap_from`, затем идёт прежняя загрузка от `oldest_id`. Новой команды и флага нет. Семантика: раздел «Проход дыры ручным `start`». | Агент уже знает `start`; цена: `start` меняет поведение у чатов с дырой. | yes |
| 2026-11-23 | Q2. Порядок очереди: ручные задачи, затем задачи догонки (`source: catchup`), затем глубокая загрузка. Ничего не отменяется: ручной `start` не отменяет задачи догонки; ручной `catchup` не прерывает идущую глубокую задачу, а встаёт сразу за ней. Ручной `start` незагруженного чата по-прежнему отменяет задачи `source: background` (проверено по коду, Known facts). Очередь получает приоритеты вместо FIFO (раздел «Очередь с приоритетами»). | Порядок «ручная, догонка, глубокая» без потерь; цена: приоритеты в очереди вместо FIFO. | yes |
| 2026-11-23 | Q3. У задачи догонки `source: "catchup"`. Конец окна планировщика (`background_sync.go:370`) и ручной `start` её не отменяют. Обновляются `rpc.HistorySyncJob.source`, `tg schema`, документация и навык. | Агент видит, что идёт догонка, правила отмены читаются по значению; цена: новое значение поля. | yes |
| 2026-11-23 | Q4. `last_alive_at` пишется только пока выполнены все условия: вход выполнен, клиент готов, нет `auth_lost`. При чистом выходе без входа метка не пишется. Обрыв связи при живом Daemon догонку при старте не включит (Out of scope). | Метка значит «обновления могли идти»; цена: долгий обрыв связи не догоняется. | yes |
| 2026-11-23 | Q5. Отдельная настройка `catchup.enabled`, умолчание `true`; догонка работает только при включённых `catchup.enabled` и `background_sync.enabled`. Владелец отдельно подтвердил: догонка включена по умолчанию. Порог 10 минут, запас 10 минут, предел 1000 и период метки 1 минута остаются константами в коде; новая настройка одна, `catchup.enabled`. | Догонку можно выключить, не трогая расписание; цена: второй выключатель и следствие в Risks. | yes |
| 2026-11-23 | Q6. Пауза `pagePause` перед каждой страницей задачи догонки, первой тоже. | Ровная нагрузка; цена: 300 чатов дольше на 300 пауз. | yes |
| 2026-11-23 | Q7. У `tg chat sync catchup` нет флага `--since`: граница из метки, прочитанной при старте этого Daemon; без простоя ответ `since: null`, ничего не ставится. | Ровно D7, без нового флага; произвольный период догоняет `chat sync queue --since`. | yes |
| 2026-11-23 | Q8. Догонка не учитывает `chat.history_mode = skip`, как и автозагрузка нового чата. | `skip` касается глубокой загрузки; цена: чат со `skip` получит до 1000 сообщений (раздел «Cost»). | yes |
| 2026-11-23 | Q9. Явные флаги `chat sync start` ограничивают сам проход дыры: `--since`, `--days`, `--months` останавливают его на дате, `--messages N` после N сообщений, загруженных этой задачей. Дыра сужается (`gap_to` опускается до наименьшего загруженного id) и остаётся; задача `done` с `depth_reached: true`. Без флагов проход идёт до `gap_from` целиком. У чата с дырой `--messages N` значит «N сообщений этой задачи», у чата без дыры прежнее «N сохранённых всего»; оба смысла явно в документации и в «Cost». | Проход «уважает явные флаги», дыра в канале с потоком проходится порциями; цена: два смысла одного флага. | yes |
| 2026-11-23 | Q10. Автозагрузка нового чата (`startAuto`) стоит в полосе догонки, FIFO вместе с задачами догонки; `source` остаётся `background`; ручной `start` и конец окна отменяют её, как сейчас. Полоса задаётся видом задачи, отмена читает `source`, правила не пересекаются (раздел «Очередь с приоритетами»). | Обе задачи короткие, порядок прихода сохраняется, новой полосы нет; цена: новый чат ждёт задачи догонки, поставленные раньше. | yes |
| 2026-11-23 | Q11. `--messages N` в задаче `start` чата с дырой значит «N сообщений, загруженных этой задачей», до конца задачи: если проход дыры закончился раньше флага и задача идёт дальше от `oldest_id` (чат с `complete = 0`), она грузит ниже `oldest_id`, пока `loaded` задачи не достигнет N. У чата с `complete = 1` задача кончается `done` сразу после закрытия дыры. `--since`, `--days`, `--months` в продолжении считаются по дате самого старого сохранённого сообщения, как до плана; `history_limit.*` к загрузке с явным флагом не применяется, как до плана. Три случая `--messages` явно в документации и в «Cost». | Один смысл флага на всю задачу чата с дырой; цена: продолжение считает свои сообщения, а не сохранённые всего. | yes |
| 2026-11-22 | D-A1. Догонка идёт новым видом задачи того же `historySyncManager`, а не своим воркером. | Одна очередь даёт один запрос истории за раз на Daemon, `chat sync status|cancel`, `cancel --all` и удержание `--idle-timeout` (`watchIdle` смотрит `d.sync.hasActive()`, `daemon.go:516`) без новой логики. | yes (2026-11-23) |
| 2026-11-22 | D-A2. Страницы догонки пишет новый `store.SaveCatchupPage`: сообщения и люди одной транзакцией, без изменения `oldest_id`, `newest_id`, `complete`, `first_message_date`; строку `sync_state` он не создаёт. | `SaveHistoryPage` через `MIN`/`MAX` создал бы чату без строки `oldest_id` у верха, и глубокая загрузка перескочила бы сохранённое ниже; `newest_id` и так отстаёт от живых. | yes (2026-11-23) |
| 2026-11-22 | D-A3. Границы дыры: `gap_to` = наименьший id, загруженный догонкой этого чата; `gap_from` = наибольший id сохранённого сообщения чата с `id < gap_to` и датой не новее `last_alive_at` (0, если такого нет). Расширение: `gap_from = MIN`, `gap_to = MAX`. Чату без строки `sync_state` отметка создаёт строку с пустыми `oldest_id`, `newest_id` и `complete = 0`. | Интервал между последним сообщением до простоя и нижним краем догонки; пустые id оставляют глубокую загрузку такого чата от верха, как сейчас. | yes (2026-11-23) |
| 2026-11-22 | D-A4. Дыра закрывается в транзакции `SaveHistoryPage`, когда одна задача прошла страницами подряд от id не ниже `gap_to` (или от верха, offset 0) до страницы с id не выше `gap_from`; тогда `gap_*` очищаются, а `newest_id` поднимается до верха прохода. Проход запускает ручной `chat sync start` (Q1, 2026-11-23); он начинается с offset `gap_to`, то есть с верхнего края дыры, а не с верха чата: сообщения выше `gap_to` догонка уже сохранила. Пересмотрено 2026-11-23 ответом на Q9: проход, остановленный явным флагом глубины до `gap_from`, сужает дыру: `gap_to` опускается до наименьшего id, загруженного этой задачей, `gap_from` и `newest_id` не меняются. | Правило не зависит от команды и закрывает дыру и глубокой загрузкой чата без строки; старт с `gap_to` экономит до 10 страниц на чат. | yes (2026-11-23) |
| 2026-11-22 | D-A5. `export-month`: при дыре, у которой дата сообщения `gap_to` не раньше начала месяца, `sync.month_start_covered` равно `false`. Новых полей в манифесте нет. | Месяц с дырой не заявляет покрытие, старые месяцы дыра не задевает. | yes (2026-11-23) |
| 2026-11-22 | D-A6. Пересмотрено 2026-11-23 ответом на Q5: настройка одна, `catchup.enabled` (строка Q5). Ключ метки `last_alive_at` в `account_state`, значение Unix-время JSON-числом. Имя ключа настройки `catchup.enabled`, переменная окружения `TG_CLI_CATCHUP_ENABLED`, режим применения `restart` по аналогии с `background_sync.enabled` (`internal/config/settings.go:40`), без флага. | Формат метки как у прочих значений `account_state` (JSON); режим `restart`, потому что решение о догонке принимается один раз при старте. | yes (2026-11-23) |
| 2026-11-23 | D-A7. Ручной `start` чата, у которого стоит в очереди задача догонки, ставит ручную задачу в ручную полосу и задачу догонки не трогает; у чата тогда две задачи. `chat sync status <чат>` показывает ручную, `chat sync cancel <чат>` отменяет обе. Идущая задача догонки этого чата не прерывается, ручная ждёт за ней. | По коду менеджер держит одну задачу на чат (`m.jobs[chatID]`), а Q2 запрещает отмену догонки; без правила задача догонки выпала бы из `status` и `cancel`. | yes (2026-11-23) |
| 2026-11-23 | D-A8. Отдельного поля «идёт проход дыры» в ответе нет: у задачи `running` проход виден по непустым `gap_from`, `gap_to`; когда дыра закрыта, они становятся `null`, а задача идёт дальше от `oldest_id` или заканчивается. | Новое поле только ради признака, который уже выводится из `gap_*`. | yes (2026-11-23) |

Строки, переживающие план (D1-D7, Q1-Q11, D-A2-D-A8), получают записи в `.agents/decisions/` шагом 12 до `done`; ячейка Decision тогда ссылается на файл.

## New data model and contract

Схема (миграция `internal/store/migrations/0014_sync_gap.sql`, имя файла фиксирует шаг 0):

- `ALTER TABLE sync_state ADD COLUMN gap_from INTEGER;` и `ADD COLUMN gap_to INTEGER;`. Оба пустые или оба заполнены; `gap_from < gap_to`.
- `DROP VIEW chats_state;` и `CREATE VIEW chats_state` с прежними столбцами (`0010_first_message_date.sql:13-40`) плюс `s.gap_from AS history_gap_from`, `s.gap_to AS history_gap_to`.
- `account_state` не меняется: ключ `last_alive_at`.

Go-типы:

- `store.SyncProgress` получает `GapFrom`, `GapTo *int`; `syncProgressColumns` дополняется.
- `rpc.HistorySyncJob` получает `gap_from`, `gap_to` (integer, optional). Для `idle` они берутся из `sync_state`, для задачи копируются из прогресса (`recordPage`, `recordProgress`, `enqueueLocked`, `idleHistorySyncJob`).
- `rpc.ChatOverview` и `store.ChatOverview` получают `history_gap_from`, `history_gap_to` (optional).
- `rpc.HistorySyncJob.source`: новое значение `catchup` (константа рядом с `HistorySyncManual`, `HistorySyncBackground`, `internal/rpc/api.go:516-520`); комментарии поля в `api.go:528` и `rpc.DashboardSyncJob.Source` (`internal/rpc/dashboard.go:129`) перечисляют три значения.
- Новый метод RPC `history_sync.catchup` (`rpc.MethodHistorySyncCatchup`), параметры `{ "dry_run": bool }`, без `since` (Q7). Ответ (форма по образцу `HistorySyncQueueResult`): `dry_run`, `since` (Unix-время границы или null, когда простоя нет), `queued`, `would_queue`, `skipped_active`, `chats` (`chat_id`, `title` untrusted, `last_message_date`, `action`: `queued`, `would_queue`, `skipped_active`). Окончательные имена полей фиксирует шаг 0 и `tg schema`; до этого шаг 0 не закрыт.
- Настройка `catchup.enabled`: bool, умолчание `true`, переменная `TG_CLI_CATCHUP_ENABLED`, режим `restart`, без флага (D-A6).

Команда: `tg chat sync catchup [--dry-run]`, класс `local_write` (как `chat sync queue`), форматы `json`, `jsonl`, `toon`, `human`. Коды выхода: 0; 2 (догонка выключена: `catchup.enabled` или `background_sync.enabled` равно `false`; сообщение называет выключенный ключ и что нужен перезапуск); 3 нет Daemon; 4 нет входа; 6 `FLOOD_WAIT` дольше автоожидания при чтении списка диалогов. Граница: метка, прочитанная при старте этого Daemon (Q7).

### Проход дыры ручным `start` (Q1)

- Когда: `historySyncStart` (`internal/daemon/history_sync.go:609-634`) читает `sync_state`; если `gap_from` и `gap_to` заполнены, задача становится проходом дыры независимо от `complete` и от флагов глубины. Чат без дыры ведёт себя как до плана.
- Ответ `start`: снимок задачи `queued`, `source: manual`, с `gap_from`, `gap_to`. До плана чат с `complete = 1` получал `done` сразу, без запроса (`enqueueLocked`, `:175-178`); с дырой такой чат получает задачу в очереди.
- Начало: offset `gap_to` (D-A4), страницы вниз. Проход кончается на странице с id не выше `gap_from` или на пустой странице; `SaveHistoryPage` закрывает дыру по D-A4 в той же транзакции.
- Что видно во время прохода: `state: running`, `pages` и `loaded` считают все страницы задачи, `gap_from` и `gap_to` показывают открытую дыру, `oldest_id`, `complete`, `first_message_date` не меняются, `newest_id` меняется при закрытии (D-A4). Когда `gap_*` стали `null`, проход окончен (D-A8).
- После прохода без флагов: при `complete = 1` задача заканчивается `done`, `complete` остаётся `1`; при `complete = 0` и заполненном `oldest_id` та же задача идёт дальше от `oldest_id`, с прежними проверками `history_limit.*`; при пустом `oldest_id` (строка создана отметкой дыры, D-A3) задача идёт дальше вниз как обычная полная загрузка от места, где кончился проход.
- Отмена посреди прохода: дыра остаётся с прежними границами, `newest_id` не меняется; следующий `start` снова начинает с `gap_to`.
- Предел глубины. Проход не обрезается пределом 1000 автодогонки. Факт по коду: `depth.satisfied` (`history_sync_depth.go:28-36`) и `historyLimit.reached` (`chat_limit.go`, `history_sync.go:520-547`) считают число и дату всех сохранённых сообщений чата через `MessageBounds`, а не страницы прохода. У чата с историей ниже дыры они обычно уже выполнены, поэтому при прямом переносе `start --messages N` (и `--days`, `--months`, `--since`, которые CLI превращает в `since`) закончился бы `done` без запроса (`depthMet`, `:632`), а полная загрузка закончилась бы после первой страницы прохода с `complete = 1` по `history_limit.since` (2022-02-02) или `history_limit.messages`. Поэтому во время прохода дыры эти проверки не выполняются: `history_limit.*` проход не останавливает, а после закрытия дыры проверки работают как до плана.
- Явные флаги глубины (Q9). Сокращение `depthMet` у чата с дырой не действует, задача ставится в очередь. Во время прохода флаг считается по самому проходу, а не по `MessageBounds`: `--since` (и `--days`, `--months`, которые CLI превращает в `since`) останавливает проход после страницы, самое старое сообщение которой не новее даты; `--messages N` останавливает его после страницы, на которой `loaded` задачи достиг N (страница по 100 сообщений, поэтому `loaded` может превысить N меньше чем на 100, как и у загрузки по глубине сейчас). Остановка по флагу до `gap_from` сужает дыру (D-A4): `gap_to` опускается до наименьшего id, загруженного задачей, `gap_from` и `newest_id` прежние; задача `done` с `depth_reached: true`, `complete` прежний. Повторный `start` начинает с суженной дыры, с offset нового `gap_to`.
- Флаг и закрытие дыры (Q11). Если проход дошёл до `gap_from` раньше, чем сработал флаг, дыра закрывается по D-A4. У чата с `complete = 1` задача кончается `done` сразу после закрытия дыры. У чата с `complete = 0` та же задача идёт дальше от `oldest_id`: с `--messages N` она грузит ниже `oldest_id`, пока `loaded` задачи (вместе со страницами прохода) не достигнет N, затем `done` с `depth_reached: true`; с `--since`, `--days`, `--months` она считает по дате самого старого сохранённого сообщения, как до плана. `history_limit.*` к загрузке с явным флагом не применяется, как до плана (`history_sync.go:520-530`). Дойдя до первого сообщения чата раньше флага, задача кончается `complete = 1`, как сейчас.
- Три случая `--messages N`: у чата без дыры прежнее «N сохранённых всего»; при проходе дыры «N сообщений этой задачи»; в продолжении той же задачи от `oldest_id` тоже «N сообщений этой задачи». Шаг 11 пишет все три случая явно в `docs/cli/chats.md`, контракт и навык.
- Ручной `start` чата с активной ручной задачей возвращает её, как до плана (`:132-134`).

### Очередь с приоритетами (Q2, Q10)

- Где хранится приоритет: новое поле `lane` у `historySyncTask` (`history_sync.go:40-53`): `laneManual`, `laneCatchup`, `laneBackground`. Его задаёт вид задачи, то есть тот, кто ставит задачу: `startDepth` (ручные, и `chat sync queue`, `history_sync_queue.go:91, 229`) даёт `laneManual`; новая постановка догонки и `startAuto` дают `laneCatchup` (Q10); `startBackground` даёт `laneBackground`.
- Вставка: `enqueueLocked` вместо `append` вставляет задачу после последней задачи очереди с `lane` не больше своей; внутри полосы порядок FIFO. `run` берёт `queue[0]`, как сейчас. Идущая задача не прерывается никем, кроме отмены.
- Отмена: `cancelBackgroundLocked` (`:224-233`) по-прежнему отменяет задачи с `source: background`, то есть задачи расписания и автозагрузки нового чата; задачи `source: catchup` он не трогает. Его зовут ручной `start` незагруженного чата (`:135-137`) и конец окна (`background_sync.go:370`), оба без изменений.
- `startBackground` ставит задачу только в пустую очередь (`:144-154`), а `runOnce` пропускает запуск при активной задаче (`background_sync.go:230`): пока стоит догонка, новая глубокая задача расписания не появляется.
- Ручной `catchup` при идущей глубокой задаче: она идёт дальше, задачи догонки встают в полосу догонки, то есть сразу за ней и впереди фоновых задач очереди.
- `startAuto` (автозагрузка нового чата, Q10): сейчас ставит задачу `source: background` в конец очереди, ничего не отменяя, и пропускает чат с активной задачей (`:707-723`). После плана она встаёт в полосу догонки, FIFO вместе с задачами догонки, `source` остаётся `background`. Полоса и `source` не противоречат друг другу: `run` выбирает задачу по `lane`, а `cancelBackgroundLocked` отменяет по `source` и на `lane` не смотрит (`:224-233`). Поэтому ручной `start` незагруженного чата и конец окна отменяют задачу автозагрузки, как сейчас, а задачи `catchup` той же полосы остаются.
- Догонка и автозагрузка одного чата не встречаются: догонка выбирает только чаты с сохранёнными сообщениями (D4), автозагрузка только чаты без них (`chatIsNew`); чат с активной задачей пропускают обе.
- Одна задача на чат и задача догонки того же чата: D-A7.

## Write and read paths

Запись:

1. Старт Daemon (`Run`, `internal/daemon/daemon.go`): до запуска пульса читается прежняя `last_alive_at`. Простой = `now - last_alive_at`. Граница `since = last_alive_at - 10m` хранится в памяти Daemon (для ручного запуска, Q7). Затем стартует горутина пульса: раз в минуту, и первый раз сразу, она проверяет условия Q4 и только при их выполнении пишет `SetAccountState("last_alive_at", now)`.
2. Условия пульса (Q4), без запроса к Telegram: `d.getAuthLost() == ""` (`daemon.go:902-906`) и `d.backgroundSyncReady()` (`background_sync.go:462-482`: канал `client.Ready()` закрыт и `d.account.user != nil`; пользователя ставит горутина старта через `client.Self` и `setIfEmpty`, `daemon.go:388-404`). Daemon без входа, с `auth_lost` или с неготовым клиентом метку не пишет и не двигает.
3. Чистый выход: в последовательности остановки после `waitForShutdown` (её проходят и `daemon stop`, и `--idle-timeout`, и SIGINT/SIGTERM через `signal.NotifyContext`, `cmd/tg-cli/main.go:101`) пульс проверяет те же условия и, если они выполнены, пишет метку последний раз отдельным контекстом с таймаутом, до закрытия базы (`defer ownedStore.Close()`, `daemon.go:218`). Без входа при выходе метка не пишется. Аварийный выход оставляет метку не старше минуты, запас 10 минут её покрывает.
4. Автозапуск догонки: горутина после `startBackgroundSync`. Выключен `catchup.enabled`, нет `d.background` (выключен `background_sync.enabled`), нет метки или простой не больше 10 минут: выход без запросов. Иначе ждёт вход и готовность клиента (как горутина контактов, `daemon.go:405-438`, и `backgroundSyncReady`), затем выбирает чаты и ставит задачи.
5. Выбор чатов: `client.Chats(throttle.WithBackground(ctx), 100, "", after, policy.Allowed)` страница за страницей, как `pickQueueChats` (`history_sync_queue.go:106-160`): закреплённый диалог старше границы пропускается, первый незакреплённый старше границы заканчивает обход. Чат без строк в `messages` пропускается (D4). `chat.history_mode` не смотрится (Q8). Чат с активной задачей идёт в ответ с `skipped_active`.
6. Постановка: задачи `source: catchup` в полосу догонки, туда же задачи автозагрузки нового чата (`source: background`, Q10); раздел «Очередь с приоритетами».
7. Задача догонки (`runCatchupTask` рядом с `runTask`): перед каждой страницей, первой тоже, пауза `d.background.pagePause` (2-8 секунд, `background_sync.page_pause_*`, Q6); offset 0, страница `client.HistoryPage`, проверка `policy.Allowed` до и после запроса, `SaveCatchupPage`, `voiceSyncPage` как у обычной страницы. Стоп: пустая страница; самое старое сообщение страницы не новее границы; загружено 1000 сообщений, тогда `MarkCatchupGap` по D-A3.
8. `SaveHistoryPage`: `newest_id` не выше `gap_from`, пока дыра есть; правило закрытия и сужения D-A4; проход дыры ручным `start` и его флаги по разделу «Проход дыры ручным `start`» (Q1, Q9).

Чтение: `chat sync status [<чат>]` (`source: catchup`, `gap_from`, `gap_to`), `chat sync start|cancel` (поля `gap_from`, `gap_to`), `chat overview` (`history_gap_from`, `history_gap_to` через представление), `export-month` (D-A5), `tg settings` (`catchup.enabled`), панель веб-просмотрщика (`DashboardSyncJob.Source` показывает `catchup`), лог Daemon (`catchup queued`, число чатов, граница; `catchup disabled` с именем выключенного ключа).

## Migration / backfill

- Миграция `0014` добавляет две пустые колонки и пересоздаёт представление. Заполнения нет: дыры до плана неизвестны.
- `account_state.last_alive_at` при первом запуске нового бинарника отсутствует: догонка не ставится (первый запуск без метки), пульс начинает писать метку, как только выполнены условия Q4. Первая догонка возможна только после следующего простоя.
- Настройка `catchup.enabled` в базе не записывается: до первой `tg settings set` действует умолчание `true`.

## Compatibility and release stages

- Ответы получают только новые необязательные поля и новое значение `source: catchup`; старые поля и их смысл не меняются. Агент, который ждёт только `manual` и `background`, увидит третье значение: навык и `tg schema` описывают его.
- `chat sync start` меняет поведение только у чата с дырой (Q1); дыры появляются только после первой догонки.
- Новая настройка `catchup.enabled` с умолчанием `true`: старые конфигурации и базы её не содержат и получают умолчание.
- База версии 14 не открывается старым бинарником: откат только с копией базы.
- Этапы: (1) слияние `feat/history-limits` в `main`; (2) этот план по шагам 0-12 в одной ветке; (3) деплой по команде владельца с копией базы.

## Step 0. Fix decisions before changes

- [x] Владелец ответил на Q1-Q8; ответы записаны в Decision log с `Confirmed: yes`, строки `Open questions` получили статус `decided` и ссылку (2026-11-23).
- [x] Владелец ответил на Q9 и Q10; ответы в Decision log, строки получили `decided` (2026-11-23).
- [x] Владелец ответил на Q11; ответ в Decision log, строка получила `decided` (2026-11-23).
- [x] Владелец подтвердил D-A1-D-A8 (2026-11-23).
- [x] Зафиксированы имена: файл миграции `0014_sync_gap.sql`, метод `history_sync.catchup` (`rpc.MethodHistorySyncCatchup`), поля ответа и константа `source: catchup` по разделу «New data model and contract», полосы очереди `laneManual`, `laneCatchup`, `laneBackground` (раздел «Очередь с приоритетами»), ключ `catchup.enabled`, переменная `TG_CLI_CATCHUP_ENABLED` (2026-11-23). Новые имена только через правку плана.
- [x] Исполнитель выбран при запуске 2026-11-23: код и тесты делает Codex Terra (`gpt-6.1-terra`, умолчание для Go), ревью, `just check` и коммиты делает менеджер (песочница Codex не открывает сокеты). Исполнитель загружает скилл `expert-golang` и читает решения через `scout`.
- [x] Ветка создана, статус плана `in_progress` (2026-11-23; ветка от `feat/history-limits`, см. «Зависимость»).

## Step 1. Audit and baseline

- [x] (2026-11-23, на `feat/history-limits`: 28 пакетов `ok`, exit 0, вывод в `var/agents/plans/20261122_2245_chat_catchup_after_downtime/baseline-check.txt`) `just check` на свежем `main` зелёный; результат и время записаны в Run log. Это базовая линия: после плана падать может только то, что план менял намеренно.
- [-] (заменено полным `just check` базовой линии, решение менеджера 2026-11-23, список тестов не нужен) `go test -count=1 ./internal/daemon -run 'HistorySync|NewChat|Background'`, `go test -count=1 ./internal/store -run 'Sync|Overview|Migration'` и `go test -count=1 ./internal/config ./internal/cli` зелёные; список тестов сохранён в `var/agents/plans/20261122_2245_chat_catchup_after_downtime/baseline-tests.txt`.
- [x] (2026-11-23, вместо Atlas `rg`, он недоступен в песочнице Codex; расхождение: вызов `history_sync.go:538` относится к `MarkHistoryLimitReached`, не к `SaveHistoryPage`) `bin/atlas incoming SaveHistoryPage`, `bin/atlas incoming SyncState`, `bin/atlas incoming enqueueLocked` и `bin/atlas incoming cancelBackgroundLocked` сверены со списками вызовов из Known facts; новые вызовы, если есть, добавлены в план.
- [x] `rg -n 'new_chat_sync.messages' internal docs` перечисляет все места, где перечислены ключи настроек; список сохранён в `var/agents/plans/20261122_2245_chat_catchup_after_downtime/settings-keys.txt` и сверен со списком шага 4.

## Implementation steps

Каждый шаг: тесты рядом с кодом, без живого Telegram, на фейковом клиенте (`fakeTelegram`, `internal/daemon/daemon_test.go:20`), фейковых часах (`historySyncClock`) и временной SQLite; отметка `- [x]` только после зелёной проверки шага.

### Шаг 2. Схема и хранилище

- [x] Миграция `0014`: колонки `gap_from`, `gap_to`, пересозданное `chats_state`. Тест миграции по образцу `buildDatabaseAt` (`internal/store/overview_test.go:11`): база версии 13 с данными мигрирует, старые столбцы представления те же, новые пустые. Проверка: `go test -count=1 ./internal/store -run 'Migration|Overview|Schema'`, ожидание: зелёные.
- [x] `SyncProgress` с `GapFrom`, `GapTo`; `scanSyncProgress` и `syncProgressColumns`. Проверка: `go test -count=1 ./internal/store`, зелёные.
- [x] `SaveCatchupPage` (D-A2): тест, что `oldest_id`, `newest_id`, `complete`, `first_message_date` не меняются, строка не создаётся, сообщения и люди записаны. Проверка: `go test -count=1 ./internal/store -run Catchup`, зелёные.
- [x] `MarkCatchupGap(chatID, gapTo, aliveAt)` (D-A3): вычисление `gap_from`, расширение второй дырой, создание строки с пустыми id. Тесты на все три случая.
- [x] `SaveHistoryPage`: потолок `newest_id` при дыре и закрытие по D-A4 (новый параметр верха прохода). Тесты: дыра остаётся при странице внутри интервала; закрывается при проходе от `gap_to` до `gap_from`; при `complete = 1` закрытие не сбрасывает `complete` и `first_message_date`; без дыры поведение прежнее (существующие тесты `sync_state_test.go` без правок ожиданий).
- [x] Чтение и запись `last_alive_at` (обёртка над `GetAccountState`/`SetAccountState`). Тест: отсутствие ключа, запись, чтение.

### Шаг 3. Метка жизни Daemon и её условия (Q4)

- [x] В `Run` прежняя метка читается до пульса; пульс раз в минуту на часах, которые тест подменяет; финальная запись в последовательности остановки до закрытия базы.
- [x] Условие записи: функция без запроса к Telegram, `getAuthLost() == ""` и `backgroundSyncReady()`; её зовут и пульс, и финальная запись.
- [0] (тесты условий записи, пульса, простоя 9 и 11 минут и битой метки написаны и зелёные; интеграционный тест остановки через `Run` не написан, это волна C, проверить в `just check`) Тесты: первый старт без метки не ставит догонку и пишет метку после входа; остановка через отмену контекста (путь `daemon stop`, idle и сигнала) пишет метку; простой 9 минут не ставит догонку, 11 минут ставит; клиент не готов: метки нет; нет входа (`account.user == nil`): метки нет, ни пульсом, ни при выходе, прежняя метка не меняется; `auth_lost` выставлен: метка не двигается; вход появился позже старта: пульс начинает писать. Проверка: `go test -count=1 ./internal/daemon -run 'Alive|Catchup'`, зелёные.

### Шаг 4. Настройка `catchup.enabled` (Q5)

- [x] `internal/config/settings.go`: строка в `settingSpecs` рядом с `background_sync.enabled` (`:40`): имя `catchup.enabled`, тип `bool`, умолчание `true`, переменная `TG_CLI_CATCHUP_ENABLED`, без флага, режим `restart`, не `protected`, не `secret`.
- [x] Поле конфигурации и умолчание: `internal/config/config.go` (структура `Config`, `:36-37`, умолчания `defaults`, `:249`), сборка из настроек рядом с `settings.go:302`.
- [x] Значение из базы: карта ключей `internal/cli/daemon_profile_database.go:26-45`.
- [x] Переменная окружения: разбор в `internal/cli` по образцу `backgroundSyncEnv.enabled` (`background_sync_config.go:56-64`): `true` или `false`, иное значение даёт код 2 до старта Daemon.
- [x] Передача в Daemon: поле `daemon.Options`; Daemon читает его один раз при старте. Ключ не добавляется в список живых ключей `internal/daemon/settings.go:158`: режим `restart`.
- [x] Каждое место из `settings-keys.txt` (шаг 1), где перечислены все ключи, получает `catchup.enabled`.
- [x] (config и CLI зелёные; ноль вызовов при выключенных выключателях проверен тестом шага 7, 2026-11-23) Тесты: `internal/config/settings_test.go`: `TestSettingDefaults` (карта умолчаний, `:13-22`), `TestTemplateDocumentedSettingDefaults` (`:176`), таблица разбора значений (`true`, `false` верны; `1`, `yes` ошибка); CLI: переменная окружения перекрывает базу, неверное значение даёт код 2; Daemon: `catchup.enabled = false` при включённом расписании даёт ноль вызовов `Chats` и `HistoryPage` и ни одной задачи. Проверка: `go test -count=1 ./internal/config ./internal/cli ./internal/daemon -run 'Setting|Catchup|Template|Background'`, зелёные.

### Шаг 5. Очередь с приоритетами (Q2)

- [x] Поле `lane` у `historySyncTask`, вставка по полосам в `enqueueLocked`, полоса у каждого вызова по виду задачи: `startDepth` ручная, постановка догонки и `startAuto` полоса догонки (Q10), `startBackground` фоновая.
- [x] Константа `source: catchup` в `internal/rpc/api.go`; `cancelBackgroundLocked` не меняется.
- [x] Правило D-A7: ручной `start` чата с задачей догонки в очереди; `cancelJob` отменяет все задачи чата.
- [0] (не хватает теста «идущая фоновая задача не прерывается постановкой догонки, догонка берётся следующей», волна C) Тесты: при постановке в порядке «фоновая, догонка, ручная» воркер берёт «ручная, догонка, фоновая»; внутри полосы FIFO; новый чат при очереди догонки (две задачи `catchup`, затем `startAuto`, затем ещё одна `catchup`) идёт в порядке прихода, третьим; ручной `start` незагруженного чата отменяет задачи `background`, в том числе задачу автозагрузки в полосе догонки (существующий тест без правок ожиданий), и не отменяет задачи `catchup`; `cancelBackground` (конец окна) отменяет задачу автозагрузки и не отменяет `catchup`; ручной `catchup` при идущей фоновой задаче: она идёт до конца, задачи догонки берутся следующими, впереди фоновых в очереди; `cancel --all` отменяет задачи догонки; D-A7: `status <чат>` показывает ручную, `cancel <чат>` снимает обе. Проверка: `go test -count=1 ./internal/daemon -run 'HistorySync|Queue|Background|NewChat|Catchup'`, зелёные, существующие тесты без правок ожиданий.

### Шаг 6. Задача догонки в менеджере истории

- [x] Вид задачи догонки в `historySyncTask` (граница, предел 1000, время метки), ветка `runCatchupTask` в `run`, `source: catchup`, полоса догонки.
- [x] Пауза `d.background.pagePause` перед каждой страницей, первой тоже (Q6).
- [x] (`FLOOD_WAIT` проверен только через подменный клиент, реальное ожидание gotd не проверено) Тесты на фейковом клиенте: остановка по дате (страница с сообщением старше границы); остановка по пустой странице; предел 1000 ставит дыру с верными границами; пауза перед первой страницей (фейковые часы видят таймер до первого `HistoryPage`); закрытый во время задачи чат даёт `failed` с `-32006` и ничего не пишет после закрытия; `FLOOD_WAIT` ведёт себя как у обычной задачи; отмена `cancel` и `cancel --all` снимает задачу догонки; чат со `chat.history_mode = skip` догоняется (Q8). Проверка: `go test -count=1 ./internal/daemon -run 'Catchup|HistorySync|Background|NewChat'`, зелёные, существующие тесты без правок ожиданий.

### Шаг 7. Выбор чатов и автозапуск

- [x] Функция выбора по Write path 5: фильтр доступа, граница по `LastMessageDate`, пропуск закреплённых старых, пропуск чатов без сообщений, `skipped_active`, фоновая метка дросселя на чтении диалогов.
- [x] Автозапуск при старте по Write path 4; при любом выключенном выключателе фейковый клиент не получает ни одного вызова `Chats` и `HistoryPage`.
- [x] Тесты: закрытый чат не выбран и не запрошен; чат без сообщений не выбран; чат со старым последним сообщением не выбран; четыре сочетания выключателей: только `true`/`true` даёт задачи, три остальных дают ноль вызовов клиента; `auth_lost` не ставит ничего. Проверка: `go test -count=1 ./internal/daemon -run Catchup`, зелёные.

### Шаг 8. Проход дыры ручным `start` (Q1)

- [ ] `historySyncStart` и `enqueueLocked`: чат с дырой получает задачу прохода с offset `gap_to`; сокращение «`complete` или `depthMet` даёт `done` сразу» для него не действует, и с флагами глубины тоже.
- [ ] `runTask`: во время прохода проверки `MessageBounds` (`task.depth` и `historyLimitFor`) не выполняются; без флагов проход идёт до `gap_from`; с флагом (Q9) `--since` останавливает проход после страницы с самым старым сообщением не новее даты, `--messages N` после страницы, где `loaded` задачи достиг N. После закрытия дыры: `complete = 1` даёт `done`; иначе offset `oldest_id`, и дальше (Q11) `--messages N` считается по `loaded` задачи до N, `--since` по `MessageBounds`, как до плана, без флагов прежние проверки `history_limit.*`; при пустом `oldest_id` загрузка идёт дальше вниз по тем же правилам.
- [ ] Хранилище: сужение дыры по D-A4 (`gap_to` до наименьшего id, загруженного задачей; `gap_from`, `newest_id`, `complete` прежние) одной транзакцией с последней страницей прохода. Тест в `internal/store`: сужение, повторное сужение, сужение не создаёт строку и не трогает чат без дыры. Проверка: `go test -count=1 ./internal/store -run Gap`, зелёные.
- [ ] Тесты: чат `complete = 1` с дырой: `start` отвечает `queued`, первая страница запрошена с offset `gap_to`, дыра закрыта, задача `done`, `complete` остался `1`; чат `complete = 0` с дырой: после закрытия следующая страница запрошена с offset `oldest_id`; чат с самым старым сообщением до 2022-02-02: проход не обрывается `history_limit.since`; отмена посреди прохода: `gap_*` и `newest_id` прежние, повторный `start` снова с `gap_to`; `status` во время прохода показывает `gap_*`, после закрытия `null`; чат без дыры: существующие тесты `start` без правок ожиданий.
- [ ] Тесты флагов (Q9), на дыре в 5 страниц у чата, где сохранённых сообщений больше N и самое старое старше даты: `--messages 150` даёт две страницы, `done`, `depth_reached: true`, `gap_to` равен наименьшему загруженному id, `gap_from` прежний; `--since` с датой внутри дыры останавливает проход на странице с этой датой и сужает дыру так же; `--days` и `--months` через CLI дают тот же `since`, что и `--since` (тест запроса в `internal/cli`); повторный `start` после сужения запрашивает первую страницу с offset нового `gap_to`; флаг, который не сработал до `gap_from` (Q11): `start --messages 5000` на дыре в 300 сообщений у чата с `complete = 0` закрывает дыру и грузит ниже `oldest_id`, пока `loaded` задачи не достигнет 5000, затем `done` с `depth_reached: true`; то же у чата с `complete = 1` кончается `done` сразу после закрытия дыры, без запроса ниже; `--since` с датой ниже дыры у чата с `complete = 0` после закрытия считает по дате самого старого сохранённого сообщения; `history_limit.messages` меньше 5000 не обрывает задачу с флагом; `start --messages N` у чата без дыры по-прежнему `done` без запроса при N сохранённых всего. Проверка: `go test -count=1 ./internal/daemon ./internal/cli -run 'HistorySync|Gap|ChatSync'`, зелёные.

### Шаг 9. RPC и CLI

- [ ] `rpc.MethodHistorySyncCatchup`, параметры и результат в `internal/rpc/api.go`; обработчик в `internal/daemon`; `--dry-run` не ставит задач и не читает страниц истории (читает только список диалогов); при выключенном выключателе код 2 до любого запроса.
- [ ] `tg chat sync catchup [--dry-run]` в `internal/cli/chat_sync_cmd.go`; тест запроса и вывода по образцу `TestChatSyncQueueRequestAndOutput` (`internal/cli/chat_sync_depth_cmd_test.go:89`); поля `title` помечены `untrusted`.
- [ ] Поля `gap_from`, `gap_to` в ответах `chat sync start|status|cancel`, `history_gap_from`, `history_gap_to` в `chat overview`, значение `catchup` в описании `source`; `schema_fields_test.go` обновлён.
- [ ] Проверка: `go test -count=1 ./internal/cli ./internal/rpc ./internal/daemon`, зелёные.

### Шаг 10. Экспорт

- [ ] `export-month` по D-A5 (`internal/daemon/export_month.go:120-140`). Тест: месяц с дырой `month_start_covered: false`, месяц раньше дыры прежний. Проверка: `go test -count=1 ./internal/daemon -run ExportMonth`, зелёные.

### Шаг 11. Документы

- [ ] `docs/cli/chats.md`: раздел `### tg chat sync catchup` вместе с командой (иначе падает тест `internal/cli`, `docs_test.go`); в `### tg chat sync start` проход дыры (Q1: начало с `gap_to`, ответ `queued` у чата с `complete = 1`, поля во время прохода, продолжение от `oldest_id`) и флаги глубины (Q9: `--since`, `--days`, `--months` останавливают проход на дате, `--messages N` после N сообщений этой задачи, сужение дыры, `depth_reached: true`; продолжение той же задачи от `oldest_id` по Q11; явно три случая `--messages N`: у чата без дыры «N сохранённых всего», при проходе дыры и в продолжении задачи у чата с дырой «N сообщений этой задачи»); поля дыры в `chat sync status|cancel|start`; значение `source: catchup` и порядок очереди (ручные, затем догонка вместе с автозагрузкой нового чата, затем фоновые) в `chat sync status`; абзац о догонке и дыре в «Loading a whole chat»; раздел `chat overview` с новыми полями. Таблицы флагов только через `just docs`, руками не правятся.
- [ ] `docs/cli/overview.md` (раздел настроек, `:337-360`): абзац и строка таблицы `catchup.enabled` (переменная, значения, умолчание `true`, нужен перезапуск, работает только вместе с `background_sync.enabled`); `docs/cli/settings.md:15`: список ключей с умолчанием `true`.
- [ ] `docs/contracts.md`: новый пункт рядом с «Batch of named chats and the load of a new chat» (метка и её условия, порог, запас, предел, два выключателя, выбор, `source: catchup`, полосы очереди и место автозагрузки нового чата в полосе догонки, пауза перед каждой страницей, дыра, её проход ручным `start` и сужение флагами, три случая `--messages N` (Q9, Q11), известные ограничения: правки и удаления за простой, обрыв связи при живом Daemon, `gap` менеджера не ставит догонку, `history_mode = skip` не учитывается); в строке 14 «off by default» заменить на «on by default» и фразу о ручном `start` дополнить: задачи догонки он не отменяет; в строке 17 «behind the queue» заменить описанием полосы догонки.
- [ ] `FEATURES.md`: пункт `10.21` (следующий свободный в разделе 10, последний сейчас 10.20) с подпунктами и отметкой «не проверено на живом аккаунте»; ID не перенумеровываются.
- [ ] Навык `docs/skill/tg/` (английский текст, русские только триггерные фразы): `references/history.md` (догонка, `source: catchup`, порядок очереди, дыра и `start`, три случая `--messages N`: чат без дыры, проход дыры, продолжение задачи у чата с дырой), `references/daemon-auth.md:27` и `references/safety.md:10` (`catchup.enabled` включён по умолчанию, меняется только по просьбе владельца, нужен перезапуск), рецепт `recipes/sync-queue-control.md` (третье значение `source`, ручной `start` догонку не отменяет, новый чат в полосе догонки), рецепт `recipes/sync-active-chats.md` или новый рецепт про возврат после простоя (по `20261120_1700`), ссылка на `FEATURES.md` 10.21 вместо копии списка непроверенного. Проверка: `just skill-check`, без расхождений.
- [ ] Тексты пишет и проверяет `writer` или `docwriter` с `glavred` по `.agents/rules/docs_chain.md`; механические правки (`just docs`, номера, ссылки, «off» на «on») без `glavred`.
- [ ] Запись changelog через `just changelog add` (текст по-английски, тип `Добавлено`), с `--decision` на файл решения; в тексте сказано, что догонка включена по умолчанию и как её выключить.
- [ ] Проверка: `just docs`, затем `go test -count=1 ./internal/cli`, зелёные; `git diff --stat docs/cli` показывает только ожидаемые разделы.

### Шаг 12. Решения и закрытие

- [ ] Файлы в `.agents/decisions/` по `.agents/rules/decisions.md`: одна запись «догонка после простоя» для D1-D7, Q2-Q8, Q10 и D-A6 (разделы Decision, Why, Cost; в Cost следствия из раздела «Cost» плана), одна запись «дыра и её проход» для Q1, Q9, Q11 и D-A2-D-A5, D-A7, D-A8 (в Cost три случая `--messages N`); строки в `_TOC.md`; `paths` не шире 60 файлов. Решение `20261120_1651` не правится молча: новое решение пишет, что «off by default» не соответствует коду и умолчание `true` (поле `supersedes` или раздел по `decisions.md`). Ячейки Decision log ссылаются на файлы.
- [ ] `just check` зелёный; результат в Run log.
- [ ] Ревью диффа Claude; замечания закрыты или записаны.
- [ ] Статус `done` только после закрытого Definition of done.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Хранилище: схема, дыра, `newest_id` | `go test -count=1 ./internal/store` | зелёные, включая новые тесты `Catchup`, `Gap`, миграции 14 | not_run |
| Настройка `catchup.enabled` | `go test -count=1 ./internal/config ./internal/cli -run 'Setting|Template|Catchup'` | зелёные; ключ в умолчаниях, переменная перекрывает базу, неверное значение код 2 | not_run |
| Менеджер и Daemon | `go test -count=1 ./internal/daemon` | зелёные; существующие тесты истории без правок ожиданий | not_run |
| Очередь с приоритетами | тест шага 5 | порядок «ручная, догонка, фоновая»; новый чат в полосе догонки в порядке прихода; `catchup` не отменяется ручным `start` и концом окна, автозагрузка отменяется | not_run |
| Проход дыры ручным `start` | тест шага 8 | первая страница с offset `gap_to`, дыра закрыта, `history_limit` не обрывает проход; четыре флага сужают дыру, повторный `start` идёт с нового `gap_to`; `--messages 5000` на дыре в 300 сообщений: 5000 этой задачей у `complete = 0`, `done` сразу после дыры у `complete = 1` | not_run |
| Условия метки (Q4) | тест шага 3 | без входа, без готовности, с `auth_lost`: метка не пишется | not_run |
| CLI, RPC, документация команд | `go test -count=1 ./internal/cli ./internal/rpc` | зелёные; тест разделов `docs/cli/` видит `tg chat sync catchup` | not_run |
| Таблицы флагов | `just docs` и `git diff docs/cli` | меняются только блоки новой команды и полей | not_run |
| Навык | `just skill-check` | расхождений с `tg schema` нет | not_run |
| Вся проверка | `just check` | формат, `vet`, тесты зелёные | not_run |
| Нет запросов при выключенном выключателе | тесты шагов 4 и 7 | фейковый клиент: 0 вызовов `Chats` и `HistoryPage` при любом из двух ключей `false` | not_run |
| Нет догонки без простоя | тест шага 3 | простой 9 минут и первый старт: 0 задач | not_run |

`go test -race` не запускается (запускает только владелец по своей команде). Живые проверки не входят в план: см. «Live check».

## Live check

Владелец отказался от живых проверок по умолчанию. Не проверено на живом аккаунте: поведение `updates.Manager` после простоя больше суток, реальное число страниц догонки и `FLOOD_WAIT` на сотнях чатов, длительность догонки, архивные диалоги в обходе. Список ведётся в одном месте: `FEATURES.md`, пункт 10.21 (и 8.2-8.4 для менеджера обновлений). Живая проверка, если владелец её даст, только читает: `tg chat sync catchup --dry-run` после простоя, затем `tg chat sync status` с паузами в десятки секунд.

## Deploy / rollback

- [ ] Перед деплоем: копия базы профиля (`db/<profile>/`), как перед миграциями 0010 и 0013; путь копии в Run log.
- [ ] Деплой только по команде владельца (`КПД` или явная просьба): `just install` собирает, ставит `~/.local/bin/tg` и перезапускает Daemon. Первый запуск нового бинарника пишет метку (после входа) и догонки не ставит (метки ещё нет).
- [ ] Догонка включена по умолчанию (`catchup.enabled = true`, `background_sync.enabled = true`): после деплоя она сработает у каждого профиля при первом простое больше 10 минут. Отключить без отката: `tg settings set catchup.enabled false` и перезапуск Daemon (`just install` или `tg daemon stop` и `tg daemon start`); на время до перезапуска `TG_CLI_CATCHUP_ENABLED=false` при старте Daemon. Идущие задачи догонки снимает `tg chat sync cancel --all`.
- [ ] Откат: остановить Daemon, вернуть копию базы, поставить прежний бинарник. Без копии откат невозможен: база версии 14 не открывается старым бинарником.
- [ ] Очередь и задания живут в памяти, после перезапуска не возобновляются; кэшей, cron и CDN нет. Включённый launchd-автозапуск (план `20261121_2134_tg_autostart`) перезапустит Daemon сам.

## Cleanup

- [ ] Временные файлы только в `var/agents/plans/20261122_2245_chat_catchup_after_downtime/`.
- [ ] Временные файлы не в git.
- [ ] Нужное позже (базовая линия тестов, список мест с ключами настроек, результаты проверок) перенесено в Run log плана.

## Definition of done

- [ ] Код изменён только в рамках Scope.
- [ ] Main invariant держится: тесты шагов 3, 4, 5 и 7 зелёные, существующие тесты истории без правок ожиданий.
- [ ] Все строки Verification `ok` или отклонение записано.
- [ ] `docs/cli/`, `docs/contracts.md` (включая «on by default»), `FEATURES.md` 10.21, навык, changelog, решения обновлены одним коммитом с кодом.
- [ ] Нет шагов `- [0]`.
- [ ] Нет временных файлов в `.agents/plans`.

## Known facts

Снимок на 2026-11-23, ветка `feat/history-limits` (`bd457af`; код как в `7f811a0`).

- `sync_state` пишут только `SaveHistoryPage` (`internal/store/sync_state.go:68-140`) и `MarkHistoryLimitReached` (`:172-190`); `ResetSyncState` (`:143`) удаляет строку. Вызовы `SaveHistoryPage`: `history_sync.go:490, 509`; вызов `MarkHistoryLimitReached`: `history_sync.go:538`, `chat_limit.go:76`. Живые события `sync_state` не трогают. `SaveHistoryPage` двигает `oldest_id` через `MIN`, `newest_id` через `MAX` (`:111-120`).
- `SyncState` читают: `export_month.go:123`, `history_sync.go:618, 684`, `new_chat_sync.go:32`, `history_sync_queue.go:123, 189`, `background_sync.go:260`; SQL-чтения `sync_state`: `store/dashboard.go:39`, `store/background_sync.go:63`, представление `chats_state` (`0010_first_message_date.sql:40`).
- Состояние загрузки чата отдаёт `chat overview` (поля `history_complete`, `history_first_message_date` через `chats_state`, `internal/store/overview.go:39-43`), не `chat info`: в коде `chat info` вызова `SyncState` нет.
- Даты верхнего сообщения диалога в таблицах нет. `dialog_state.data` хранит `top_message` (id, без даты, `internal/tgclient/owner_state.go`), только после `chat state` с Telegram, это снимок. Свежий список диалогов даёт `client.Chats`: у `rpc.Chat` есть `LastMessageDate` из `e.Last.GetDate()` (`internal/tgclient/convert.go:21-23`) и `Pinned`. Новой выборки в `tgclient` не нужно.
- Страница истории: `messages.getHistory` по 100 (`telegramPageSize`, `internal/tgclient/history.go:17`), `offset 0` значит от верха, offset `N` отдаёт сообщения с id меньше `N`; каждый такой запрос идёт фоновой полосой дросселя (`historyPageFromContext`, `internal/tgclient/throttle.go:118-127`). Чтение диалогов фоновую метку получает только через `throttle.WithBackground` (как `refreshFolders`, `background_sync.go:346`).
- Очередь: `historySyncManager` с одним воркером (`history_sync.go:60-111, 366-396`), FIFO через `append` в `enqueueLocked` (`:156-185`), пауза между задачами отсутствует. Одна задача на чат в `m.jobs[chatID]`.
- Источники задач: `start` и `chat sync queue` идут через `startDepth` с `source: manual` (`:126-138`, `history_sync_queue.go:91, 229`); расписание через `startBackground` с `source: background`, только в пустую очередь (`:144-154`); автозагрузка нового чата через `startAuto` с `source: background`, в конец очереди, ничего не отменяя, чат с активной задачей пропускается (`:707-723`).
- Отмена: `startDepth` при `!state.Complete && !depthMet` зовёт `cancelBackgroundLocked` (`:135-137`), который отменяет все задачи `source: background`, в очереди и идущую (`:224-233`); значит, ручной `start` незагруженного чата отменяет и задачи расписания, и задачи автозагрузки нового чата. Конец окна зовёт `cancelBackground` с тем же правилом (`background_sync.go:370`). Ручной `start` загруженного чата или с выполненной глубиной ничего не отменяет. Отмена читает только `source` задачи; места в очереди она не смотрит, поэтому полоса по виду задачи (Q10) правил отмены не меняет.
- Глубина и пределы: `depth.satisfied` (`history_sync_depth.go:28-36`) и `historyLimit.reached` (`chat_limit.go`) сравнивают число всех сохранённых сообщений чата и дату самого старого из них (`store.MessageBounds`, `sync_state.go:154`); `historySyncStart` считает `depthMet` до постановки (`history_sync.go:622-634`), и задача с `depthMet` или `complete` сразу `done` без запроса (`:175-178`); `runTask` после каждой страницы проверяет глубину (`:520-529`) или `historyLimitFor` (`:530-547`), а при пределе `MarkHistoryLimitReached` ставит `complete = 1`. Умолчания пределов: `history_limit.since` 2022-02-02, `history_limit.messages` 200000 (`internal/config/history_limit.go:13-16`). Флаги CLI `--days` и `--months` превращаются в `since` (`internal/cli/chat_sync_cmd.go:65-100`).
- Планировщик: `startBackgroundSync` создаёт `d.background` только при `background_sync.enabled` (`background_sync.go:85-92`); первый запуск через 40-180 минут после старта; `runOnce` пропускает запуск при `auth_lost`, неготовом клиенте или активной задаче (`:228-232`). Пауза страницы фоновой задачи `pagePause`, по умолчанию 2-8 секунд (`internal/config/settings.go:45-46`).
- Готовность без запроса: `backgroundSyncReady` (`background_sync.go:462-482`) смотрит, закрыт ли `client.Ready()`, и есть ли `d.account.user`; пользователя ставит горутина старта через `client.Self` и `setIfEmpty` (`daemon.go:388-404`). `auth_lost` читается `getAuthLost` (`daemon.go:902-906`), сбрасывается при входе (`:750`). `Ready()` закрывается один раз и обрыв связи не отражает.
- Настройки: реестр `settingSpecs` (`internal/config/settings.go:32-80`): имя, тип, умолчание JSON, переменная `TG_CLI_...`, флаг, режим `live`/`restart`. `background_sync.enabled`: `bool`, `true`, `TG_CLI_BACKGROUND_SYNC_ENABLED`, без флага, `restart` (`:40`). Ключи `restart` доходят до Daemon через поле конфигурации (`config.go:36-37`, умолчания `:249`, сборка `settings.go:302`), карту `internal/cli/daemon_profile_database.go:26-45` и разбор окружения в CLI (`background_sync_config.go:56-64`); живые ключи перечислены в `internal/daemon/settings.go:158` и читаются через `liveSetting`. Тесты ключей: `internal/config/settings_test.go` (`TestSettingDefaults` сверяет число ключей, `TestTemplateDocumentedSettingDefaults`, таблица разбора). Документация ключей: `docs/cli/overview.md:337-360`, `docs/cli/settings.md:15`.
- `background_sync.enabled` по умолчанию `true` (`internal/config/settings.go:40`, применяется при перезапуске).
- Источник задачи виден и в панели веб-просмотрщика: `rpc.DashboardSyncJob.Source` (`internal/rpc/dashboard.go:129`, заполняется в `internal/daemon/dashboard.go:140`).
- Старт и остановка Daemon: `Run` (`daemon.go:152`), менеджер истории `:266`, планировщик `:302`, ожидание `waitForShutdown` `:459`, остановка `:461-503`; idle-стоп отменяет тот же контекст (`:442-452`, `watchIdle` `:507-527`); SIGINT/SIGTERM через `signal.NotifyContext` (`cmd/tg-cli/main.go:101`); база закрывается `defer` (`daemon.go:218`) после последовательности остановки.
- `account_state`: `GetAccountState`/`SetAccountState` (`internal/store/account_state.go`), значения JSON; занятые префиксы `updates.state.`, `updates.channel.`, `events.high_water`, `chat.message_limit.`, `chat.history_mode.`.
- Миграции: файлы `internal/store/migrations/NNNN_*.sql`, версия в `PRAGMA user_version`, последняя `0013_settings.sql`, следующая `0014`. Ветка зависимости миграций не добавляет.
- Менеджер обновлений: `updates.New(c.updateConfig())` (`internal/tgclient/client.go:114`), `gap` шлют колбэки `OnChannelTooLong`, `OnChannelGap`, `OnChannelInaccessible` (с id канала) и `OnTooLong` (без чата) (`updates_events.go:228-232`).
- `chat.history_mode.<id>` (`skip`, `first`, `auto`, `internal/store/web.go:83-101`) учитывает только выбор планировщика (`store/background_sync.go:64-66`); автозагрузка нового чата его не смотрит.
- `FEATURES.md`: последний пункт раздела 10 сейчас 10.20, новый получает 10.21.

## Cost

Оценка по коду, не измерена (Q6: оценку оставить как есть). Обход диалогов 100 на запрос, для 300 активных чатов 3-4 запроса (больше, если среди свежих много закрытых списками). На чат от 1 до 10 страниц (`1000 / 100`). При 450-600 страницах и паузе 2-8 секунд (в среднем 5) перед каждой страницей догонка идёт около 40-50 минут фона на 300 активных чатов.

Известные следствия решений владельца:

- Q5: догонка включена по умолчанию у каждого профиля; первая сработает при первом простое больше 10 минут после деплоя.
- Q8: чат с `chat.history_mode = skip` догоняется как любой другой и получит до 1000 сообщений за одну догонку.
- Q2: пока идёт догонка, расписание не ставит глубоких задач (`runOnce` пропускает запуск при активной задаче), так что глубокая загрузка сдвигается на время догонки.
- Q4: долгий обрыв связи при живом Daemon не догоняется.
- Q9, Q11: у флага `--messages N` два смысла в трёх случаях: у чата без дыры «N сохранённых всего»; при проходе дыры и в продолжении той же задачи от `oldest_id` у чата с дырой «N сообщений этой задачи». `start --messages 500` у чата без дыры с 3000 сохранёнными сообщениями кончается без запроса, а у чата с дырой загружает 500 сообщений этой задачей (5 страниц по 100; при N не кратном 100 задача превышает N меньше чем на страницу). `start --messages 5000` на дыре в 300 сообщений у чата с `complete = 0` загружает 5000 сообщений: 300 дыры и 4700 ниже `oldest_id`; у чата с `complete = 1` та же команда загружает только дыру. Проход с флагом, остановленный до `gap_from`, оставляет суженную дыру, и закрыть её можно только новыми `start`.
- Q10: новый чат при очереди догонки ждёт задачи догонки, поставленные раньше него: в худшем случае почти всю догонку, около 40-50 минут.

## Assumptions

- Пропуск менеджера после 10 суток будет: `getChannelDifference` для канала с большим потоком отвечает `channel_too_long` (это записано в контракте как поведение gotd), `getDifference` для личных чатов и групп может вернуть `too_long`. Не проверено на живом аккаунте.
- Архивные диалоги в обход списка, вероятно, не входят, как и у `chat sync queue`: обход зовёт `messages.getDialogs` без `folder_id`. Не проверено.

## Mismatches found

- `docs/contracts.md:14` и решение `20261120_1651` (и его имя файла): `background_sync.enabled` «off by default»; код: `true` по умолчанию (`internal/config/settings.go:40`); `FEATURES.md` 10.16.1, `docs/cli/overview.md:337`, `docs/cli/settings.md:15` и навык пишут «включён по умолчанию». Правда в коде. Шаг 11 исправляет контракт на «on by default», шаг 12 записывает это в новом решении, файл `20261120_1651` молча не правится.
- Бриф: дыру показать там, где `chat info` отдаёт состояние загрузки; по коду состояние загрузки отдаёт `chat overview`.
- Ответ на Q1 говорит «от верха вниз до `gap_from`»; план начинает проход с верхнего края дыры (offset `gap_to`), а не с верха чата. Владелец подтвердил D-A4 2026-11-23, расхождение снято.

## Run log

- 2026-11-22: план написан планировщиком по брифу `var/agents/plans/20261122_2245_chat_catchup_after_downtime/brief.md`; код не менялся.
- 2026-11-23: ответы владельца на Q1-Q8 внесены по `var/agents/plans/20261122_2245_chat_catchup_after_downtime/revision1.md`; добавлены Q9, Q10, D-A7, D-A8, шаги 4, 5, 8; код не менялся.
- 2026-11-23: по `var/agents/plans/20261122_2245_chat_catchup_after_downtime/revision2.md` внесены ответы владельца на Q9 и Q10, D-A1-D-A8 подтверждены владельцем; D-A4 дополнено сужением дыры, добавлен Q11; код не менялся.
- 2026-11-23: по `var/agents/plans/20261122_2245_chat_catchup_after_downtime/revision3.md` внесён ответ владельца на Q11; открытых вопросов нет; код не менялся.
- 2026-11-23: владелец сказал «Делай»; план `in_progress`; ветка `feature/20261122_2245_chat_catchup_after_downtime` в worktree `../tg-cli-catchup` от `feat/history-limits` (слияние в `main` не делалось); исполнитель Codex `gpt-6.1-terra`.
- 2026-11-23: шаг 1 выполнен: `just check` базовой линии зелёный (28 пакетов `ok`, exit 0). Atlas недоступен в песочнице Codex (кэш `uv`), менеджер разрешил замену на `rg`; в Known facts исправлено: вызов `history_sync.go:538` относится к `MarkHistoryLimitReached`.
- 2026-11-23: волна A (шаги 2-4) принята: `just check` зелёный (28 пакетов `ok`, exit 0), коммит `d44cc85`. Три теста ждут волны B (простой 9 и 11 минут, ноль вызовов при `catchup.enabled = false`, остановка через `Run`); замечания ревью к волне B: битая метка `last_alive_at` не должна останавливать старт Daemon (предупреждение в лог), числа 10 минут, 1000 и 1 минута вынести в константы `internal/daemon`.
- 2026-11-23: волна B (шаги 5-7) принята: `just check` зелёный (28 пакетов `ok`, exit 0), коммит `648a27d`; замечания волны A исправлены. Остались два теста: «идущая фоновая задача не прерывается постановкой догонки» (шаг 5) и остановка через `Run` (шаг 3); оба идут в волну C. Замечание ревью к волне C: необязательные параметры через variadic (`SaveHistoryPage(..., gapPass ...GapPass)`, `enqueueLocked(..., lanes ...historySyncLane)`) заменить явными параметрами, пока API не разошёлся по коду.

## Open questions

| Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- |
| Q1. Как закрыть дыру в чате, где сработал предел 1000 | Без пути закрытия дыра у чата с `oldest_id` ниже дыры или с `complete = 1` остаётся навсегда | (a) `catchup --chat`; (b) ручной `start` сначала проходит дыру; (c) только видна | (b) | decided: Decision log, строка Q1 (2026-11-23) |
| Q2. Что делает ручной `chat sync start` при очереди догонки и что делает ручной `catchup` при идущей глубокой загрузке | Ручной `start` отменяет все фоновые задачи, глубокая задача может идти часами | (a) порядок «ручная, догонка, глубокая», без отмен; (b) `start` отменяет догонку; (c) догонка прерывает глубокую | (a) | decided: Decision log, строка Q2 (2026-11-23) |
| Q3. Значение `source` у задачи догонки в `chat sync status` | С `background` задачу отменят конец окна и ручной `start` | (a) `catchup`; (b) `background` и внутренний признак | (a) | decided: Decision log, строка Q3 (2026-11-23) |
| Q4. Когда пишется `last_alive_at` | Daemon без входа или без сети живёт, но обновлений не получает | (a) всегда; (b) только при входе, готовом клиенте и без `auth_lost` | (b) | decided: Decision log, строка Q4 (2026-11-23) |
| Q5. Включается ли догонка у всех по умолчанию | `background_sync.enabled` по умолчанию `true` в коде | (a) единый выключатель; (b) отдельная настройка `catchup.enabled` | (a) | decided: Decision log, строка Q5 (2026-11-23): выбран (b) с умолчанием `true`, оба выключателя обязательны |
| Q6. Пауза между чатами догонки | Без неё чаты в одну страницу идут подряд на скорости дросселя | (a) пауза перед каждой страницей, первой тоже; (b) только между страницами задачи | (a) | decided: Decision log, строка Q6 (2026-11-23) |
| Q7. Граница ручного `tg chat sync catchup` | После старта метка свежая, ручной запуск не найдёт простоя | (a) граница из метки при старте; (b) флаг `--since` | (a) | decided: Decision log, строка Q7 (2026-11-23) |
| Q8. Учитывает ли догонка `chat.history_mode` = `skip` | Владелец мог исключить чат из загрузки истории | (a) не учитывает; (b) пропускает | (a) | decided: Decision log, строка Q8 (2026-11-23) |
| Q9. Как явные флаги `start` (`--messages`, `--days`, `--months`, `--since`) ограничивают проход дыры | По коду флаги сравнивают число и дату всех сохранённых сообщений чата, у чата с историей ниже дыры они уже выполнены | (a) флаги не действуют во время прохода; (b) флаги ограничивают сам проход, дыра сужается; (c) флаг при дыре даёт код 2 | (b) | decided: Decision log, строка Q9 (2026-11-23) |
| Q10. Где стоит автозагрузка нового чата относительно задач догонки | В полосе фоновых она ждала бы всю догонку | (a) в полосе догонки, FIFO, `source: background`; (b) в полосе фоновых; (c) отдельная полоса впереди | (a) | decided: Decision log, строка Q10 (2026-11-23) |
| Q11. Что значит флаг глубины в задаче `start`, где проход дошёл до `gap_from` раньше, чем флаг сработал, и задача идёт дальше от `oldest_id` (чат с `complete = 0`) | Q9 задаёт смысл флага для прохода дыры, а для продолжения той же задачи не задаёт | (a) снова «N сохранённых всего»; (b) «N сообщений этой задачи» до конца задачи; (c) задача кончается при закрытии дыры | (b) | decided: Decision log, строка Q11 (2026-11-23) |
