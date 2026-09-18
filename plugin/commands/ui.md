---
description: Open keeper's dashboard — connections, the approval queue, the query log
allowed-tools: Bash(keeper ui), Bash(keeper doctor), Bash(keeper daemon status)
---

Get the dashboard's address and hand it over:

!`keeper ui 2>&1 || keeper doctor 2>&1 | head -6`

If that printed a URL, give it to the user as a plain clickable link and stop.
They want the dashboard, not a description of it.

If it printed something else, read it and say which of these it is:

- **not reachable** — the daemon is not running. `keeper daemon start`.
- **vault locked** — it is running and nothing can be read yet.
  `keeper vault unlock`, which needs their terminal; it refuses a pipe.
- **command not found** — keeper is not installed. The `keeper-install` skill
  walks that, or the repository README has the download.

Do not try to reconstruct the URL. The loopback port is chosen at startup —
7773 when it is free and any free one when it is not — and this session has no
way to know which.
