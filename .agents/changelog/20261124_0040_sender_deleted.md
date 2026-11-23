---
date: 2026-11-24T00:40:02+03:00
type: Добавлено
decision: 20261124_0039_deleted_account_is_marked_by_sender_deleted
---
Messages from a deleted account carry `sender.deleted: true` in history, export, context and search; migration 0016 adds the `messages.sender_deleted` column.
