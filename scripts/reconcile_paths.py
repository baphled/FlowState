"""Path and pattern grammar, work meter and pattern overlap for reconciliation scope checks (D0 r4 reference port).

A literal path is repository-relative POSIX: segments of `[A-Za-z0-9_.@+=,-]` (1 to 100 characters) joined by single `/`,
at most 512 characters in all. No empty segment, no dot-only segment, no `.git` segment in any case with optional trailing
dots, and no leading or trailing `/`, backslash, whitespace, control or non-ASCII character.

A pattern is the same plus `*` (zero or more characters inside one segment) and `**` as a whole segment (zero or more
segments). An `allowed` pattern (allowed_paths, external paths) must start with a literal segment; a `watched` pattern may
start with a wildcard. Every predicate returns False for a non-string and never raises, so a caller can treat an
ungrammatical input as a refusal. Validation is by anchored regular expressions with no nested quantifiers.

`overlaps` relates two patterns and `Work` meters it (and the matcher in `reconcile_glob`) in deterministic table cells,
so a pathological input raises `WorkExceeded` instead of running long; callers treat that as a refusal.
"""
import re

MAX_LEN, MAX_SEG = 512, 100
EXTERNAL_PREFIX = "/FlowState/"
_RESERVED = r"(?!\.+(?:/|$))(?!\.[Gg][Ii][Tt]\.*(?:/|$))"
_LITERAL_SEG = _RESERVED + r"[A-Za-z0-9_.@+=,-]{1,%d}" % MAX_SEG
_PATTERN_SEG = r"(?:\*\*(?=/|$)|" + _RESERVED + r"(?![^/]*\*\*)[A-Za-z0-9_.@+=,*-]{1,%d})" % MAX_SEG


def _compile(first, rest):
    return re.compile("%s(?:/%s)*" % (first, rest))


_LITERAL = _compile(_LITERAL_SEG, _LITERAL_SEG)
_PATTERN = _compile(_PATTERN_SEG, _PATTERN_SEG)
_ALLOWED = _compile(_LITERAL_SEG, _PATTERN_SEG)


def _valid(grammar, value):
    return isinstance(value, str) and len(value) <= MAX_LEN and grammar.fullmatch(value) is not None


def is_literal_path(path):
    """True iff `path` is a grammatical literal repository path."""
    return _valid(_LITERAL, path)


def is_pattern(pattern):
    """True iff `pattern` is a grammatical pattern; the first segment may be a wildcard (watched patterns)."""
    return _valid(_PATTERN, pattern)


def is_allowed_pattern(pattern):
    """True iff `pattern` is grammatical and its first segment is literal (allowed_paths): `*`, `*/**` and `**/x` fail."""
    return _valid(_ALLOWED, pattern)


def is_external_literal(path):
    """True iff `path` is `/FlowState/` followed by a grammatical literal path."""
    return isinstance(path, str) and path.startswith(EXTERNAL_PREFIX) and is_literal_path(path[len(EXTERNAL_PREFIX):])


def is_external_pattern(pattern):
    """True iff `pattern` is `/FlowState/` followed by an allowed pattern (literal first segment)."""
    return isinstance(pattern, str) and pattern.startswith(EXTERNAL_PREFIX) \
        and is_allowed_pattern(pattern[len(EXTERNAL_PREFIX):])


def has_wildcard(pattern):
    """True iff the entry contains a wildcard and so can match more than one path."""
    return "*" in pattern


WORK_BUDGET = 500000


class WorkExceeded(ValueError):
    """The work budget of one evaluation was exhausted; the caller fails closed."""


class Work:
    """Cost meter shared by one evaluation: `left` cells, plus a memo of single-segment glob results."""

    def __init__(self, limit=WORK_BUDGET):
        self.left, self.cache = limit, {}

    def spend(self, cells):
        """Charge `cells`; raise WorkExceeded once the budget is overdrawn."""
        self.left -= cells
        if self.left < 0:
            raise WorkExceeded("work budget exhausted")


def _table(rows, cols):
    """A `rows+1` by `cols+1` table with a True corner; False cells and a False border keep neighbour reads in range."""
    table = [[False] * (cols + 2) for _ in range(rows + 2)]
    table[rows][cols] = True
    return table


def _fill(rows, cols, cell):
    """Fill the table bottom-up, right-to-left; `cell(i, j, table)` computes every cell except the corner."""
    table = _table(rows, cols)
    for i in range(rows, -1, -1):
        for j in range(cols, -1, -1):
            if (i, j) != (rows, cols):
                table[i][j] = cell(i, j, table)
    return table[0][0]


def _segments_intersect(a, b):
    """Is there a string matched by both single-segment globs? Conservative: ignores the reserved segment names."""
    def cell(i, j, table):
        here = (a[i:i + 1], b[j:j + 1])
        if "*" in here:
            return table[i + 1][j] or table[i][j + 1]
        return here[0] == here[1] and table[i + 1][j + 1]
    return _fill(len(a), len(b), cell)


def _overlap_cell(first, second, i, j, table, intersect):
    """One cell of the segment table: may first[i:] and second[j:] both match one path?"""
    if i == len(first):
        return second[j] == "**" and table[i][j + 1]
    if j == len(second):
        return first[i] == "**" and table[i + 1][j]
    if "**" in (first[i], second[j]):
        return table[i + 1][j] or table[i][j + 1]
    return table[i + 1][j + 1] and intersect(i, j)


def overlaps(p, q, work=None):
    """True iff some literal path could match both patterns: a conservative over-approximation.

    Ungrammatical input is True (fail closed). Raises WorkExceeded when a supplied `work` meter runs out.
    """
    if not (is_pattern(p) and is_pattern(q)):
        return True
    work = work or Work()
    first, second = p.split("/"), q.split("/")
    work.spend(len(first) * len(second) + 1)

    def intersect(i, j):
        work.spend((len(first[i]) + 1) * (len(second[j]) + 1))
        return _segments_intersect(first[i], second[j])

    return _fill(len(first), len(second), lambda i, j, table: _overlap_cell(first, second, i, j, table, intersect))
