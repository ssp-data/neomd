# Reading Emails

Emails are rendered as styled Markdown in the terminal using [glamour](https://github.com/charmbracelet/glamour). The reader supports vim-style navigation.

## Navigation

| Key | Action |
|-----|--------|
| `j` / `k` | scroll line by line |
| `space` / `d` | page down / up |
| `gg` | jump to top of email |
| `G` | jump to bottom of email |
| `h` / `q` / `esc` | back to inbox |

## Opening Emails Externally

| Key | Action |
|-----|--------|
| `e` | open in `$EDITOR` (read-only) — search, copy, vim motions |
| `o` | open in w3m (terminal browser, clickable links) |
| `O` | open in `$BROWSER` (GUI browser, images rendered) |
| `ctrl+o` | open newsletter web version in `$BROWSER` (from `List-Post` header) |

## Images

Remote images appear as `[Image: alt]` placeholders, keeping the reading experience clean and fast. To see images, press `O` to open in your browser.

**Inline / attached images** (e.g. screenshots pasted into an email) are listed in the reader header: `Attach:  [1] screenshot.png  [2] report.pdf`. Press `1`–`9` to download to `~/Downloads/` and open with `xdg-open`. Inline images also show `[Image: filename.png]` placeholders at their position in the body text.

## Links

Links in emails are automatically numbered inline where they appear in the body. A link like `Check out our blog` renders as `Check out our blog [1]` in the terminal.

Press `space` then a digit (`1`–`9`, `0` for 10th) to open the link in `$BROWSER`.

- Up to 10 links per email, deduplicated by URL
- Numbers appear inline so you can see them while reading without scrolling
- If an email has no links, `space` works as page-down as usual

## Attachments

Attachments are listed in the reader header:

```
Attach:  [1] report.pdf  [2] photo.png
```

Press `1`–`9` to download attachment N to `~/Downloads/` and open it with `xdg-open`. Filenames are deduplicated automatically if a file already exists.

## Replying, Forwarding, and Drafts

| Key | Action |
|-----|--------|
| `r` | reply to sender |
| `R` | reply-all (sender + all CC recipients) |
| `f` | forward email |
| `E` | continue draft (only in Drafts folder) — re-opens as editable compose |
