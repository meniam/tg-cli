"""Запуск через ``python -m atlas``."""

from atlas.cli import main

# WHY: воркеры разбора на macOS стартуют через spawn и заново импортируют этот файл;
# без проверки каждый воркер запустил бы команду целиком.
if __name__ == "__main__":
    raise SystemExit(main())
