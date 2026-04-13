---
title: Screener Workflow
weight: 3
---

The screener classifies senders into four buckets using plain-text allowlists. Unknown senders land in `ToScreen` until you make a decision.

## How classification works

| List file           | Category                 | Where email lands |
| ------------------- | ------------------------ | ----------------- |
| `screened_in.txt`   | Approved                 | stays in Inbox    |
| `screened_out.txt`  | Blocked                  | ScreenedOut       |
| `feed.txt`          | Newsletter / feed        | Feed              |
| `papertrail.txt`    | Receipts / notifications | PaperTrail        |
| _(not in any list)_ | Unknown                  | ToScreen          |

## Auto-screen and background sync

By default neomd screens your inbox automatically so you never have to press `S`:

- **On every inbox load** — when you open neomd or switch to Inbox (or press `R`), the screener classifies all loaded emails in-memory and silently moves them. Your inbox is always clean.
- **Background sync** — while neomd is running, the inbox is re-fetched and re-screened every 5 minutes. New mail that arrived since you opened neomd is handled automatically.

Both behaviours are configurable in `[ui]`:

```toml
[ui]
auto_screen_on_load = true   # set false to disable auto-screen on inbox load
bg_sync_interval    = 5      # minutes between background syncs; 0 = disabled
```

`S` / `:screen` still works as a manual dry-run with `y/n` confirmation if you want to preview moves first.

## Day-to-day: screen new arrivals

Press `S` (or run `:screen`) to dry-run the screener against the emails currently loaded in your Inbox. A preview shows what would move where — press `y` to apply, `n` to cancel.

For individual senders, use `I` / `O` / `F` / `P` from any folder or the ToScreen queue.

## Bulk re-classification after updating your lists

When you add many senders to `feed.txt` or `papertrail.txt` at once (e.g. after importing from HEY), use this workflow:

```
1. Edit feed.txt / papertrail.txt / screened_in.txt with the new senders
2. Restart neomd  (lists are loaded at startup)
3. :reset-toscreen   →  shows "Move N emails from ToScreen → Inbox? y/n"
                         (moves everything back so it can be re-classified)
4. y to confirm
5. :screen-all       →  dry-run against ALL inbox emails (not just the loaded subset)
6. y to apply
```

`:screen-all` (alias `:sa`) scans every email in your Inbox — read and unread — and proposes moves for any sender that is now in a list. It does **not** touch emails already in Feed, PaperTrail, or other folders.

## Screening happens once

Emails are only auto-screened while they are in the **Inbox**. Once moved to ToScreen (or any other folder), they are not re-classified automatically. This keeps the logic simple and predictable.

If emails end up in ToScreen incorrectly (e.g. screened by another device like [Termux with Android](configurations/android) with incomplete lists), use `:reset-toscreen` to move them back to Inbox where auto-screen will re-classify them.

## Colon commands

Press `:` to open the command line. Tab cycles through completions; Enter runs the command.

| Command           | Alias  | Description                                           |
| ----------------- | ------ | ----------------------------------------------------- |
| `:screen`         | `:s`   | dry-run screen currently loaded Inbox emails          |
| `:screen-all`     | `:sa`  | dry-run screen **every** Inbox email (no count limit) |
| `:reset-toscreen` | `:rts` | move all ToScreen emails back to Inbox                |
| `:reload`         | `:r`   | reload the current folder                             |
| `:quit`           | `:q`   | quit neomd                                            |
