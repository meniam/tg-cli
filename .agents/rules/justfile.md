# Personal preferences: justfiles and help formatting

Apply across all projects when creating or updating justfiles and their modules.
Root and module help must share the same presentation. Apply this template to
new modules immediately, without waiting for a separate reminder.

**Defaults, not law.** A project's `AGENTS.md` and docs win over this file; a
project file with the same name replaces it entirely. See `AGENTS.md`,
"Precedence".

## Command interface

- Use the justfile as a clear, single entry point for working with a project.
- Write user-facing help in Russian unless the project sets another language.
  Keep it concise and concrete: explain purpose and outcomes, without internal
  implementation details or long technical explanations.
- Show complete, copyable commands, such as `just catalog deploy`, rather than
  a bare `deploy` or an ambiguous `catalog ...`.
- Running `just` or `just <module>` must display formatted help.
  Use a `help` recipe with the `[default]` attribute.
- Keep `just --list` as a technical command listing. Do not use its default
  output, with entries such as `default`, bare module names, and groups like
  `[dev]`, as the main help screen.
- Preserve existing command names and behavior when changing presentation.

## Structure of every help screen

1. A title banner with a rounded border: `╭─╮`, `│ │`, `╰─╯`.
2. One paragraph explaining what the tool is and what it is for. Line wrapping
   is allowed.
3. A capabilities section titled “Возможности”, explaining available actions.
4. Commands grouped into meaningful sections or services.
5. The order of operations, when needed, and significant consequences.
6. A command for viewing the full command listing, where useful.

Derive the order of operations and warnings from the current project's rules.
Do not carry deployment commands or constraints from one project into another.

## Visual presentation

- Overall left padding: **2 spaces**, including the banner and normal paragraphs.
- Commands and numbered steps: **4 spaces** from the terminal's left edge.
- An explanatory line below a command: **6 spaces** from the left edge.
- Use bold headings, cyan commands, and the default text color for descriptions.
- Align descriptions to the right of commands in a column using spaces.
- Separate sections with a blank line. Do not insert a blank line between a
  section heading and its commands or steps.
- Inside the banner, leave 2 spaces between the title and each side border.
- Avoid unnecessary colors and decorative elements.
- Disable ANSI escape codes when output is redirected or `NO_COLOR` is nonempty.

Example output follows. Russian text is intentional; commands are illustrative:

```text
  ╭─────────────────────────╮
  │  Каталог · базы данных  │
  ╰─────────────────────────╯

  Абзац о назначении сервиса.

  Возможности
  Краткое описание доступных действий.

  PostgreSQL
    just catalog pg-reload     Перезапустить
    just catalog pg-rebuild    Пересобрать без кэша и перезапустить

  Создание базы PostgreSQL
    just catalog pg-create-db <имя> <пароль>
      Создать роль и одноимённую базу; подставьте значения вместо <…>.
```

## Maintenance

- Reuse a shared formatting mechanism within each project so changes apply to
  every module. Do not introduce a separate visual style for each module.
- Choose file paths and help storage conventions according to the project.
  The indentation rules above describe rendered output, not necessarily the
  source files.
- Update the corresponding help whenever commands change.
- Check root and module help output, both with and without colors.
- Run `shellcheck` when changing a shell-based renderer.
- Checking help alone does not require deployment or starting services.

## Checklist

1. `just` and `just <module>` show formatted help through a `[default]` `help`
   recipe; `just --list` stays a technical listing.
2. Help is in Russian unless the project sets another language; concise, with
   complete copyable commands.
3. Each help screen has the parts of its structure in order: banner, purpose,
   "Возможности", grouped commands; order of operations and the full-listing
   command where needed.
4. Indents are 2, 4 and 6 spaces; descriptions are aligned in a column; the
   banner has 2 spaces inside each side border.
5. ANSI codes are off when output is redirected or `NO_COLOR` is nonempty.
6. Root and module help share one formatting mechanism, with no per-module style.
7. Order of operations and warnings come from this project's rules.
8. Help is updated with the commands; command names and behavior are unchanged
   by presentation changes.
9. Root and module help are checked with and without colors; `shellcheck` runs
   for a shell-based renderer.
