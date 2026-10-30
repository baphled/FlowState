# Pull request scope

Each PR delivers one bounded behaviour with its tests and necessary documentation.
Refactoring, package migrations and unrelated fixes use separate prerequisite PRs.
Split vertically along contracts; every intermediate PR must remain tested and usable.

## Hard review budget

- At most 500 added plus deleted text lines and 10 changed paths.
- Tests, documentation, dependency files, generated files and deletions all count.
- Moves count as deletion plus addition; changed text/binary bytes share a 128 KiB cap.
- A changed blob larger than 1 MiB fails closed. No label, title, environment flag,
  generated marker or inline waiver raises these limits.

Before pushing, from the checkout with committed work and a freshly fetched target:

```sh
make check-pr-size BASE_SHA="$(git rev-parse origin/next)"
```

The target must be an ancestor. Refresh stale branches before checking. Budget the
whole diff before delegation; checkpoint and split before exceeding the cap. Keep
each agent on one item. Size is necessary, but separate review still checks cohesion.

CI uses the base revision's checker once installed, so a candidate cannot enlarge
its own allowance. Required Build depends on the size gate and fails if it fails,
skips or cancels. Expensive CI waits for this early gate. Initial installation runs
the candidate checker. Policy changes
need their own bounded PR; oversized implementation PRs are not exempted.
