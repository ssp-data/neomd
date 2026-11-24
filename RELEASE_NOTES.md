## What has changed?

neomd 0.10.4 is about replies going to the right people from the right address. Replying to your own mail outside the Sent tab, to mail that reached you through an alias or catch-all address, or to a mailing list no longer puts you in Cc or picks a stale From. The sender view also moves from `V` to `ctrl+v`.

**Highlights**

- **Addresses that are you without being a sender: `alternative_emails`** — list addresses such as `inquiries@example.com` or `*@example.com` at the root of the config (replies go out from `default_from`) or inside an `[[accounts]]` block (replies go out from that account). Reply-all strips them from Cc, and mail from them counts as yours in threads. This is distinct from `[[senders]]`, which are addresses you send from.
- **From is chosen per message, not per tab** — whether a mail counts as one you sent is now decided from the message itself: it sits in the Sent folder, or its From is one of your addresses. So replying to your own mail from a conversation view, an Inbox thread or Archive addresses the original recipient, not you. Reactions with `ctrl+e` use the same From picker.
- **Sensible From fallback** — when no account, sender or alternative email received the mail (mailing lists, unknown forwarders), the reply goes out from `default_from` instead of whatever the previous compose used. To outranks Cc when both match: addressed at Work and copied at Personal replies from Work.
- **Sender view is now `ctrl+v`** — "all emails from this sender, across all folders" in the inbox list moves from `V` to `ctrl+v`, which was too easy to mistype next to `ctrl+r` and `ctrl+e`. `V` is unbound.

**Fixes**

- Replying with `r` or `ctrl+r` to a mail you sent, opened from an Inbox thread, the conversation view or Archive, put your own address in To, demoted the real recipient to Cc and could pick the wrong From account. Own mail now replies To the original To, with reply-all Cc set to the original Cc minus your addresses, from the address it was sent from.
- Replying to mail received at an alias or catch-all address kept that address in Cc and sent from the last-used From. With `alternative_emails` configured it is stripped and the reply goes out from the matching account.
- Mail between your own addresses that is not in Sent now replies like received mail: back to the sending address, from the receiving one.

Every reply shape, from plain received mail to me-to-me, alias, mailing list, Reply-To and newsletter cases, is pinned in one test table so new cases are added there rather than fixed in isolation. The `alternative_emails` option is documented at https://neomd.ssp.sh/docs/configuration/.
