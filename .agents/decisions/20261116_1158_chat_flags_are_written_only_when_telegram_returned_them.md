---
decided: 2026-11-16
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: no
paths: [internal/store/telegram.go, internal/daemon/persist.go, internal/tgclient/convert.go]
---
# Флаги чата пишутся в базу, только когда Telegram их вернул

## Context

Флаги `scam`, `fake`, `restricted` приходят в объекте канала и сохраняются вместе с чатом. Сохранение из `chat list` и `chat resolve` шло без флагов, и слияние строки `chats` обнуляло значения, записанные `chat info`.

## Decision

- У входящей записи чата есть признак `FlagsKnown`. `mergeChat` берёт `scam`, `fake` и `restricted` из входящей записи только при `FlagsKnown`; иначе известные значения остаются.
- `chat info`, `chat list` и `chat resolve` ставят признак, когда Telegram вернул объект чата с этими полями. Поля `created_at`, `scam`, `fake`, `restricted`, `restriction_reason` видны в `chat info`; `chat overview` показывает из них только `created_at`.

## Why

- Запись без сведений не должна стирать сведения (отвергнуто: «последнее сохранение побеждает» для флагов).
- Решение принял менеджер по поручению владельца после замечания независимой проверки документации.

## Cost

- Один дополнительный признак в записи чата и в слиянии.
