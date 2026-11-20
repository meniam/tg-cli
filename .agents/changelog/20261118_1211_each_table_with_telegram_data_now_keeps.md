---
date: 2026-11-18T12:11:51+03:00
type: Изменено
decision: 20261118_1211_raw_keeps_telegram_objects_with_type_keys_and_no_access_hash
---
Each table with Telegram data now keeps the received Telegram object in raw as JSON: keys by TL type and schema, no access_hash, no session hash.
