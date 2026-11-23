---
date: 2026-11-24T00:50:45+03:00
type: Исправлено
decision: 20261124_0050_deleted_flag_of_people_comes_from_the_stored_object
---
Private chats and authors of deleted accounts are now marked `sender.deleted: true` from the stored Telegram object; migration 0017 marks the people already stored and fills missing names.
