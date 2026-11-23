---
name: scout
description: "Navigation through tg-cli with Atlas (`bin/atlas`): a Go code graph plus an index of documents, rules and decisions. Use before scouting, planning and implementing, when you need to find where something is done, how a mechanism works, who calls a function, what an edit will touch, which tests to run and whether there is a precedent. Also for \"where is it\", \"how does it work\", \"where is it called from\", \"what will break\", \"which tests are affected\" and \"is there a document about\", even if Atlas is not named."
---

# scout: the project map

Atlas is an aid, not a source of truth. After a search always open the found file and check the
current code.

## Documents come first

Any mechanism is studied starting from its document, not from its code. A document says **why** the
mechanism exists, what feeds it and where its borders are. Code says **what** it does right now.
Whoever reads only code sees the conditions but not the intent, and takes a private detail for the rule.

Where to look for documents:

- `bin/atlas query "<topic>" --scope documentation`: a targeted search over `docs/`, `README.md`,
  `FEATURES.md`;
- `docs/cli/` (every command, flag and exit code) and `docs/contracts.md` (behavior contracts: journal,
  throttle, history load, export, access lists, events, writes, media);
- `--scope agent`: `AGENTS.md`, rules from `.agents/rules` and skills from `.agents/skills`;
- `--scope decision`: why a choice was made, with its alternatives (`.agents/decisions`).

Two rules follow:

- the answer of a scouting names the documents on the topic separately, next to the code links; if
  there is no document, it says so;
- a mismatch between a document and the code is a finding, not a trifle: the truth is in the code, the
  document needs a fix.

`FEATURES.md`, decisions and `docs/skill/` are written in Russian, code and the other
documents in English. Search in both languages: a Russian question finds Russian documents, English
names find code. `meta.missing_words` of `query` shows which word is nowhere.

## Order of work

1. **Document.** `query --scope documentation`, then read what it found.
2. **Overview.** `explore "<question>"`: entry points, the call chain, the source, documents, the
   places where the target is chosen at run time and the blast radius, in one call.
3. **Precisely.** `node`, `incoming`, `outgoing` by the exact name from step 2.
4. **Reading.** The file from disk at `path:start-end` from the answer.
5. **Exact search.** `rg` for a known string and for what is outside the map: JSON field names,
   flag text, `testdata/`, `.agents/plans`. Code of a given shape is found with `ast-grep`, not a regexp:

   ```bash
   ast-grep run -l go -p 'fmt.Errorf($$$)' internal/
   ast-grep run -l go -p 'func ($R *daemon) $M($$$) $$$' internal/daemon --json=compact
   ```

   `$X` is one node, `$$$` is any number of arguments or expressions. It runs on the host (Homebrew).

Traps of text search:

- `rg -r` is `--replace`, not recursion: `rg` searches recursively anyway.
- `rg` and `grep` in the agent shell respect `.gitignore`. A search from the root silently skips
  `dist/` and `var/`, so "not found" proves nothing there. Go into them by an explicit path or with
  `--no-ignore`.

Skipping steps is costly: a conclusion from one query without a document builds a decision on a wrong
guess.

## What to ask with

Choose by the question, not by habit:

| Question | Answer with |
| --- | --- |
| Why a mechanism exists, what feeds it, where its borders are | a document: `query "<topic>" --scope documentation` |
| How X works as a whole | `explore "<question>"` |
| Where a symbol lies, what is near it by topic, a precedent | `query "<words>"` |
| Card of a type, function, method or file: links both ways | `node <symbol>` (`--code` adds the source) |
| Who calls a function, several levels up | `incoming <Type.Method> --depth 3` |
| What a function calls, down | `outgoing <Type.Method> --depth 2` |
| Which tests to run after an edit | `git diff HEAD --name-only --relative \| bin/atlas affected - -o path` |
| A list of files on a topic, like `rg -l` | `query "<words>" -o path` |
| Where a string or value occurs | `rg -n '<string>'` |
| Where in the code are places of **such a shape** | `ast-grep run -l go -p '<pattern>' internal/` |
| What is in a file now | reading the file, the only current source |

## Commands

Run from the project root through `bin/atlas`. The answer is one JSON on stdout; `-o pretty` is for
human reading, `-o path` gives only paths (`query` and `affected` only: `explore` and `node` exit with
code 1; shrink `explore` with `--limit` and `--max-chars`, `node` by leaving out `--code`). `just atlas <command>` is the same command with `-o pretty` by default.

```bash
bin/atlas explore "daemon start background" --limit 6
bin/atlas explore "daemon.handle Limiter.Acquire"                # the chain between two, if the graph has a path
bin/atlas query "access lists hide chats" --limit 20
bin/atlas query "throttle" --scope documentation
bin/atlas query "background history sync" --scope decision        # add the scope before editing or choosing
bin/atlas node internal/daemon/daemon.go
bin/atlas node 'daemon.handle' --code
bin/atlas incoming Limiter.Acquire --depth 3
bin/atlas status scope kind                                       # values of --scope and --kind
```

`explore` looks for entry points in the code, so it needs words that are in the names: type,
function or method names, or English terms. A question without such words gives `No symbol found` and
code 3. Then run `query` first and pass the found names to `explore`.

A symbol is given by its full name, `Type.Method`, a short name, a path or `path:line`. In Go a
qualified name has no package prefix: `daemon.handle`, `Limiter.Acquire`, `Run`. A short name with
several definitions gives `ambiguous: true` and a list of candidates (or code 4): refine it with
`Type.Method` or a path.

## Read the meta of the answer

Atlas says itself how far its answer can be trusted. Look at it before using the output.

**`query`:**

- `meta.missing_words`: words that are nowhere in the project: check the spelling, take a synonym or an
  English term. The output for the other words may not answer the question at all.
- `meta.variants`: how a word was widened (stem, glossary, synonyms from `.atlas.json`).
- `missed` and `coverage` of an entry: which words are not in it. An entry with `coverage` 0.4 was picked
  by a part of the query.
- `meta.total`: 3 and 30000 matches mean different confidence in the first ten.

**`explore`:**

- `seeds`: where the answer started. Check that the right symbol was chosen, otherwise ask again with
  the exact name.
- `missing_names`: names from the question that are not in the code. JSON fields and flags land here
  legitimately: they live in `docs/cli/` and in `tg schema`, not in the graph.
- `ambiguous_names`: a name is defined more than five times; narrow it to `Type.Method`.
- `boundaries`: where the static path breaks and the target is chosen at run time (`interface`,
  dynamic calls). The graph does not see further; look for the target by `key` and `candidates` or by
  reading.
- `impact`: what depends on the found code two levels up; `symbols: 0` means the graph found no dependents; check `rg` before calling the code free to edit.

**Links in `node`, `incoming`, `outgoing`:** the `guess` field means the link was guessed by a
heuristic: check the line `at` by reading.

## Limits of the map

- **Interfaces.** A call through an interface goes to the method declaration. Go has no `implements`
  keyword: Atlas derives it from method names (without signatures), only for interfaces of two or more
  methods whose method set is fully known, and not for types from `_test.go`; such an edge is a
  guess. The implementations of an interface are in `node <Interface>`. In this project `cli` talks to
  `daemon` over JSON-RPC and `main` injects the Telegram client, so the chain often ends at the
  interface or at the socket: read the other side by its name.
- **Calls on a value of unknown type** (`build().Run()`) go by name: a single method with that name in
  the codebase, marked `guess`. A conversion `T(x)` is a `references` edge, not a call.
- **Same name in several files of one package** is a build variant (`_linux.go`, `//go:build`): the
  reference is `ambiguous`, build tags are not evaluated.
- **`incoming` sees calls, not mentions.** For a type it is often empty: types are used in signatures
  and fields. All incoming kinds of links (`references`, `instantiates`) are in `node`; JSON strings
  and flags are found by `rg`.
- **An empty answer of the graph means "the graph does not know", not "no links".** Most references
  of a Go file lead to the standard library and `gotd`, outside the project. Go on with `query` and `rg`.
  The exact state is `bin/atlas status graph ref`.
- **Scopes.** By default `query` searches `source`, `test`, `documentation`, `agent`. Decisions
  (`--scope decision`), learnings (`--scope learning`) and the changelog (`--scope changelog`) are
  connected explicitly. A journal entry is context, not proof: the truth is in the code.
- **Outside the map:** `.agents/plans`, `dist/`, `var/`, `testdata/`, secrets.

## Freshness and failures

No separate build or warm-up is needed: every command syncs the index with the disk itself. The first
call after a long pause takes several seconds, progress goes to stderr.

- `-n` answers without syncing. Fits a series of questions in a row when files did not change.
- `bin/atlas sync --full` (`just atlas index`) is maintenance, not scouting: it rebuilds from scratch if an answer looks broken or the keys
  of `.atlas.json` changed except `paths` and `root`: the index notices a change of those two itself.
- Exit codes: `0` answer, `1` error, `2` no config or index, `3` symbol not found, `4` name is
  ambiguous.

The index lives in `var/cache/atlas` and is not in Git. `bin/apps/atlas` is a build from the Atlas
repository: it is not edited, an Atlas bug or a missing feature goes to the user.

## Answer format

Whoever scouts searches itself and delegates no further. A report to the one who asked has a fixed
shape; drop empty sections, except "Documents":

```markdown
## Conclusion
2-5 sentences: the direct answer to the question.

## Documents
- `docs/...md`: what it says on the topic. Or: "No document on the topic".

## Code
- `internal/daemon/daemon.go:106-415`: `Run`, its role in the mechanism.

## Links and borders
The call chain, the points where the target is chosen at run time (interface, injected client),
the blast radius.

## Tests
Which tests cover the place, or what `affected` returned for the edit.

## Mismatches
A document says X, the code does Y (`path:line`). The code is the truth, the document needs a fix.

## Not checked
What the graph does not know, which links are guessed (`guess`), what could not be read.
```

Links only as `path:line` or `path:start-end` from the project root. No JSON dumps and no long code
quotes. Read big files by line range, not whole.

## Rules of the search

- The search changes nothing: no files, no Git, no manual index edits. `bin/atlas` syncs the index
  itself, that is allowed; do not run `sync --full` while scouting.
- Do not read secrets (`.env`, `.env.*`, `*.session`, `access.json`, `api_hash`, keys, tokens) and do
  not reach Telegram or any external service.
- If `bin/atlas` exits with code `1` or `2`, stop and give whoever asked the command, the exit code, the `error` field of the JSON on stdout and a non-empty stderr. Falling
  back to `rg` silently is forbidden: it loses the documents and links the map would give. Codes `3`
  and `4` are normal: refine the name and go on.
- Every claim rests on code you read, not only on the graph: the graph shows structure and links, the
  state of a file is read from disk.
- Stop when the question is closed. Mention what you noticed on the way in one line, do not widen the
  search.
