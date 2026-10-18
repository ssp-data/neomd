## What has changed?

neomd 0.10.2 adds email tags. Tags are stored as IMAP keywords on the message itself, so they survive reinstalls and show up in any other client that reads keywords. Press `<space>t` to tag the cursor email or the marked set, see the tags as colour chips in front of the subject, and filter the list down to the tags you care about. Two reader fixes also make deeply quoted threads and emails with embedded images open in a fraction of a second.

**Highlights**

- **Tag emails with `<space>t`** — the picker opens on the cursor email or the marked set, `enter` or `space` toggles a tag, `a` creates a new one and `/` narrows the list. A tick means every target carries the tag, a tilde means only some do. Toggles apply at once and the server update runs behind the list; if it fails, the chip reverts and the status line says why.
- **Tags appear as chips before the subject** — each tag renders as a small coloured label, with chip width taken out of the subject budget so rows never overflow. Only tags you created render; reserved system keywords such as `$label1` are hidden and cannot be added.
- **Filter the list by tag** — in the picker, `f` toggles the highlighted tag into a list filter and `F` clears it. The filter works like `z` unread-only: it follows you across folder tabs, reload and search until `F` or `esc` in the inbox turns it off, and the footer names the active tags meanwhile. If the filter hides every row, `<space>t` still opens so you can change it.
- **Configure look and feel in `[tags]`** — tags are on by default and a fresh install shows nothing until you create one; `enabled = false` switches the feature off. Per-tag `[tags.<keyword>]` sections set `display`, `fg`, `bg` or hide a single tag's chips. `nerd_pill = true` draws pill-shaped chips and needs a nerd font, so it stays off by default. `mono = true` renders every chip in the theme's muted colour for a quiet glyph-only row. `bg = "transparent"` lets a transparent terminal show through.
- **Check your provider with `:tag-test`** — the probe dialog reports whether the account's IMAP server accepts, searches and persists keywords. Infomaniak and the test servers have full support; accounts without IMAP keep tags locally. Every tag change is also written to a per-account Message-ID list under the config directory as a restore fallback.

**Fixes**

- Long reply chains with dozens of nested quote levels took over two seconds to open and now open in well under half a second. The reader caps the displayed quote depth at three bars; replies, forwards, the editor view and the browser view still use the full body.
- Emails that embed their images as base64 data URIs could hang the reader with the CPU pinned. The reader now shows a short image placeholder for such links and renders the body instantly. The `O` browser view still shows the embedded pictures.

How tags work, how they compare to Thunderbird's, and every config option are documented at https://neomd.ssp.sh/docs/configuration/tags/.
