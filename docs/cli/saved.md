# Saved Messages

## tg saved tags

Lists Saved Messages tags. A peer filter restricts the list to one source. Tag titles are untrusted.

<!-- schema:flags tg saved tags -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--peer` | string | `none` | filter by Saved Messages source |
<!-- /schema:flags -->

## tg saved tag-rename

Renames or clears a tag title: `tg saved tag-rename <emoji> --title <title>` or `tg saved tag-rename <emoji> --clear`. The owner's own chat ID must be in `write` in `access.json`.

<!-- schema:flags tg saved tag-rename -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--clear` | bool | `false` | remove the tag title |
| `--dry-run` | bool | `false` | preview without changing Telegram |
| `--title` | string | `none` | new tag title |
<!-- /schema:flags -->

## tg saved history

Reads messages from a Saved Messages source: `tg saved history <peer>`. Closed sources are inaccessible.

<!-- schema:flags tg saved history -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue from the previous page |
| `--limit` | int | `50` | maximum messages, 1 to 100 |
<!-- /schema:flags -->

## tg saved search

Searches Saved Messages by query, tag, or both. The optional peer restricts the source.

<!-- schema:flags tg saved search -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue from the previous page |
| `--limit` | int | `50` | maximum messages, 1 to 100 |
| `--peer` | string | `none` | filter by source |
| `--tag` | string | `none` | filter by tag |
<!-- /schema:flags -->

## tg saved list

Lists Saved Messages sources. Closed sources are omitted, and titles are untrusted.

<!-- schema:flags tg saved list -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue from the previous page |
| `--limit` | int | `50` | maximum dialogs, 1 to 100 |
<!-- /schema:flags -->
