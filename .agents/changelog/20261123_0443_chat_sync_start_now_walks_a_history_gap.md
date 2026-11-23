---
date: 2026-11-23T04:43:43+03:00
type: Изменено
decision: 20261123_0439_history_gap_and_manual_pass
---
chat sync start now walks a history gap that a catchup left (gap_from, gap_to in chat sync status and chat overview) before the usual load; export-month does not claim the month start across a gap.
