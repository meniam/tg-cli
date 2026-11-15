#!/usr/bin/env -S uv run --quiet --script
# /// script
# requires-python = ">=3.14"
# dependencies = []
# ///
"""Project changelog: list, add a record, move past months into the archive.

Records live in .agents/changelog/: one file per record, frontmatter (date, type, breaking,
decision) and one line of text. The current month is in the folder root, past months in
YYYY-MM/ subfolders. Rules: .agents/rules/changelog.md. Types and the text are Russian.
"""
import argparse
import re
import shutil
import sys
from datetime import datetime, timedelta
from pathlib import Path
from zoneinfo import ZoneInfo

ROOT = Path(__file__).resolve().parent.parent / '.agents' / 'changelog'
DECISIONS = ROOT.parent / 'decisions'
TZ = ZoneInfo('Europe/Moscow')
LIMIT = 200
TYPES = ('Добавлено', 'Изменено', 'Устарело', 'Удалено', 'Исправлено', 'Безопасность')
MONTHS = ('января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа',
          'сентября', 'октября', 'ноября', 'декабря')
WEEKDAYS = ('Понедельник', 'Вторник', 'Среда', 'Четверг', 'Пятница', 'Суббота', 'Воскресенье')
TRANSLIT = dict(zip('абвгдеёжзийклмнопрстуфхцчшщъыьэюя', [
    'a', 'b', 'v', 'g', 'd', 'e', 'e', 'zh', 'z', 'i', 'y', 'k', 'l', 'm', 'n', 'o', 'p', 'r', 's', 't',
    'u', 'f', 'h', 'ts', 'ch', 'sh', 'sch', '', 'y', '', 'e', 'yu', 'ya']))


def fail(message):
    print(f'Error: {message}', file=sys.stderr)
    sys.exit(1)


def parse(path):
    text = path.read_text(encoding='utf-8')
    match = re.match(r'\A---\n(.*?)\n---\n(.*)\Z', text, re.S)
    if not match:
        raise ValueError('no frontmatter')
    meta = {}
    for line in match.group(1).splitlines():
        key, sep, value = line.partition(':')
        if sep:
            meta[key.strip()] = value.strip()
    body = next((ln.strip() for ln in match.group(2).splitlines() if ln.strip()), '')
    date = datetime.fromisoformat(meta['date'])
    if meta.get('type') not in TYPES or not body:
        raise ValueError('no type or text')
    return {
        'date': date,
        'type': meta['type'],
        'breaking': meta.get('breaking', '').lower() == 'true',
        'decision': meta.get('decision') or None,
        'body': body,
        'name': path.name,
        'path': path,
    }


def collect():
    files = [p for p in ROOT.glob('*.md') if p.name != 'AGENTS.md']
    files += [p for d in ROOT.glob('[0-9][0-9][0-9][0-9]-[0-9][0-9]') if d.is_dir() for p in d.glob('*.md')]
    records = []
    for path in files:
        try:
            records.append(parse(path))
        except (ValueError, KeyError) as error:
            print(f'Skipped {path.relative_to(ROOT)}: {error}', file=sys.stderr)
    records.sort(key=lambda r: (r['date'], r['name']), reverse=True)
    return records


def cmd_list(args):
    records = collect()
    if not args.all:
        first = datetime.now(TZ).date() - timedelta(days=max(args.days, 1) - 1)
        records = [r for r in records if r['date'].date() >= first]
    if args.type:
        records = [r for r in records if r['type'] == args.type]
    if args.breaking:
        records = [r for r in records if r['breaking']]
    if not records:
        print('No records. Widen the period: --days N or --all.')
        return
    day = None
    for r in records:
        current = r['date'].date()
        if current != day:
            if day is not None:
                print()
            day = current
            print(f'## {current.day} {MONTHS[current.month - 1]} {current.year}, {WEEKDAYS[current.weekday()]}')
        mark = ' **Breaking.**' if r['breaking'] else ''
        link = f' (decision: {r["decision"]})' if r['decision'] else ''
        print(f'- {r["type"]}:{mark} {r["body"]}{link}')


def make_slug(text):
    latin = ''.join(TRANSLIT.get(c, c) for c in text.lower())
    words = re.findall(r'[a-z0-9]+', latin)
    slug = ''
    for word in words:
        if len(slug) + len(word) + 1 > 40:
            break
        slug = f'{slug}_{word}' if slug else word
    return slug if re.match(r'^[a-z]', slug) else 'change'


def cmd_add(args):
    type_ = next((t for t in TYPES if t.lower() == args.type.lower()), None)
    if type_ is None:
        fail(f'type "{args.type}" is not one of: {", ".join(TYPES)}')
    text = args.text.strip()
    if not text or '\n' in text:
        fail('the text must be one non-empty line')
    if len(text) > LIMIT:
        fail(f'text is {len(text)} characters, the limit is {LIMIT}: details go to a decision or a plan, the record keeps the outcome')
    if re.match(rf'^({"|".join(TYPES)}):', text):
        fail('the type is a separate argument, the text must not start with it')
    if args.decision and not (DECISIONS / f'{args.decision}.md').exists():
        fail(f'no decision file .agents/decisions/{args.decision}.md')
    slug = args.slug or make_slug(text)
    if not re.match(r'^[a-z][a-z0-9_]{2,60}$', slug):
        fail('slug: latin snake_case, 3-61 characters')
    now = datetime.now(TZ)
    path = ROOT / f'{now.strftime("%Y%m%d_%H%M")}_{slug}.md'
    if path.exists():
        fail(f'file already exists: {path.name}; pass another --slug')
    lines = ['---', f'date: {now.isoformat(timespec="seconds")}', f'type: {type_}']
    if args.breaking:
        lines.append('breaking: true')
    if args.decision:
        lines.append(f'decision: {args.decision}')
    lines += ['---', text]
    ROOT.mkdir(parents=True, exist_ok=True)
    path.write_text('\n'.join(lines) + '\n', encoding='utf-8')
    print(f'Created: {path.relative_to(ROOT.parent.parent)}')


def cmd_archive(args):
    current = datetime.now(TZ).strftime('%Y-%m')
    moves = []
    for path in sorted(ROOT.glob('*.md')):
        if path.name == 'AGENTS.md':
            continue
        try:
            month = parse(path)['date'].strftime('%Y-%m')
        except (ValueError, KeyError) as error:
            print(f'Skipped {path.name}: {error}', file=sys.stderr)
            continue
        if month < current:
            moves.append((path, ROOT / month / path.name))
    if not moves:
        print(f'Nothing to move: the root holds only {current} records.')
        return
    for src, dst in moves:
        if dst.exists():
            fail(f'{dst.relative_to(ROOT)} already exists, move stopped')
    months = sorted({dst.parent.name for _, dst in moves})
    if args.dry_run:
        print(f'Would move {len(moves)} files to {", ".join(months)} (dry run, nothing changed).')
        return
    for src, dst in moves:
        dst.parent.mkdir(exist_ok=True)
        shutil.move(str(src), str(dst))
    print(f'Moved {len(moves)} files to {", ".join(months)}. Commit them separately.')


def main():
    parser = argparse.ArgumentParser(prog='just changelog', add_help=False)
    sub = parser.add_subparsers(dest='command', required=True)
    p = sub.add_parser('list', add_help=False)
    p.add_argument('--days', type=int, default=7)
    p.add_argument('--all', action='store_true')
    p.add_argument('--type', choices=TYPES)
    p.add_argument('--breaking', action='store_true')
    p.set_defaults(run=cmd_list)
    p = sub.add_parser('add', add_help=False)
    p.add_argument('type')
    p.add_argument('text')
    p.add_argument('--breaking', action='store_true')
    p.add_argument('--decision')
    p.add_argument('--slug')
    p.set_defaults(run=cmd_add)
    p = sub.add_parser('archive', add_help=False)
    p.add_argument('--dry-run', action='store_true')
    p.set_defaults(run=cmd_archive)
    args = parser.parse_args()
    args.run(args)


if __name__ == '__main__':
    main()
