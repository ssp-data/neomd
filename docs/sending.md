# How Sending Works

neomd sends every email as `multipart/alternative`:

- **`text/plain`** — the raw Markdown you wrote (readable as-is in any client)
- **`text/html`** — rendered by [goldmark](https://github.com/yuin/goldmark) with a clean CSS wrapper

This means recipients using Gmail, Apple Mail, Outlook, etc. see properly formatted links, bold, headers, inline code, and code blocks — while you write nothing but Markdown.

When attachments are present the MIME structure is upgraded automatically:
- **Images** → `multipart/related` with `Content-ID` — displayed inline in the email body
- **Other files** (PDF, zip, …) → `multipart/mixed` — shown as downloadable attachments

## Multiple From Addresses

Add `[[senders]]` blocks to config to define extra identities that share an existing account's SMTP credentials:

```toml
[[senders]]
name    = "Work alias"
from    = "info@example.com"
account = "Personal"   # must match the name = field of an [[accounts]] block
```

In compose and pre-send, press `ctrl+f` to cycle through all configured accounts followed by all senders. The displayed `From:` field updates live. Sent copies always go to the active account's Sent folder regardless of which From is selected.

## CC, BCC, Reply-all, and Forward

In the compose form, `ctrl+b` toggles the Cc and Bcc fields (hidden by default). Bcc recipients receive the email but are never written to message headers. From the reader, `r` replies to the sender and `R` replies to the sender plus all Cc recipients (your own address excluded, `Reply-To` respected).

Press `f` to forward an email — works from both the reader and the inbox list (the body is fetched automatically). The editor opens with the original message quoted and `Fwd:` prepended to the subject. Fill in the `# [neomd: to: ]` field and add your own text above the quoted block.

## Attachments

Attachments are tightly integrated with both the pre-send screen and neovim.

**From the pre-send screen** — press `a` to open yazi (auto-detected; override with `$NEOMD_FILE_PICKER`). Press `D` to remove the last attachment.

**From within neovim** — press `<leader>a` in any `neomd-*.md` buffer to open yazi in a floating terminal. Selected files are inserted at the cursor as visible `[attach] /path/to/file` lines.

> **Requires** [custom.lua](https://github.com/sspaeti/dotfiles/blob/master/nvim/.config/nvim/lua/sspaeti/custom.lua) added to your neovim config, and [yazi](https://github.com/sxyazi/yazi) installed.

neomd strips `[attach]` lines before sending:
- **Image files** (`.png`, `.jpg`, `.gif`, `.webp`, `.svg`) → embedded inline in the HTML body; recipients see the image at that position
- **Other files** → appended as a regular MIME attachment

```markdown
Here is a screenshot:
[attach] /home/you/screenshots/overview.png

And a PDF for reference:
[attach] /home/you/docs/report.pdf
```

![neomd](../images/attachments-example.webp)

## Pre-send Review

After saving and closing the editor, neomd shows a review screen before sending — add or remove attachments, save to Drafts, or re-open the editor without sending accidentally.

![neomd](../images/presend-navigation.png)

| Key | Action |
|-----|--------|
| `enter` | send |
| `p` | preview in `$BROWSER` — renders through the same pipeline as sending, with inline images visible |
| `a` | attach file via yazi |
| `D` | remove last attachment |
| `d` | save to Drafts (IMAP APPEND with `\Draft` flag) |
| `e` | re-open editor |
| `esc` | cancel |

Press `p` to see exactly what the recipient will see — the email is rendered through the same goldmark Markdown-to-HTML pipeline used for sending. Local image paths from `[attach]` lines are converted to `file://` URLs so the browser displays them inline.

## Drafts

Press `d` in the pre-send screen to save to Drafts instead of sending. Navigate to Drafts with `gd`. To resume a saved draft, open it and press `E` — it re-opens in the editor with all fields pre-filled, and saving goes through the normal pre-send review.

For reading emails — images, links, attachments, and navigation — see [reading.md](reading.md).
