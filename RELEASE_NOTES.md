## What has changed?

neomd 0.10.6 is a small release with one change: the HTML signature now goes where you put `[sig]` or `[html-signature]` in the message. In a top-posted reply it appears right under your text instead of at the very bottom, below the quoted history.

**Highlights**

- **The HTML signature is placed at the marker** — the first `[sig]` or `[html-signature]` line in the body is replaced by the signature at that position. Previously the marker only switched the signature on, and it was always appended at the end of the message. Sending, send-later and the `O` browser preview all place it the same way. (idea from [#27](https://github.com/ssp-data/neomd/pull/27) by @Brams-s, reimplemented as a smaller change)
- **Existing behavior is kept where the marker is at the end** — new messages already have the marker at the bottom, so they look as before. Bodies without a marker, out-of-office replies, drafts and Listmonk newsletters are unaffected. A marker inside a fenced code block is not used, and the signature then goes at the end as before. The plain-text part of the mail is unchanged.

**Fixes**

- In top-posted replies, the HTML signature ended up below the entire quoted thread, where Gmail and Outlook fold it behind "…" or it reads as part of the other person's mail. It now sits where the marker is, directly under your reply.
