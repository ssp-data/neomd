---
title: FAQ
weight: 50
---

Questions that came up when people using neomd.



## Is it possible to create new directories/tabs

Currently, no. All folders are hard coded in a struct in a code as this is optimized for the GTD and HEY Screener workflow and keeps things simple.

But, please reach out to me and tell me which folders you need, maybe it's a folder that everyone might use, or otherwise, if I get enough request, I add a way to customize folders as I do with the sort order of folder tabs already.


### Advanced: Add custom folders yourself

You can fork neomd and modify the Go source code:

1. **Edit the code** (ask Claude to help with this):
    - Add a field to `FoldersConfig` struct in `internal/config/config.go`
    - Add entry to `keyToLabel` map
    - Optionally add keyboard shortcuts in `internal/ui/model.go`
 - Run `make build` to compile
2. **Create the IMAP folder** via webmail (e.g., "NewFolder")
3. **Configure it** in your `config.toml`:
 ```toml
 [folders]
   # ... existing folders ...
   new = "NewFolder"
   tab_order = ["inbox", "to_screen", "feed", "new", "sent", "archive"]
```

Once added this way, you can navigate to your custom folder with existing `[]HL` and `space+1-9`. If you added keyboard shortcuts in step 1, those will work too (e.g., gn / Mn).

## Does the signature appear only in new messages, not in replies?

Currently the signature is only automatically added if you create and compose a **new email**.

But you can add the signature in any email, e.g. if you reply with `[html-signature]` like this:

```markdown

# [neomd: to: email@domain.com]
# [neomd: from: Simon <my-email@ssp.sh>]
# [neomd: subject: Re: Subject Title]


Here's my reply.
BR Simon

[html-signature]

---

> **Previous sender <email@domain.com>** wrote:
>
> * * *
......

```

The html-signature is the placeholder for adding the HTML signature, but yes, it will always be added at the end of the email (e.g. in this case the reply).

## The Drafts and Spam folders seem to show wrong emails

Drafts and Spam are **off-tab folders** (not in the regular tab rotation) and behave slightly differently:

- **Access**: `gd` for Drafts, `gS` for Spam (or `:go-spam`)
- **Indication**: When viewing them, the folder name appears highlighted in the tab bar with a `│` separator
- **Reload**: Pressing `R` reloads the Drafts/Spam folder 
- **Leave**: Press `tab` or navigate to another folder (`gi`, `ga`, etc.) to return to regular tabs

**Old bug (fixed 2026-04-10)**: In older versions, pressing `R` while viewing Drafts could show Inbox content. If you experience this, rebuild neomd to get the fix.

## Why do Bengali / Arabic / Thai / emoji subjects show as `·` in the inbox?

Complex scripts (Bengali, Devanagari, Arabic, Hebrew, Thai, emoji, …) render at unpredictable widths in modern terminals. The terminal emulator (foot, kitty, alacritty, …) and the application disagree on how many cells a grapheme cluster takes — this is a well-known problem documented in [lipgloss #562](https://github.com/charmbracelet/lipgloss/issues/562) and [Mitchell Hashimoto's "Grapheme Clusters and Terminal Emulators"](https://mitchellh.com/writing/grapheme-clusters-in-terminals). The real fix is the [OSC 66 text-sizing protocol](https://sw.kovidgoyal.net/kitty/text-sizing-protocol/), but it's only implemented in kitty and foot, and tmux strips it.

When a subject contains such characters, the inbox row would overflow the terminal width, the bubbles list would wrap the row, and the visible top of the list would scroll off. To keep the layout solid, neomd sanitises the subject for display: every run of unpredictable-width characters collapses into a single `·` placeholder. Latin scripts (including German `ä` `ö` `ü` `ß`, accented vowels, Latin Extended), Greek, Cyrillic, common punctuation, and **CJK (Korean / Japanese / Chinese)** all pass through verbatim — CJK characters are uniformly East Asian Wide (2 cells per code point) and both terminals and `runewidth` agree on their width, so they don't have the cluster-ambiguity problem the other scripts do. European and CJK subjects look normal in the inbox list; only Bengali / Devanagari / Arabic / Hebrew / Thai / emoji collapse to `·`.

The transform only affects the **displayed** subject in the inbox list and the reader's `Subject:` line. The email body (including its Bengali / Arabic / Thai content) renders unchanged through glamour, and the underlying `Subject` field is preserved on the email object — searches, filters, replies, and forwards still see the original text.

If you regularly read subjects in one of the still-collapsed scripts, run neomd in a kitty terminal **without** tmux and the placeholder behaviour will eventually move behind a config flag once OSC 66 gets broader adoption.

## Why some of he tabs have unread number count and others don't?

This is on purpose, it's made to be **less distractive**. For example, I don't need to know the number of unread of my Feed, as it's just newsletter, and when I have time to read, I will, but I ddon't want to be stressed out to read 99 unread newsletters.

But Inbox e.g. is important, it's the folder for doing things next (GTD), and therefore, I want to know, also if there's a new email came in, I want to know.

Only Inbox, Papertrail, Waiting and Scheduled have unread counter. Why not Someday folder? Again, as these are more or less, nice to have emails, I don't want to create presure if we add them for later in someday by showing it all the time.
