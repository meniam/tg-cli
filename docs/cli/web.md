# Local web interface

## tg web

`tg web [--profile PROFILE] [--listen IP:PORT] [--open]`

Browse the selected account's locally saved folders, chats and messages. Start
the daemon and sign in before starting the server. The server runs until
Ctrl+C. The command prints one JSON envelope with the URL, listen address and
profile. Use `--open` to open the URL in the default browser.

<!-- schema:flags tg web -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--listen` | string | `none` | listen address with port (default loopback, scan ports 1313 to 1412) |
| `--open` | bool | `false` | open the viewer in the default browser |
<!-- /schema:flags -->

```sh
tg web --open
```

```json
{"ok":true,"data":{"url":"http://127.0.0.1:1313/#token=…","listen":"127.0.0.1:1313","profile":"default"}}
```

Treat the URL as a credential. The token expires when the process stops.
The browser exchanges the fragment for an HttpOnly, SameSite=Strict cookie.
By default the server looks for a free port from 1313 to 1412 on
`127.0.0.1`. An explicit port must be free. An explicit non-loopback address
exposes the interface over plaintext HTTP and prints a warning.

The `[web]` keys `listen` and `open` have environment overrides
`TG_CLI_WEB_LISTEN` and `TG_CLI_WEB_OPEN`. Command-line flags take precedence.
The daemon reads `cache_bytes` (default 33554432) and `cache_ttl` (default
`168h`) from the same section on startup.

The browser shows only the history the daemon has saved. Use
[`tg chat sync`](chats.md#tg-chat-sync) to load more history. Folder rules are applied to the local snapshot. If the folder list has not
been saved yet, run [`tg folder list`](folders-and-account.md#tg-folder-list). Miniatures require the downloads directory next
to `config.toml` to exist and appear in the policy's `downloads` list. The
daemon stores them in `.tg-web/<profile>/<chat_id>/`.

The search field above the chat list searches all saved messages. Use the
search button in the chat header to search that chat. A result opens the
message in context.

Select the chat name to see its details, pinned messages and local history
mode. **Attachments** lists files and other media from the messages loaded
in the current view. **Access** shows the profile’s access policy. The
browser is read-only. See [Access policy](access.md).

Exit codes include 1 for a server error, 2 for invalid options, 3 if the daemon
is absent and 4 if there is no authorized session. [Exit codes](overview.md#exit-codes)
