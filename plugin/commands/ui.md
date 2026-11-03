---
description: Open keeper's dashboard — connections, the approval Inbox, the query log
allowed-tools: Bash(keeper ui), Bash(keeper doctor), Bash(keeper daemon status), Bash(~/.local/bin/keeper ui)
---

Get the dashboard's address and hand it over:

!`PATH="$PATH:$HOME/.local/bin"; keeper ui 2>&1 || keeper doctor 2>&1 | head -6`

If that printed a URL, give it to the user as a plain clickable link and stop.
They want the dashboard, not a description of it.

If it printed something else, read it and say which of these it is:

- **not reachable** — the daemon is not running. Run `keeper daemon start`.
- **command not found** — keeper is not installed, or `~/.local/bin` is not on
  PATH. The `keeper-install` skill walks both.

Do not try to reconstruct the URL. The loopback port is chosen at startup, and
this session has no way to know which.
