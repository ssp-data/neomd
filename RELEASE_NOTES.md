## What has changed?

neomd 0.10.5 is about not losing work and catching mistakes early. Closing the editor without changes now asks before throwing a message away, and an invalid `from` address is reported when neomd starts instead of when you press send. It also adds Drafts and Spam as optional tabs, a `[sig]` shortcut, a second light theme and keyring storage for the OAuth2 client secret.

**Highlights**

- **Closing the editor no longer discards the message silently** — quitting with `ZZ` without edits, `ZQ`, `:q!` or an emptied body used to drop the compose session straight back to the inbox. neomd now asks `Discard unsent message?`: `n` reopens the editor on the file exactly as you left it, `y` discards. Reply tracking, attachments and send-later state are kept until you decide. (reported in [#39](https://github.com/ssp-data/neomd/pull/39))
- **Drafts and Spam as tabs** — add `"drafts"` and/or `"spam"` to `[folders] tab_order` to show them as tabs at that position; `gd`, `gS` and `:go-spam` then jump to the tab. Note that `"drafts"` is no longer in the default tab order; use `gd` or list it explicitly. ([#37](https://github.com/ssp-data/neomd/pull/37), [#28](https://github.com/ssp-data/neomd/pull/28))
- **`[sig]` shortcut for the HTML signature** — a line containing only `[sig]` works like `[html-signature]`, which helps in replies where the signature placeholder has to be typed by hand. (related: [#27](https://github.com/ssp-data/neomd/pull/27))
- **New theme `catppuccin-latte`** — a second light palette next to `kanagawa-light`, using the official Catppuccin Latte colors. Markdown emails in the reader render for a light background with it. ([#36](https://github.com/ssp-data/neomd/pull/36) by @clementnuss)
- **OAuth2 client secret in the OS keyring** — `oauth2_client_secret = "keyring"` is resolved from the keyring the same way `password = "keyring"` is, from its own entry `account/<name>/oauth2_client_secret`. ([#17](https://github.com/ssp-data/neomd/pull/17) by @notthatjesus, reimplemented)

**Fixes**

- A Drafts tab showed the Inbox instead of the Drafts folder. It now shows Drafts. ([#37](https://github.com/ssp-data/neomd/pull/37), [#28](https://github.com/ssp-data/neomd/pull/28))
- An empty or malformed `from` passed config validation, left the pre-send screen with a blank `From:` line, and failed only at send. Every `[[accounts]]` and `[[senders]]` `from` is now checked at startup. Existing configs that omit `from` keep working: an empty account `from` falls back to `user` when that is a plain email address. ([#39](https://github.com/ssp-data/neomd/pull/39) by @farangkao, softened with the fallback)
- On macOS, large OAuth2 access tokens (common with Google) did not fit into the keyring and were silently stored in a plaintext token file instead. neomd now stores a refresh-only token in the keyring and fetches a new access token on first use. ([#17](https://github.com/ssp-data/neomd/pull/17))

**Pull requests**

Merged:
- [#36](https://github.com/ssp-data/neomd/pull/36) ui: add catppuccin-latte theme — @clementnuss

Closed, addressed in this release:
- [#37](https://github.com/ssp-data/neomd/pull/37) fix incorrect 'drafts' folder view — @clementnuss (bug report; fixed with the tab rework)
- [#28](https://github.com/ssp-data/neomd/pull/28) fix(ui): map Drafts tab correctly and reuse it for gd — @Brams-s (superseded by the tab rework)
- [#17](https://github.com/ssp-data/neomd/pull/17) Feature/support oauth2 secret in keyring — @notthatjesus (keyring secret and macOS token fix reimplemented; the `:set-oauth2-secret` command depended on the closed #5 and was left out)
- [#39](https://github.com/ssp-data/neomd/pull/39) config: validate from at load — @farangkao (landed with a `user` fallback so existing configs keep starting)

Still open:
- [#27](https://github.com/ssp-data/neomd/pull/27) fix: preserve HTML signature marker position — @Brams-s (positional signature; the `[sig]` alias in this release does not change where the signature is placed)

Keyring setup for passwords and OAuth2 secrets is described at https://neomd.ssp.sh/docs/configuration/.
