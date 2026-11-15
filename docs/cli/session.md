# Session transfer

A session string grants full access to the account. Keep it in an owner-only file and import only a string supplied by the owner. Run only one process per key, including across machines: the local duplicate check cannot see another machine.

### tg session export

Exports the current session as a `tgcli1` string. `--yes` and `--out` are required. A file is created with mode 0600 and an existing file is not overwritten. Use `--out -` only when the session may be exposed in standard output.

```sh
tg session export --out session.txt --yes
```

<!-- schema:flags tg session export -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--out` | string | `none` | output file, or - for stdout |
| `--yes` | bool | `false` | confirm the export |
<!-- /schema:flags -->

**Safety class:** `read`, dangerous. **Preconditions:** an authorized profile. **Answer:** profile, format, DC, key ID, and either path or session. **Exit codes:** 0 success, 2 invalid arguments or file, 4 no session.

### tg session import

Imports `tgcli1`, Telethon or gramjs from an owner-only file or stdin. Stop the profile daemon first. An existing profile requires `--force` and matching known account IDs. A replaced key remains active on Telegram until its device is terminated there.

```sh
tg session import --from session.txt --yes
```

<!-- schema:flags tg session import -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--force` | bool | `false` | replace the same account session |
| `--from` | string | `none` | input file, or - for stdin |
| `--yes` | bool | `false` | confirm the import |
<!-- /schema:flags -->

**Safety class:** `local_write`, dangerous. **Preconditions:** a stopped daemon. **Answer:** profile, format, DC, key ID, replacement status and duplicate profiles. **Exit codes:** 0 success, 2 invalid input or occupied profile.
