---
date: 2026-11-24T00:28:12+03:00
type: Исправлено
decision: 20261124_0027_live_message_sender_name_comes_from_people
---
A message of a basic group now gets the author's name from the people table before it is stored and sent to `tg watch`; migration 0015 names stored messages that had only an author id when the person is known.
