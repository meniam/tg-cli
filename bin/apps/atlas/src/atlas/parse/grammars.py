"""Грамматики tree-sitter для поддерживаемых языков."""

from functools import cache

import tree_sitter
import tree_sitter_go
import tree_sitter_javascript
import tree_sitter_php
import tree_sitter_python
import tree_sitter_typescript

from atlas.model import Language


@cache
def parser_for(language: Language) -> tree_sitter.Parser:
    """Парсер языка; создаётся один раз на процесс, в том числе на процесс-воркер."""
    match language:
        case Language.PHP:
            grammar = tree_sitter_php.language_php()
        case Language.TYPESCRIPT:
            grammar = tree_sitter_typescript.language_typescript()
        case Language.TSX:
            grammar = tree_sitter_typescript.language_tsx()
        case Language.JAVASCRIPT:
            grammar = tree_sitter_javascript.language()
        case Language.PYTHON:
            grammar = tree_sitter_python.language()
        case Language.GO:
            grammar = tree_sitter_go.language()
        case Language.MARKDOWN:
            raise ValueError("Markdown is parsed without tree-sitter")
    return tree_sitter.Parser(tree_sitter.Language(grammar))
