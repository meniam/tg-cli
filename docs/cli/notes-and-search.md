# Notes, local search and the tool description

Commands that work on the local database of the profile, and the two commands that describe the tool: `note add|list|delete`, `search`, `version` and `schema`. The shared rules (profiles, global flags, the envelope, exit codes) are in [overview.md](overview.md). All examples use fictional data.

The local database is one SQLite file per profile: `<config dir>/db/<profile>/store.db`, mode `0600`, plain text. It sits next to the configuration file and not in the data directory, so `home` and `TG_CLI_HOME` do not move it; see [profiles and the data directory](overview.md#profiles-and-the-data-directory) for the layout and for the rule that keeps `db` out of a file sync. The Daemon saves every chat, person and message it reads from Telegram into it, and full-text search (FTS5) works on it without a Telegram connection. Search text is lower-cased and `ё` is folded into `е`, so case and `ё` do not matter. The driver is `modernc.org/sqlite` (pure Go, FTS5, no cgo). The database holds other people's conversations, protected only by the file mode and the disk encryption of your machine.

Every table that holds Telegram data keeps the received object in a `raw` column: `chats`, `people`, `messages`, `message_versions`, `folders`, `chat_versions`, `dialog_state`, `account_sessions`, `blocked_peers`, `notify_defaults`, `events` and `transcriptions`. The value is JSON of the Telegram object as the Daemon received it. The first key `"_"` names the TL constructor (`message`, `channel`, `peerUser`), the other keys follow the TL schema (`from_id`, `edit_date`), unused and zero fields are left out, and `access_hash` is removed at any depth. A row of `chats`, `people` or `transcriptions` is built from several answers, so its `raw` is an object keyed by the TL name of the method that returned a part (`messages.getDialogs`, `channels.getFullChannel`) or by the constructor name of an update (`updateTranscribedAudio`); a new answer of the same method replaces only its own key. A `transcriptions` row written by the local engine holds no Telegram object: its `raw` is `{"voice_local":{"engine":"gigaam-v3","segments":[{"start":0.0,"end":2.4,"text":"..."}]}}`, the phrases with their times in seconds (untrusted text). An answer about one row is kept whole, with its `chats` and `users` arrays. No session key is there either: the `hash` of an `authorization` object is cut out as well. Rows of `messages` written before this format keep the earlier JSON of the Daemon and differ by the missing `"_"` key, and the next read of that message replaces its `raw`. A `message_versions` row keeps the `raw` copied from the message when the edit was saved, and an `events` row is never rewritten, so an old event stays without `raw`. In the remaining tables `raw` appears only after a read in this format; the earlier `people.raw` and `folders.raw` moved to their own columns. The file is plain text, so `raw` is readable with `sqlite3`:

```sh
sqlite3 ~/.config/tg-cli/db/<profile>/store.db \
  "SELECT json_extract(raw, '$._') AS type FROM messages ORDER BY id DESC LIMIT 5"
sqlite3 ~/.config/tg-cli/db/<profile>/store.db \
  "SELECT raw FROM messages WHERE chat_id = -1001234567890 ORDER BY id DESC LIMIT 1"
```

`raw` holds what the parsed columns leave out: the IP address of a session in `account_sessions`, the phone number of a person who has no row of their own in `people`, the `file_reference` of a media file, and the names and ids of closed chats that Telegram mentioned in the same answer (see [what a closed chat looks like to the agent](access.md#what-a-closed-chat-looks-like-to-the-agent)). No command returns `raw`, and search matches the columns, not this JSON.

## Notes

A note is a text that the owner or the AI agent attaches to a chat, a person or a message. Notes live apart from Telegram data: a reload of chats and messages never changes or deletes them, they survive a Daemon restart, and the AI writes them without the confirmation of the owner. A note has an author (`owner` or `ai`), the model name when the author is `ai`, tags, and creation and edit times. There is no importance field yet. The commands need a running Daemon but no login (the one exception is `note add` for an `@username` the database has never seen).

When the access lists close a chat, its notes are hidden: `note list` does not show them, `note add` for it gives exit code 7 `not_found`, and `note delete` of a note of such a chat gives the same `not_found` as a note that does not exist.

### tg note add

Adds a note to a chat, a person or a message.

```text
tg note add [flags] <body|->
```

<!-- schema:flags tg note add -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--author` | string | `owner` | note author: owner or ai |
| `--chat` | string | `none` | chat reference for a chat or message note |
| `--message` | int64 | `0` | message id when --chat identifies a message note |
| `--model` | string | `none` | AI model name, only with --author ai |
| `--person` | string | `none` | person reference for a person note |
| `--tag` | stringArray | `[]` | tag to add; repeat for multiple tags |
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon. Choose the target: `--chat` for a chat note, `--person` for a person note, or `--chat` together with `--message` for a message note. Without a target the command is exit code 2 (`choose --chat, --person, or --chat with --message`). `--model` works only with `--author ai` (`--model requires --author ai`), and `--author` must be `owner` or `ai`. The body is the argument; `-` reads the whole body from stdin. `--tag` can be repeated. For a negative chat id use `--chat=-12345` or `--chat -12345 -- body`: the `--` ends the flags before a body that starts with `-`. A note for an `@username` the database has never seen asks Telegram to resolve it, which needs a login. The note body is not marked `untrusted`: it is the text of the owner or the agent. An agent signs its notes with `--author ai` and the name of its model.

**Example.**

```sh
echo "Asked for the contract, follow up on Monday" | \
  tg note add --chat=-1001234567890 --message 4102 --author ai --model my-model --tag followup --tag contract -
tg note add --person 987654321 -- "Prefers short messages"
```

**Answer.**

```json
{"ok":true,"data":{"id":2,"kind":"message","chat_id":-1001234567890,"target_id":4102,"body":"Asked for the contract, follow up on Monday","author":"ai","model":"my-model","tags":["contract","followup"],"created_at":"2026-11-14T18:34:24Z","updated_at":"2026-11-14T18:34:24Z"}}
```

**Answer fields** (`data`): `id` integer, `kind` string, `chat_id` integer (optional), `target_id` integer (optional), `body` string, `author` string, `model` string (optional), `tags` array (optional), `created_at` string, `updated_at` string.

`kind` is `chat`, `person` or `message`; `target_id` is the message id of a message note and the person id of a person note.

**Exit codes.** 0; 2 no target, a bad `--author`, `--model` without `--author ai`; 3 no Daemon; 4 only for an `@username` that needs Telegram; 7 the chat is closed or unknown.

**Verification.** Notes about people are not filtered by chat access lists. See [FEATURES.md](../../FEATURES.md), item 10.8.

### tg note list

Lists local notes, newest first.

```text
tg note list [flags]
```

<!-- schema:flags tg note list -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--author` | string | `none` | filter by owner or ai |
| `--before-id` | int64 | `0` | return notes with a smaller id |
| `--chat` | string | `none` | filter by chat reference |
| `--limit` | int | `50` | notes per page, 1 to 500 |
| `--message` | int64 | `0` | filter one message id, requires --chat |
| `--person` | string | `none` | filter by person reference |
| `--tag` | string | `none` | filter by tag |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon. `--chat` filters chat and message notes, `--person` filters person notes, `--chat` with `--message` filters one message; `--message` without `--chat` is exit code 2 (`--message requires --chat and cannot be combined with --person`). Page with `--before-id`: pass the smallest id of the page to get older notes. A page holds 50 notes by default and 500 at most. With `--format jsonl` each note is a line and there is no cursor line. For a negative chat id use `--chat=-12345`.

**Example.**

```sh
tg note list --tag followup
tg note list --chat=-1001234567890 --message 4102
tg note list --before-id 3 --format jsonl --fields id,body
```

**Answer.**

```json
{"ok":true,"data":{"notes":[{"id":3,"kind":"person","target_id":987654321,"body":"Prefers short messages","author":"owner","created_at":"2026-11-14T18:34:24Z","updated_at":"2026-11-14T18:34:24Z"}]}}
```

**Answer fields** (`data`): `notes` array, `notes.id` integer, `notes.kind` string, `notes.chat_id` integer (optional), `notes.target_id` integer (optional), `notes.body` string, `notes.author` string, `notes.model` string (optional), `notes.tags` array (optional), `notes.created_at` string, `notes.updated_at` string.

**Exit codes.** 0; 2 `--message` without `--chat` or with `--person`, a bad `--author`; 3 no Daemon.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 9.2 and 10.8.

### tg note delete

Deletes one note by id, or all notes of one author.

```text
tg note delete <id> | --author owner|ai --yes [flags]
```

<!-- schema:flags tg note delete -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--author` | string | `none` | delete all notes by this author: owner or ai |
| `--yes` | bool | `false` | confirm deletion of all notes by author |
<!-- /schema:flags -->

**Safety class:** `local_write`. Dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon. The command is dangerous: a deleted note cannot be restored. A single note by id needs no `--yes`. All notes of an author need `--author owner|ai --yes`; without `--yes` the command is exit code 2 (`would delete all ai notes; confirm with ...`) and deletes nothing. Giving no id and no author, or both, is exit code 2. Note that `--author` also deletes the notes of closed chats and is not filtered by the access lists.

**Example.**

```sh
tg note delete 2
tg note delete --author ai --yes
```

**Answer.**

```json
{"ok":true,"data":{"deleted":1}}
```

**Answer fields** (`data`): `deleted` integer.

**Exit codes.** 0; 2 no id and no author, both, or no `--yes` for the author mode; 3; 7 the note does not exist (`not found`) or belongs to a closed chat.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.8.

## Local search

### tg search

Full-text search in the local database: chats, people, messages and notes. It needs no Telegram connection and no login, only the running Daemon. It finds only what the Daemon has already loaded or saved: read a chat first (`message history`), load a whole chat with `chat sync start`, and search Telegram itself with [`message search`](messages-read.md#tg-message-search). In the `messages` scope it knows word forms (stemming, see [Word forms](#word-forms-and-links-to-messages)), and with `--semantic` it also finds fragments of a conversation by meaning (see [Semantic search](#semantic-search)).

```text
tg search [flags] <query>
```

<!-- schema:flags tg search -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--author` | string | `none` | filter note hits by owner or ai |
| `--chat` | string | `none` | restrict message and note hits to this chat |
| `--deleted` | bool | `false` | include messages marked deleted |
| `--limit` | int | `20` | hits per scope, 1 to 100 |
| `--min-similarity` | float64 | `0` | with --semantic: lowest cosine similarity of a vector hit, 0 to 1 (0 turns the threshold off; default from [semantic_search] min_similarity) |
| `--prefix` | bool | `false` | match the last query term as a prefix |
| `--scope` | stringSlice | `[]` | scopes: chats, people, messages, notes; repeat or separate with commas |
| `--semantic` | bool | `false` | search conversation fragments by words and meaning (messages only; --limit counts fragments) |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon. Without `--scope` all four scopes are searched. Quote a multiword query in the shell, and put `--` before a query that starts with `-`. FTS5 syntax inside the query is harmless. `--limit` counts hits per scope (20 by default, 100 at most). `--chat` restricts message and note hits to one chat, `--author` filters note hits, `--prefix` matches the last query term as a prefix, `--deleted` includes messages marked deleted. `--scope` accepts repeated values or a comma-separated list; with `--format jsonl` each hit is a line, in scope order. Hits of closed chats are hidden; people (`people`) and notes about people are not filtered. The `snippet` and `title` of a hit are marked `untrusted` (also for a note hit), and so are `chat_title` and `chat_username` of a message hit, so `--human` frames them. `--semantic` switches the command to another mode with its own answer; the rest of this block describes the ordinary mode, and [Semantic search](#semantic-search) the other.

**Example.**

```sh
tg search "invoice" --scope chats,messages --prefix
tg search contract --scope notes --author ai
```

**Answer.**

```json
{"ok":true,"data":{"hits":[{"scope":"messages","id":501,"chat_id":-1001234567890,"snippet":"Sent the invoice for March","date":"2026-11-20T09:15:00Z","rank":-0.0327868852459016,"link":"https://t.me/c/1234567890/501","chat_title":"Release team","chat_username":null},{"scope":"notes","id":2,"chat_id":-1001234567890,"snippet":"Asked for the contract, follow up on Monday","author":"ai","model":"my-model","note_kind":"message","rank":-0.51351890473488}]}}
```

**Answer fields** (`data`): `hits` array, `hits.scope` string, `hits.id` integer, `hits.chat_id` integer (optional), `hits.title` string (optional, untrusted), `hits.snippet` string (untrusted), `hits.date` string (optional), `hits.deleted` boolean (optional), `hits.author` string (optional), `hits.model` string (optional), `hits.note_kind` string (optional), `hits.rank` number (lower is better in every scope), `hits.link` string or null (message hits only; always present for them), `hits.link_unavailable` string (optional, message hits only), `hits.chat_title` string (message hits only; untrusted; empty for a chat the database does not know) and `hits.chat_username` string or null (message hits only; untrusted; `null` when the chat has no username). The chat fields come from the `chats` table in the same query as the hit and need no Telegram request; the `title` of a `chats` hit is unchanged.

**Exit codes.** 0 (also with no hits); 2 a bad `--scope` or `--author`, no query, `--semantic` with a flag it does not accept, or `--min-similarity` outside 0 to 1 or without `--semantic` (see below); 3 no Daemon.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.9 and 13.1.

#### Word forms and links to messages

In the `messages` scope the search runs two legs over the same query and fuses them by Reciprocal Rank Fusion (RRF, constant 60). The exact leg matches the words as written, so numbers, part numbers and names stay exact. The stemmed leg matches the stems of the words (Snowball, Russian and English), so `доставка`, `доставки` and `доставку` find each other without `--prefix`. A word with digits, a name or a word of another language stays as written. Each leg contributes its `--limit` times three best messages, and the fused list is cut to `--limit`. The stems are kept in the database next to the text (schema version 11). Opening a database of an older version converts it once, and an older binary refuses a converted one: copy `store.db` before the update. The text of a voice message or a video note includes its finished transcription (the local engine's or Telegram's, see [voice.md](voice.md)), so both legs find what was said; the transcription of a plain video is stored but not searched. A transcription is untrusted text and a Russian-only engine gives noise on other languages, which lands in the search as it is. The other scopes are unchanged.

`rank` of a message hit is the negated RRF score, so a smaller number is a better hit, as with the `bm25` rank of the other scopes. The value is not comparable with the `rank` of an earlier version.

Every message hit carries `link`, the `t.me` link of the message, built from the chat type, the chat username and the forum topic in the local database, without a request to Telegram. A public chat gives `https://t.me/<username>/<id>`, a private channel or supergroup `https://t.me/c/<chat>/<id>`, and a message in a forum topic `https://t.me/c/<chat>/<topic>/<id>`. A direct chat and a basic group have no web link: `link` is `null` and `link_unavailable` says `private_chat` or `basic_group`. For a chat that is not in the local `chats` table `link` is `null` and `link_unavailable` is absent. A link names the chat and the message number only; it holds no text.

#### Semantic search

`--semantic` searches conversation fragments instead of single messages. A fragment is a stretch of one chat (and one forum topic) cut by pauses in the talk; see [contracts.md](../contracts.md#semantic-search). Three legs find fragments, and RRF fuses them (constant 60): the exact words and the stemmed words of the query over the messages, and the vector similarity of the query to the stored fragment vectors. A fragment found by several legs ranks higher. The rank of a fragment on a word leg is the best rank of its messages; a message that no stored fragment covers (a short one) is returned as a fragment of its own with `indexed` false.

The vector leg runs only when `[semantic_search] enabled = true`, Ollama answers within 10 seconds and `vectors.db` holds fragments of the current model (see [`tg search index`](#tg-search-index-start) and [`[semantic_search]`](overview.md#request-throttle-and-flood_wait)). The Daemon embeds the query through Ollama at the moment of the call and sends it nothing else. When the leg cannot run, the answer is built from the word legs and `vector_skipped` says why. The search never fails because of the vector side.

**Similarity threshold.** The vector leg drops a hit whose cosine similarity to the query (`bge-m3`, a number from 0 to 1) is below `[semantic_search] min_similarity` (default `0.5`), after the top hits are found and before the fusion; the word legs are not filtered. The number of dropped hits is `vector_dropped`. `--min-similarity X` overrides the setting for one query and `--min-similarity 0` keeps every vector hit. If the leg found hits but all of them are below the threshold, `vector_skipped` stays absent (the vector leg ran) and the fragments come from the word legs alone. How to choose the value: for unrelated fragments of real chats the cosine of `bge-m3` has a median of 0.40 to 0.42 and a 99th percentile near 0.5; a good question reaches 0.6 to 0.7 at the top, a short vague one only about 0.55. So `0.5` removes most of the noise and keeps the good hits, `0.55` is stricter (a short vague query may then lose its vector hits), `0.45` is softer (more noise). Raise `--min-similarity` when the answer holds fragments that have nothing to do with the question, lower it when a fragment you expect is missing and `vector_dropped` is above 0. These numbers come from one measurement on one index; they are a starting point, not a guarantee (see [FEATURES.md](../../FEATURES.md), item 13.1).

`--semantic` accepts `--chat`, `--limit` (fragments, 1 to 100, default 20), `--prefix` and `--min-similarity` (0 to 1; a value outside the range, or the flag without `--semantic`, is exit code 2). It refuses `--scope` with any scope but `messages`, `--author` and `--deleted` with exit code 2 and a message that names the flag; deleted messages are not searched. Hits of closed chats are hidden, and a closed chat is never indexed.

```sh
tg search --semantic "когда приедет заказ" --limit 5
```

```json
{"ok":true,"data":{"fragments":[{"chat_id":-1001234567890,"topic_id":0,"first_id":480,"last_id":487,"starts_at":"2026-11-20T09:00:00Z","ends_at":"2026-11-20T09:40:00Z","score":0.03278688524590164,"sources":["exact","vector"],"indexed":true,"text":"Привезут завтра до обеда\nХорошо, жду","link":"https://t.me/c/1234567890/480","match_link":"https://t.me/c/1234567890/483","chat_title":"Release team","chat_username":null}]}}
```

**Answer fields** (`data`): `fragments` array (best first); `fragments.chat_id`, `topic_id` (0 when the chat has no topics), `first_id` and `last_id` integers; `starts_at` and `ends_at` strings (RFC 3339, UTC); `score` number (the RRF score, higher is better); `sources` array of `exact`, `stem` and `vector`, the legs that found the fragment; `indexed` boolean; `text` string (untrusted; the live messages of the fragment from the local database, one per line, at most 1500 characters, cut with `…`); `link` string or null (the first message of the fragment); `match_link` string or null (the message the word legs ranked best; null when only the vector leg found the fragment); `link_unavailable` string (optional, as for message hits); `chat_title` string (untrusted; empty for a chat the database does not know) and `chat_username` string or null (untrusted; `null` without a username), for every fragment, also for a short message returned as a fragment of its own and for one found by the vector leg alone. `vector_skipped` string (optional) is one of `disabled` (the setting is off), `unavailable` (Ollama did not answer in time), `model_missing` (Ollama has no such model), `index_empty` (no fragment is indexed with the current model) and `error` (any other failure); it is absent when the vector leg ran. The text of the query and of the messages never appears in `vector_skipped`. `vector_dropped` integer (always present): the vector hits left out because their cosine similarity was below the threshold; 0 when nothing was dropped or the vector leg did not run. With `--human` a line `vector hits below the similarity threshold: N` follows the result when `vector_dropped` is above 0. A fragment whose every message was deleted after indexing is dropped from the answer. With `--format jsonl` each fragment is a line.

**Limits.** The quality of the vector leg on real conversations is not measured yet, see [FEATURES.md](../../FEATURES.md), item 13.1.

The word `index` is also the name of a subcommand group (`tg search index`). To search for the word itself, put `--` before it: `tg search -- index`. Without it `tg search index` fails with exit code 2 and asks for a subcommand.

### tg search index start

Runs the semantic indexer of the Daemon now, without waiting for the next interval. The indexer cuts the local messages into fragments of conversation and keeps one embedding per fragment in `vectors.db`, next to the message database. It reads the local database, writes only `vectors.db`, never calls Telegram, and sends the text of fragments only to the local Ollama service (the address must be a loopback one). The command returns at once with the state; a run that is already in progress is reported unchanged. Check progress with `tg search index status`.

```text
tg search index start [flags]
```

<!-- schema:flags tg search index start -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon and `[semantic_search] enabled = true` in the configuration (restart the Daemon after the change). Without the setting the command fails with exit code 2 and the message `semantic search is off: set [semantic_search] enabled = true`. It needs no Telegram connection and works after the login is lost. Besides this command a run starts by itself one `interval` after the Daemon starts, then one `interval` after the end of each run, and when a history load or a background sync run ends. A fragment is embedded again when its text changed and it is closed (the last message is 30 minutes old) or has grown by 300 characters; fragments of closed chats are removed from `vectors.db`.

**Example.**

```sh
tg search index start
```

**Answer.**

```json
{"ok":true,"data":{"enabled":true,"state":"indexing","model":"bge-m3:latest","model_digest":"790764642607","indexed":0,"total":0,"embedded":0,"removed":0,"started_at":1795176000}}
```

**Answer fields** (`data`): the state of the indexer, see [`tg search index status`](#tg-search-index-status).

**Exit codes.** 0; 2 semantic search is off; 3 no Daemon; 1 the indexer could not start (the message says why, for example that `vectors.db` could not be opened).

**Verification.** See [FEATURES.md](../../FEATURES.md), item 13.1.

### tg search index status

Reports the state of the semantic indexer. It reads the state in memory and never asks Ollama, so it is cheap and works while the feature is off (it answers `disabled`).

```text
tg search index status [flags]
```

<!-- schema:flags tg search index status -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon. The states are `disabled` (the setting is off), `idle` (waiting for the next run; `reason` is `cancelled` after a cancelled run), `indexing`, `unavailable` (Ollama did not answer), `model_missing` (Ollama has no such model: run `ollama pull <model>`) and `error` (another failure, `reason` says which; also the state when `vectors.db` could not be opened). In the last three states the next run tries again. Search by words keeps working in every state.

**Example.**

```sh
tg search index status
```

**Answer.**

```json
{"ok":true,"data":{"enabled":true,"state":"idle","model":"bge-m3:latest","model_digest":"790764642607","indexed":6382,"total":6382,"embedded":0,"removed":0,"started_at":1795176000,"finished_at":1795176004,"next_run_at":1795176604}}
```

**Answer fields** (`data`): `enabled` boolean, `state` string, `reason` string (optional, never message text), `model` and `model_digest` strings (optional), `indexed` (fragments stored for the current model), `total` (fragments long enough for the index), `embedded` and `removed` (what the last run computed and dropped) integers, `started_at`, `finished_at` and `next_run_at` Unix seconds (optional; `next_run_at` is present while no run is in progress).

**Exit codes.** 0; 3 no Daemon.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 13.1.

### tg search index cancel

Stops the run in progress. The fragments already stored stay, and the next run continues from there. Without a run it changes nothing and reports the state.

```text
tg search index cancel [flags]
```

<!-- schema:flags tg search index cancel -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon and `[semantic_search] enabled = true`. The answer carries the state at the moment of the call; the run ends a moment later, so check `tg search index status` for `idle` with the reason `cancelled`.

**Example.**

```sh
tg search index cancel
```

**Answer.**

```json
{"ok":true,"data":{"enabled":true,"state":"indexing","model":"bge-m3:latest","model_digest":"790764642607","indexed":1200,"total":6382,"embedded":1200,"removed":0,"started_at":1795176000}}
```

**Answer fields** (`data`): the state of the indexer, see [`tg search index status`](#tg-search-index-status).

**Exit codes.** 0; 2 semantic search is off; 3 no Daemon.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 13.1.

## The tool describes itself

### tg version

Prints the version of the tool and the revision it was built from. It needs no Daemon and no network.

```text
tg version [flags]
```

<!-- schema:flags tg version -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** None.

**Example.**

```sh
tg version
tg version --human
```

**Answer.**

```json
{"ok":true,"data":{"version":"dev","revision":"15fe89f19de6b8e5d07a1fa26cb6c2e4f59ceb7b"}}
```

**Answer fields** (`data`): `version` string, `revision` string (optional).

**Exit codes.** 0.

**Version.** A build with `just build` reports `dev`; `revision` distinguishes it from the old npm `tg`.

### tg schema

Prints the machine description of every command, argument, flag and exit code. It is read only, needs no Daemon and no network, and prints the same document every time. It is the source of truth: do not copy the list of commands, take it from the schema.

```text
tg schema [command...] [flags]
```

<!-- schema:flags tg schema -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** None.

An optional command path restricts `commands` to that command or group. An unknown path returns a usage error. Without a path, the full schema is returned.

**What the document holds.**

| Field of `data` | Content |
| --- | --- |
| `schema_version` | the version of the schema format (1) |
| `tool` | `name`, `version`, `revision` |
| `output` | the formats (`json`, `jsonl`, `toon`, `human`) and the shape of the success and failure envelopes |
| `exit_codes` | `code`, `name`, `description` of every exit code |
| `global_flags` | `name`, `type`, `default`, `usage` of every global flag |
| `commands` | every command, see below |
| `subscription` | the live event subscription: the method `subscribe`, its result (`subscription_id`, `cursor`), the notification `event` and the event fields |

Every entry of `commands` has `path` (for example `message send-media`), `summary`, an optional `description`, `safety` (`read`, `local_write`, `account_write`), `dangerous`, `result` (the Go type of the answer), `formats`, `fields` (the response fields with `path`, `type`, `optional`, `untrusted`), `args` (`name`, `required`, `variadic`) and `flags` (`name`, `type`, `default`, `values`, `usage`). List commands carry `item`, the item fields of the list. `message search` also has `public_item` for the `--public` hits, `chat sync status` has `chat_result` for the one-chat answer, and `message watch` has `subscription_method`. The response fields come from the Go types of the results, so they follow the code, and a test fails when a command lacks a safety class, a `dangerous` mark or a result type. A field with `untrusted: true` carries text written by other people.

When a flag changes the result type, `result_variants` lists the flag, result type and fields. For example, `daemon start --foreground` returns `stopped` after the daemon stops.

**Example.**

```sh
tg schema
tg schema chat
tg schema --fields commands.path
tg schema --fields exit_codes
```

**Answer.** Shortened:

```json
{"ok":true,"data":{"commands":[{"path":"auth cancel"},{"path":"auth login"},{"path":"auth resend"},{"path":"auth status"}]}}
```

**Exit codes.** 0.

**Limits.** The schema describes the background `daemon start` result, not the `--foreground` result. See [FEATURES.md](../../FEATURES.md), item 3.8.
