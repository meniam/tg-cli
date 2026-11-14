# Decision Log

Index of `.agents/decisions/`. Rules: `decisions.md`.

- 2026-11-13 — [Утилита пишется на Go поверх gotd/td, с нуля](20261113_2050_language_is_go_with_gotd_td.md)
- 2026-11-13 — [Daemon говорит по JSON-RPC 2.0 через Unix-сокет](20261113_2051_daemon_speaks_json_rpc_over_unix_socket.md)
- 2026-11-13 — [Код лежит в cmd/ и internal/, без src/ и pkg/](20261113_2052_code_lives_in_cmd_and_internal.md),
  revised by [cli импортирует daemon ради путей, Telegram-клиент подставляет main](20261113_2113_cli_imports_daemon_paths_and_main_injects_telegram.md)
- 2026-11-13 — [Вход в аккаунт идёт через Daemon по RPC](20261113_2109_login_goes_through_daemon_rpc.md)
- 2026-11-13 — [CLI-фреймворк cobra](20261113_2110_cli_framework_is_cobra.md)
- 2026-11-13 — [Данные профиля лежат в каталоге профиля, ключи приложения берутся из окружения](20261113_2111_profile_layout_and_credentials_from_env.md),
  revised by [Вход идёт по пресету официального клиента, своё приложение необязательно](20261113_2116_login_uses_official_client_presets.md)
- 2026-11-13 — [Ответ CLI это JSON-конверт, коды выхода версии 0 закреплены](20261113_2112_json_envelope_and_exit_codes_v0.md)
- 2026-11-13 — [cli импортирует daemon ради путей, Telegram-клиент подставляет main](20261113_2113_cli_imports_daemon_paths_and_main_injects_telegram.md),
  revises [Код лежит в cmd/ и internal/, без src/ и pkg/](20261113_2052_code_lives_in_cmd_and_internal.md),
  revised by [cli зависит от store только через daemon, прямой импорт запрещён](20261114_0005_cli_may_reach_the_store_only_through_the_daemon.md)
- 2026-11-13 — [Вход идёт по пресету официального клиента, своё приложение необязательно](20261113_2116_login_uses_official_client_presets.md),
  revises [Данные профиля лежат в каталоге профиля, ключи приложения берутся из окружения](20261113_2111_profile_layout_and_credentials_from_env.md),
  revised by [Имя устройства по умолчанию MacBook Pro M1 BRN, --device переопределяет](20261113_2218_device_name_is_macbook_pro_m1_brn_by_default.md)
- 2026-11-13 — [Поток входа свой: повторная отправка кода и вход по QR](20261113_2202_login_flow_is_our_own_with_resend_and_qr.md)
- 2026-11-13 — [Имя устройства по умолчанию MacBook Pro M1 BRN, --device переопределяет](20261113_2218_device_name_is_macbook_pro_m1_brn_by_default.md),
  revises [Вход идёт по пресету официального клиента, своё приложение необязательно](20261113_2116_login_uses_official_client_presets.md)
- 2026-11-13 — [Хранилище: одна SQLite на профиль, история подгружается по команде и медленно](20261113_2230_storage_is_one_sqlite_per_profile_with_slow_opt_in_history.md),
  revised by [Драйвер SQLite modernc.org/sqlite, нормализацию «ё» делаем сами](20261113_2301_sqlite_driver_is_modernc_with_own_yo_normalization.md)
- 2026-11-13 — [Пометки лежат отдельно, AI пишет их без подтверждения](20261113_2231_notes_are_separate_and_ai_writes_them_without_confirmation.md)
- 2026-11-13 — [Чтение страницами с курсором по id сообщения, JSONL с завершающей строкой курсора](20261113_2250_reading_pages_by_message_id_cursor_and_jsonl_with_cursor_line.md)
- 2026-11-13 — [Драйвер SQLite modernc.org/sqlite, нормализацию «ё» делаем сами](20261113_2301_sqlite_driver_is_modernc_with_own_yo_normalization.md)
- 2026-11-14 — [cli зависит от store только через daemon, прямой импорт запрещён](20261114_0005_cli_may_reach_the_store_only_through_the_daemon.md),
  revises [cli импортирует daemon ради путей, Telegram-клиент подставляет main](20261113_2113_cli_imports_daemon_paths_and_main_injects_telegram.md)
- 2026-11-14 — [Команда schema описывает команды, классы безопасности и поля ответов](20261114_0030_schema_command_with_safety_classes_and_field_lists.md)
- 2026-11-14 — [Форматы вывода: toon и human рядом с json, режимы quiet и verbose](20261114_0031_toon_human_quiet_verbose_output_modes.md)
- 2026-11-14 — [Daemon уходит в фон по умолчанию, остановка по простою включается флагом](20261114_0032_daemon_starts_in_background_idle_stop_is_opt_in.md)
- 2026-11-14 — [Метод execute не делаем](20261114_0033_execute_is_not_built.md)
- 2026-11-14 — [Дроссель аккаунта один, на 60% от ориентиров ботов, три уровня, автоожидание FLOOD_WAIT до 10 секунд](20261114_0310_throttle_one_account_gate_60_percent_three_levels.md)
- 2026-11-14 — [Списки чатов в access.json скрывают закрытый чат, пустые списки открывают всё, чужой текст помечен признаком в схеме и рамкой в human](20261114_0900_access_lists_hide_chats_and_untrusted_text_is_marked.md)
- 2026-11-14 — [Живые события идут из менеджера обновлений gotd через журнал SQLite с курсором по id, subscribe шлёт уведомления, message watch печатает jsonl](20261114_1000_live_events_through_gotd_updates_with_stored_cursor.md)

## Open questions

- Как называется утилита.
- Каков риск ограничения аккаунта при входе через пресет официального клиента.
- Какие платформы поддерживаются.
- Какие действия считать опасными (классы безопасности решены, список конкретных действий и подтверждения нет) и как обеспечить безопасность по умолчанию.
- Нужно ли скачивание медиа и в каком объёме (решается в ТЗ).
- Как измерять «быстро» и какие целевые значения.
