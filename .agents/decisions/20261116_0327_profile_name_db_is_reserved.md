---
decided: 2026-11-16
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: no
paths: [internal/daemon/paths.go, internal/config/config.go]
---
# Имя профиля db зарезервировано

## Decision

- Профиль с именем `db` запрещён (код 2): `ResolvePaths`, `default_profile`, ключи `[profiles.<имя>]`.
- Каталог `db` пропускается при обходе профилей (`DuplicateProfiles`, миграция конфига).

## Why

- На Linux каталог данных по умолчанию совпадает с `~/.config/tg-cli`, и каталог профиля `db` совпал бы с корнем баз, а `DuplicateProfiles` создал бы там `daemon.lock`.
- Отвергнуто: разрешить имя и разводить каталоги особым случаем. Больше кода, больше риска для данных.
- Решение принял менеджер по поручению владельца.

## Cost

- Одно имя профиля недоступно.
