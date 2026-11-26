"""Linear-time glob matcher over the `reconcile_paths` grammar (port of the D0 r4 reference, stdlib only).

No regular-expression backtracking: a segment is matched by an iterative two-pointer glob and a path by a bottom-up
table over segments. Each evaluation also spends the deterministic `Work` meter of `reconcile_paths` (table cells, no
wall clock) and raises `WorkExceeded` when it runs dry, which callers treat as a refusal. Ungrammatical input never
matches: it returns False.
"""
from scripts.reconcile_paths import EXTERNAL_PREFIX, Work, is_literal_path, is_pattern


def _glob(pattern, text):
    """Segment glob where `*` is any run of characters: two pointers, no recursion."""
    i = j = mark = 0
    star = -1
    while j < len(text):
        if i < len(pattern) and pattern[i] == "*":
            star, mark, i = i, j, i + 1
        elif i < len(pattern) and pattern[i] == text[j]:
            i, j = i + 1, j + 1
        elif star >= 0:
            mark += 1
            i, j = star + 1, mark
        else:
            return False
    return pattern[i:].strip("*") == ""


def _segment(work, pattern, text):
    key = (pattern, text)
    if key not in work.cache:
        work.spend(len(pattern) * len(text) if "*" in pattern else 1)
        work.cache[key] = _glob(pattern, text)
    return work.cache[key]


def _match_segments(patterns, texts, work):
    rows, cols = len(patterns), len(texts)
    work.spend(rows * cols + 1)
    nxt = [j == cols for j in range(cols + 1)]
    for i in range(rows - 1, -1, -1):
        cur = [False] * (cols + 1)
        if patterns[i] == "**":
            cur[cols] = nxt[cols]
            for j in range(cols - 1, -1, -1):
                cur[j] = nxt[j] or cur[j + 1]
        else:
            for j in range(cols):
                cur[j] = nxt[j + 1] and _segment(work, patterns[i], texts[j])
        nxt = cur
    return nxt[0]


def match(pattern, path, work=None):
    """True iff the grammatical `pattern` matches the grammatical literal `path`; anything else is False.

    `*` never crosses `/`; `**` is zero or more whole segments; matching is case-sensitive. Raises WorkExceeded when a
    supplied `work` meter runs out.
    """
    if not (is_pattern(pattern) and is_literal_path(path)):
        return False
    return _match_segments(pattern.split("/"), path.split("/"), work or Work())


def match_ci(pattern, path, work=None):
    """Case-folded `match` for protected globs (the grammar is ASCII, so lower() is exact)."""
    if not (isinstance(pattern, str) and isinstance(path, str)):
        return False
    return match(pattern.lower(), path.lower(), work)


def match_external(pattern, path):
    """`match` for two `/FlowState/` paths: both must carry the prefix, which is stripped before matching."""
    if not all(isinstance(v, str) and v.startswith(EXTERNAL_PREFIX) for v in (pattern, path)):
        return False
    return match(pattern[len(EXTERNAL_PREFIX):], path[len(EXTERNAL_PREFIX):])
