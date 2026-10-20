---
title: Tags & Notes Settings
weight: 5
---

Configuration for [tags and private notes](/docs/tags-and-notes/) — what the features do, with screenshots, is on that page. This page is the `[tags]`, `[tags.<name>]` and `[notes]` reference.

## `[tags]`

Server-side tagging, on by default:

```toml
[tags]
enabled = true     # false: global kill-switch — no picker, no chips
key = "t"          # the <space> chord that opens the picker
nerd_pill = false  # pill-shaped chips with half-disc end caps — needs a nerd font
mono = false       # every chip in the theme's muted colour, no background (ignores all fg/bg)
# fg = "#1F1F28"   # default chip colours for tags without their own section
# bg = "#DCD7BA"   # hex, ANSI number, or "none"/"transparent" (bg only)
```

- **`enabled = false` is the global kill-switch**: neither the picker nor any chip renders. Per-tag `enabled = false` (below) hides one tag's chips while the feature is on and can never override the global switch.
- **`[tags.<name>]` sections only change how an existing tag looks.** They never create a tag: until a message carries the keyword, the section does nothing. Tags are created in the picker (`<space>t`, `a`).
- **Keywords are lowercase**, letters, digits, `-`, `_`, `.` (max 32 chars). Keywords starting with `$` or `\` belong to other systems (Thunderbird's `$label1`–`$label5`, `$HasAttachment`) and are never offered or shown.
- **A local backup exists**, per account, at `~/.config/neomd/tags/<account-address>/<keyword>.txt` (one Message-ID per line), written on every toggle — in case a provider ever purges unknown keywords. The server is the source of truth.
- **Provider support varies** — run `:tt` (`:tag-test`) on an email to probe your server: it stores a test keyword, verifies it via FETCH and SEARCH, and removes it again, with a verdict per step.

{{< callout type="info" >}}
Verified with full support on Infomaniak and Hostpoint (`PERMANENTFLAGS \*`, STORE + SEARCH, persistence across sessions; keywords are invisible but preserved in their webmail).
{{< /callout >}}

## How they Look: Example

Each look below matches a screenshot on the [Tags & Private Notes](/docs/tags-and-notes/#how-tags-can-look) page. The tag names (`important`, `test`, `help`) are only examples.

**Standard view** — the default: plain reverse-video chips before the subject, no special font needed:

```toml
[tags]
enabled = true
```

**Nerd-font pills** — half-sphere end caps around each chip. **Off by default** because it **needs a nerd font** in the terminal: without one the caps render as placeholder boxes.

```toml
[tags]
nerd_pill = true
```

**Per-tag display text and colours** — one `[tags.<keyword>]` section per tag you want to customize. The stored IMAP keyword is always lowercase and never changes; only how it displays does:

```toml
[tags.important]
enabled = true
display = "Important"
fg = "#FF9900"
bg = "#660000"
```

**Nerd-font glyphs in the display text** — put a glyph into `display`, either pasted literally or as a TOML `\uXXXX` escape (the escape survives copy-paste, which strips private-use glyphs in many places; find code points at nerdfonts.com/cheat-sheet). Omarchy ships nerd fonts by default:

```toml
[tags.test]
enabled = true
display = "\uF0A3 Testing"
fg = "#00FF00"
bg = "#006600"
```

**Compact glyph-only** — glyph-only `display` with `bg = "none"`; on a transparent terminal the background shows through. Or set `mono = true` once: every chip then renders in the active theme's muted colour with no background (per-tag `fg`/`bg` are ignored, `display` and `enabled` still apply), so tags read as quiet markers instead of a rainbow:

```toml
[tags]
enabled = true
nerd_pill = false

[tags.important]
enabled = true
display = "\uF071"   # nf-fa-warning
fg = "#FF9944"
bg = "none"

[tags.test]
enabled = true
display = "\uF0A3"   # nf-fa-certificate
fg = "#00FF00"
bg = "none"

[tags.help]
enabled = true
display = "\uF059"   # nf-fa-question_circle
fg = "#777777"
bg = "none"
```

## `[notes]`

Private per-email notes, on by default:

```toml
[notes]
enabled = true          # false turns the feature off: no popup, no ✎ snippet, no Note line
folder = "neomd/Notes"  # IMAP folder for the note messages (created on first use)
```

- **Where notes live.** Each note is a tiny message of its own in `folder` whose `X-Neomd-Note-For` header names the email's Message-ID. **The email itself is never touched**: no re-upload, no new UID, no flag change. Editing a note uploads a fresh copy and expunges the old one; only the note's own UID changes. This works on every IMAP server — the ANNOTATE extension (RFC 5257) that was designed for per-message comments is only implemented by Cyrus, so neither Gmail nor a Dovecot host like Hostpoint has it, and METADATA (RFC 5464) is per-mailbox only.
- **The folder is created on your first note, never before.** `/` in the name is replaced by the server's hierarchy delimiter (`neomd.Notes` on a Dovecot with `.` separators). Other clients see it as an ordinary mailbox with one short message per note (subject `Note: <email subject>`); unsubscribe it there if it gets in the way.
- **A local copy** sits at `~/.config/neomd/notes/<account-address>/<message-id>.md` (a small header block, then the Markdown body), written through on every save: the snippet shows before the first server fetch, and nothing is lost if the folder ever disappears. The server copy is fetched in the background at start, on account switch and on every background sync — the server wins per Message-ID, notes written offline are kept and uploaded on their next edit.

{{< callout type="info" >}}
Verified on Hostpoint (Dovecot) and the in-memory test server: save, read back, edit (replaces the old note), delete — with the noted email's UID and flags unchanged throughout.
{{< /callout >}}
