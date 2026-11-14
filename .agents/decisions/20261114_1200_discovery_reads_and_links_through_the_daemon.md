# Поиск в Telegram, контекст, комментарии и ссылки идут через Daemon, ссылка строится без channels.exportMessageLink, chat mark-read это запись под write

Decided: 2026-11-14
Revised: —

## Context

Агент читал историю чата и искал только по локальной базе (`tg search`): найти можно лишь то, что Daemon уже прочитал. Нельзя было искать в самом Telegram, получить сообщения вокруг найденного, прочитать комментарии к посту канала, передать или открыть ссылку на сообщение и пометить чат прочитанным. Фильтр закрытых чатов ([решение о списках](20261114_0900_access_lists_hide_chats_and_untrusted_text_is_marked.md)) и список `write` ([решение о записи](20261114_1100_writes_are_off_until_the_owner_lists_the_chat.md)) уже есть, дроссель не повторяет запись ([решение о дросселе](20261114_0310_throttle_one_account_gate_60_percent_three_levels.md)).

Аудит gotd v0.166.0: запросы `messages.search`, `messages.searchGlobal`, `messages.getHistory`, `messages.getReplies`, `messages.readHistory`, `channels.readHistory`, `channels.exportMessageLink` есть; `getHistory` и `getReplies` принимают `offset_id` и `add_offset`; у поста флаг обсуждения лежит в `MessageReplies.Comments`.

## Decision

- Поиск в Telegram: `message search <чат> <запрос> [--media photo|video|document|voice|audio|link|gif|pinned] [--limit N] [--cursor C]` (`messages.search`, ответ как у истории, курсор в `next_cursor`) и `message search --public <запрос> [--limit N] [--cursor C]` (`messages.searchGlobal`, хэштеги как текст запроса). Лимит по умолчанию 20, максимум 100. Результат `--public`: `hits` из `{chat_id, chat?, message}`; Daemon отбрасывает до ответа попадания закрытых чатов, без `chat_id` и с `chat_id` сообщения, не совпадающим с `chat_id` попадания. Курсор глобального поиска хранит запрос, peer, ID сообщения и rate, поэтому курсоры чатов с одинаковыми ID не смешиваются. Локальный `tg search` не меняется.
- Контекст: `message context <чат> <id> [--before N] [--after N] [--local]`, по умолчанию по 5, максимум по 50, ответ по возрастанию id, включая само сообщение. Окно берётся из `messages.getHistory` с `offset_id` и `add_offset`, затем упорядочивается и обрезается. CLI всегда передаёт `before` и `after`, включая ноль; без поля в RPC Daemon берёт пять. `--local` читает только локальную базу (`store.MessagesAround`): без Telegram и без входа, только неудалённые сообщения; неизвестное сообщение `not_found`.
- Комментарии: `message comments <чат> <id> [--limit N] [--cursor C]` читает `messages.getReplies` по посту; если `MessageReplies.Comments` нет, `not_found`.
- Ссылки: `chatref` разбирает `t.me/<username>/<id>`, `t.me/<username>/<topic>/<id>`, `t.me/c/<channel>/<id>`, `t.me/c/<channel>/<topic>/<id>` (с `https://`, `http://` или без схемы, query и фрагмент игнорируются; `c/<channel>` даёт `-100<channel>`), остальное код 2 с примером. `tg://privatepost` не поддерживается. `message get|context|comments` принимают пару `<чат> <id>` или одну ссылку. `message link <чат> <id> [--topic N]` собирает ссылку локально: `https://t.me/<username>/<id>` для канала или супергруппы с username, иначе `https://t.me/c/<channel>/<id>`, с темой перед id; для личных чатов и обычных групп код 2. `channels.exportMessageLink` не вызывается; чат разрешается так же, как в других командах.
- Отметка прочитанным: `chat mark-read <чат> [--dry-run]` (`messages.readHistory` или `channels.readHistory` до максимального ID), класс `account_write`, не `dangerous`, `--yes` не нужен. Нужен `write` в `access.json`: иначе код 9, закрытый чат `not_found`. `--dry-run` проверяет доступ и не вызывает метод записи Telegram; разрешение `@username` или телефона может обратиться к Telegram методом чтения. Дроссель считает запрос записью и не повторяет его после FLOOD_WAIT.
- Все новые методы чтения проходят проверку закрытых чатов с прежним `not_found`. Прочитанное из открытых чатов сохраняется в базу как обычная история, из закрытых не сохраняется. Чужой текст помечен `untrusted`; у `message search --public` в схеме отдельный элемент списка `public_item`.

## Why

- Отдельные команды, а не расширение локального `search`: `search` работает офлайн по FTS5 и видит только прочитанное, его ответ и курсор другие. Слить их значило бы сделать результат зависимым от того, что агент прочитал раньше. Отвергнуто: общий `search` с флагом источника.
- Ссылка строится локально, а не через `channels.exportMessageLink`: так форма воспроизводима, включая тему, и ссылка не требует отдельного запроса `channels.exportMessageLink`. Отвергнуто: `channels.exportMessageLink`.
- `chat mark-read` требует `write`: собеседник видит прочтение, значит это запись в аккаунт, а список `write` уже единственная граница записи, второго механизма не нужно. Отвергнуто: `mark-read` без `write` как «почти чтение».
- Нулевое окно контекста передаётся явно, чтобы отличать «ноль» от «не задано».
- `--local` не догружает недостающее: чего нет в базе, того нет в ответе, зато команда не требует ни Telegram, ни входа.

## Cost

- Глобальный поиск быстро упирается во FLOOD_WAIT. Смягчают лимит 20 по умолчанию и общий дроссель; ожидание дольше порога возвращается кодом 6, а не повторяется.
- Окно контекста зависит от семантики `add_offset` у Telegram. На живом аккаунте оно не проверено, как и остальные команды этого решения.
- Прочтение видно собеседнику.
- Пока фильтр чтения выключен, попадания глобального поиска из чужих публичных чатов возвращаются как есть; непустой `allow` отбрасывает их, потому что их нет в списке.
- Не сделано: фильтры поиска по отправителю и дате, поиск по нескольким чатам, контекст по топику, непрочитанное по топикам, `tg://privatepost`.
- Проверено автотестами без Telegram; живая проверка остаётся.
