---
decided: 2026-11-20
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
revises: [20261114_0900_access_lists_hide_chats_and_untrusted_text_is_marked]
paths: [docs/cli/access.md, internal/daemon/access.go]
---
# tg settings показывает id чатов из allow и block только с фразой владельца

## Context

Решение [о списках чатов](20261114_0900_access_lists_hide_chats_and_untrusted_text_is_marked.md)
прячет закрытые чаты: ни одна команда не показывает агенту их id
(`docs/cli/access.md`, «What a closed chat looks like»), `daemon status` даёт
только числа. С командой `tg settings` списки можно прочитать из базы.
Владелец 2026-11-20 принял вариант B вопроса Q4 плана
`20261120_2242_settings_in_database`.

## Decision

- `tg settings list` и `settings.list` показывают для `access.allow` и
  `access.block` только число элементов, как `daemon status`.
- `access.write`, `access.admin`, `access.files`, `access.downloads` и флаги
  `access.*` читает любой вызывающий.
- `tg settings get access.allow` и `get access.block` с id отвечают только после
  верной фразы владельца, набранной в терминале; проверка живёт в Daemon и в
  офлайн-функции пакета `daemon`, неверная фраза считается в ограничении подбора.

## Why

- Отвергнуто: читает любой (вариант A). Агент узнал бы id чатов, скрытых от него.
- Отвергнуто: все `access.*` только с фразой (вариант C). Агенту нужно знать,
  куда ему можно писать и откуда брать файлы, иначе он не составит запрос.

## Cost

- Два режима вывода одного ключа: числа без фразы и id с фразой.
- Чтение `allow` и `block` без фразы тоже даёт код отказа; навык `docs/skill/tg`
  объясняет агенту, что просить их не нужно.
