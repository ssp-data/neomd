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
| `notify.txt`        | Desktop notification     | (no move; only fires `notify-send` — see [Notifications](../notifications/)) |
| _(not in any list)_ | Unknown                  | ToScreen          |

### Domain entries

Any list line beginning with `@` (e.g. `@ssp.sh`) matches **every** address at that domain. Plain email addresses keep their exact-match behaviour and **always win over a domain rule** when both are present, so per-address overrides remain possible:

```
# screened_in.txt
@ssp.sh                 # everyone at ssp.sh is approved …
```

```
# screened_out.txt
spammy@ssp.sh           # … except this one address, which is blocked
```

Domain entries work in every screener list (`screened_in`, `screened_out`, `feed`, `papertrail`, `spam`, `notify`). The `Di` / `Do` chord (see below) writes them for you from inside neomd.


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

### Whole-domain shortcuts: `Di` / `Do`

When you want to approve or block **every** future address at a domain in one go, press the `D` chord:

| Keys | Effect                                                               |
| ---- | -------------------------------------------------------------------- |
| `Di` | Append `@<domain>` to `screened_in.txt`  (asks `y/n` first)          |
| `Do` | Append `@<domain>` to `screened_out.txt` (asks `y/n` first)          |

The chord works on the highlighted email in the inbox **and** on the open email in the reader. The domain is taken from the email's `From` header. Existing per-address entries (in any list) still take precedence over the domain rule, so a single blocked address inside an otherwise-approved domain stays blocked.

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

## Screener as phishing defense

The screener isn't just a productivity tool — it's a natural phishing filter. Since unknown senders always land in ToScreen, your Inbox only contains emails from senders you've explicitly approved.

This makes impersonation attempts easy to spot: if you've already screened in `info@sbb.ch` (Swiss train service), a phishing email from `info@sbb-tickets.fake.com` will land in ToScreen, not your Inbox. You'll immediately notice it's suspicious because the real sender is already approved. Without the screener, the fake would sit alongside legitimate emails with no visual distinction.

**Practical rule:** treat ToScreen as a quarantine. Verify the sender address before approving. Press `$` to mark spam.

## Screening happens once

Emails are only auto-screened while they are in the **Inbox**. Once moved to ToScreen (or any other folder), they are not re-classified automatically. This keeps the logic simple and predictable.

If emails end up in ToScreen incorrectly (e.g. screened by another device like [Termux with Android](configuration/android) with incomplete lists), use `:reset-toscreen` to move them back to Inbox where auto-screen will re-classify them.

## Priority Screener: Exact match over `@domain` match

When an incoming sender could match more than one list, neomd picks a category in two passes:

1. **First pass — exact email** is looked up in every list, in this priority order:
   `spam` → `screened_out` → `feed` → `papertrail` → `screened_in`.
   The first list that contains the *exact* address decides the category, and the search stops.
2. **Second pass — `@domain` rules** are consulted only if the first pass found no exact match, again in the same priority order. The first list whose `@domain` entry matches decides the category.
3. If neither pass matches → the sender lands in `ToScreen`.

This guarantees that a per-address rule **always overrides** the broader domain rule across *any* category. Two real examples:

| `screened_in.txt` | `screened_out.txt` | Sender                | Result      | Why                                                                             |
| ----------------- | ------------------ | --------------------- | ----------- | ------------------------------------------------------------------------------- |
| `neomd.demo@ssp.sh` | `@ssp.sh`        | `neomd.demo@ssp.sh`   | **Inbox**   | Exact match in `screened_in` (pass 1) wins over `@ssp.sh` block (pass 2)        |
| (empty)           | `@ssp.sh`          | `neomd.demo@ssp.sh`   | ScreenedOut | No exact match anywhere → `@ssp.sh` in `screened_out` matches in pass 2         |
| `@ssp.sh`         | `spammy@ssp.sh`    | `spammy@ssp.sh`       | ScreenedOut | Exact match in `screened_out` (pass 1) wins over `@ssp.sh` approval (pass 2)    |
| `@ssp.sh`         | (empty)            | `random@ssp.sh`       | **Inbox**   | No exact match → `@ssp.sh` in `screened_in` matches in pass 2                   |

**The mental model:** *"Add the domain to lift the whole company, then surgically block (or approve) individual addresses without restating the rule."*

The same two-pass logic runs for [`notify.txt`](/docs/notifications/#domain-entries) too — an exact entry there always wins over an `@domain` entry, but for the notify list this rarely matters since both produce the same outcome (a desktop notification).

## Colon commands

Press `:` to open the command line. Tab cycles through completions; Enter runs the command.

| Command           | Alias  | Description                                           |
| ----------------- | ------ | ----------------------------------------------------- |
| `:screen`         | `:s`   | dry-run screen currently loaded Inbox emails          |
| `:screen-all`     | `:sa`  | dry-run screen **every** Inbox email (no count limit) |
| `:reset-toscreen` | `:rts` | move all ToScreen emails back to Inbox                |
| `:reload`         | `:r`   | reload the current folder                             |
| `:quit`           | `:q`   | quit neomd                                            |
