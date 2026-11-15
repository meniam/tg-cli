---
decided: 2026-11-13
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
paths: [go.mod, internal/tgclient]
---
# Утилита пишется на Go поверх gotd/td, с нуля

## Context

Нужна своя консольная утилита для личного Telegram-аккаунта по MTProto: Daemon,
быстрый отклик, мультиаккаунт, локальная история на SQLite с офлайн-поиском,
главный пользователь AI-агент ([INTENT.md](../../INTENT.md)).

Установленная `tg` (`@miolamio/tg-cli` 0.5.1) написана на TypeScript поверх
gramjs. По данным GitHub на 2026-11-13, gramjs (`gram-js/gramjs`) архивирован
(последний пуш 2026-07-14), Telethon архивирован (2026-02-21), Pyrogram
архивирован (2024-12-23).

## Decision

- Язык Go, MTProto-клиент [gotd/td](https://github.com/gotd/td) (MIT, пуш
  2026-11-13, 2385 звёзд, `go 1.25.0`).
- Утилита пишется с нуля: форк `miolamio/tg-cli` невозможен, он на TypeScript.
- Мультиаккаунт и история в SQLite закладываются в основу, а не добавляются
  позже. Драйвер SQLite и CLI-фреймворк этим решением не выбраны.

## Why

- В `gotd/td` есть пакеты `updates`, `takeout`, `peers`, `downloader`,
  `uploader`, `message`, `query`; в `gotd/contrib` есть middleware `floodwait`
  и `ratelimit`. Это покрывает живой поток, синхронизацию истории, загрузку
  медиа и защиту от FLOOD_WAIT без собственного кода.
- README `gotd/td` заявляет около 150 КБ на простаивающий клиент и тысячи
  клиентов в одном процессе: Daemon с несколькими аккаунтами обходится
  дёшево. Один статический бинарь для Daemon и CLI.
- На gotd/td уже работают tdl (загрузчик) и kaosb/tgcli (SQLite с FTS5).
- Отвергнуто: TypeScript и mtcute. Живой и типобезопасный (591 звезда, MIT),
  но рантайм Node или Bun и один поток на процесс. Подходил бы, если бы
  важнее была скорость разработки.
- Отвергнуто: TypeScript и teleproto. Молодая библиотека (312 звёзд), опора на
  код архивного gramjs.
- Отвергнуто: Rust и grammers. Единственная серьёзная библиотека: версия 0.12.0,
  Codeberg, 77 звёзд, API нестабильно, готовых takeout и floodwait не найдено.
  GitHub-копия архивирована.
- Отвергнуто: Python. Telethon и Pyrogram архивированы, Kurigram под LGPL-3.0,
  Python хуже для быстрого Daemon и упаковки.
- Отвергнуто: TDLib. C++ и биндинги, своя зашифрованная локальная БД
  дублирует нашу SQLite.

## Cost

`gotd/td` низкоуровневая: высокоуровневые обёртки над диалогами, поиском и
выдачей пишем сами. Не проверено прототипом: хватает ли
`gotd/td/telegram/updates` для восстановления пропущенных апдейтов; решение
пересматривается, если не хватает.
