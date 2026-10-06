# AGENTS.md — Feature Invariants

This file lists the key **user-visible behaviors that must not break**. It exists because
subtle regressions have slipped in before (e.g. the `·` reply indicator silently broke when
reply detection was refactored — see CHANGELOG 2026-07-09).

**How to use this file (for AI agents and humans):**

1. **Before changing code**, scan the section(s) that touch your area and keep those
   invariants intact.
2. **After changing code**, re-scan the same section(s) and confirm each invariant still
   holds — run the pinning test if one is listed (`go test ./internal/... -run TestName`).
3. **When adding a feature**, add its invariant here (one bullet: behavior, code anchor,
   pinning test).
4. **At the end of every user-visible change, add a `CHANGELOG.md` entry** (dated heading,
   bold title, what/why/where, test name).

Build commands, architecture, and API quirks live in `CLAUDE.md`. Feature docs live in
`README.md` and `docs/content/docs/`. This file is only the "do not break" list.

---

## Hardening Suite — run after ANY change to sending or IMAP

neomd is used for business email: a mangled recipient, subject, attachment name, or
leaked Bcc reaches real clients. The hardening suite is the safety net that catches
this class of regression. **After any change touching `internal/smtp`, `internal/imap`,
`internal/schedule`, `internal/contacts`, or the send path in `internal/ui`, run:**

```sh
go test ./... -run Hardening          # unit: full build→wire→parse-back, no network
make test-integration                 # live: real SMTP+IMAP fidelity (demo account)
```

What it pins (all byte-exact, not substring checks):

- **`internal/imap/roundtrip_hardening_test.go`** — every message the builders produce
  is parsed back with go-message (what the recipient's client does) *and* `parseBody`
  (what neomd itself does): From/To/Cc/Subject decode exactly, plain part before HTML,
  body lines survive quoted-printable (umlauts), attachment names AND bytes identical
  (0–255 binary fixture), Message-ID uses sender domain, threading headers only on
  replies (never duplicated), drafts keep Bcc + literal markdown + attachments,
  send-later delivers byte-identical messages with no `X-Neomd-*` leak — except
  the Date header, which the daemon stamps with the ACTUAL delivery time via
  `schedule.RewriteDate` (changing any other byte fails the test). Also:
  long multi-encoded-word subjects and emoji decode back exactly
  (`TestHardening_RoundTrip_SubjectExtremes`), 20+ recipients keep order and count
  (`_ManyRecipients`), body edge cases survive — two-space hard breaks, lone `.`
  and `--` lines, header-lookalike lines, 2000-char lines, CRLF input, no wire
  line ends in literal whitespace (`_BodyEdgeCases`), inline image + file
  attachment combined shape with byte-exact payloads both views
  (`_InlineImagePlusAttachment`), wire format — strict CRLF, ≤998-char lines,
  parseable Date, unique Message-IDs (`TestHardening_WireFormat`), and emoji
  reactions parse back with exact To/threading/body (`_ReactionMessage`).
- **`TestHardening_HeaderInjection`** — CRLF in subject/recipients/threading IDs and
  hostile attachment filenames (`"`/newline) can never smuggle headers
  (`sanitizeHeaderValue`, `sanitizeFilenameParam` in `internal/smtp/sender.go`;
  control-char rejection in `contacts.Add`).
- **`internal/ui/workflow_hardening_test.go`** — WORKFLOW-level: drives the real
  bubbletea Update handlers end-to-end (reply-all → real `neomd-*.md` compose
  file → editorDoneMsg → pre-send → enter) and delivers to a fake in-process
  TLS SMTP server, asserting only what the outside world sees (auth user,
  MAIL FROM, RCPT TO, delivered wire bytes). This catches composition/wiring
  bugs that per-function tests can't (e.g. swapped cc/bcc arguments in
  `sendEmailCmd` = Bcc leak — verified by mutation test).
  `TestHardening_Workflow_ReplyAllUsesReceivingAccount`: reply goes out via the
  account whose address received the email (auth + MAIL FROM + From header),
  reply-all Cc excludes every own address (logins, account Froms, sender
  aliases), auto_bcc reaches RCPT but never headers, threading headers point at
  the original, `·`-indicator data survives.
  `TestHardening_Workflow_MarkdownFileToWire`: a real markdown compose file
  (`# [neomd: ...]` headers, `[attach]`, `[html-signature]`, signature block)
  arrives with To/Cc/Bcc routing, umlaut subject, literal-markdown plain part,
  rendered HTML (bold/link/callout), text sig in both parts, HTML sig in HTML
  only, attachment bytes identical, and no internal marker delivered.
- **`internal/ui/send_hardening_test.go`** — RCPT TO is complete (To+Cc+Bcc), deduped,
  bare addresses only, and unchanged by contact-name decoration; messy input
  (double/trailing commas, whitespace) never yields empty or malformed RCPT
  entries (`TestHardening_RcptNoEmptyOrMalformedEntries`); auto_bcc merges
  case-insensitively against decorated forms and reaches RCPT exactly once
  (`TestHardening_AutoBccPipeline`).
- **`internal/integration_hardening_test.go`** (live) — draft attachment round-trip
  under its original filename with identical bytes (the 2026-08 rename incident),
  full send fidelity through a real server (umlaut subject + binary attachment,
  no Bcc/X-Neomd header on the delivered message), scheduled-queue APPEND/FETCH
  round-trip, reply threading through the server (delivered In-Reply-To/References
  match the original's real Message-ID, envelope view included —
  `TestIntegration_Hardening_ReplyThreadingThroughServer`), and body fidelity as
  the recipient decodes it (hard breaks, dot-stuffed `.` line, 1200-char line,
  umlauts/emoji — `TestIntegration_Hardening_BodyFidelityThroughServer`).

When you add a new field or path to outgoing messages, extend the round-trip suite in
the same commit — a field that isn't parse-back-asserted is a field that can silently
break.

**Hardening assertions may only be extended, never weakened.** If a hardening test
fails after a code change, the default assumption is that the CODE broke a
user-visible contract — investigate the code first. Relaxing, deleting, or rewriting
a hardening assertion to make a change pass requires the user's explicit approval in
that conversation; "the test was too strict" is not a decision an agent makes alone.

---

## Reply & Threading

- **Quote header carries the original date** — replies and emoji reactions quote the
  original as `> **Name <addr>** wrote on 28 Sep 2026 at 13:29:` (local time, 24h,
  `buildQuotedReply` in `internal/editor/editor.go`); a zero `Date` falls back to the
  plain `wrote:` form. Forward keeps its own RFC-style `Date:` line. Tests:
  `TestReplyPreludeIncludesDate`, `TestReactionBodyIncludesDate`.
- **`·` reply indicator** — after sending a reply, the original email gets the IMAP
  `\Answered` flag (`MarkAnswered` in `internal/imap/client.go`, called from `sendEmailCmd`
  in `internal/ui/model.go`) and the inbox shows `·` (or `·╰` inside a thread,
  `internal/ui/inbox.go`). The local flag also updates immediately on `sendDoneMsg` without
  a refetch — in `m.emails` and the folder's cache snapshot (`setAnsweredLocal`), so a
  cache-hit switch, optimistic removal or `n` rebuild keeps the dot. Tests:
  `TestSendDoneMsgUpdatesAnsweredFlag`, `TestReplyIndicatorWithThread`,
  `TestReplyDotSurvivesListRebuild`.
- **Reply tracking survives pre-send round-trips** — `pendingIsReply` is *session-scoped*:
  re-edit (`e`), spell check (`s`), AI handoff (`i`), and CC/BCC edit (`ctrl+b`) from
  pre-send must all preserve `replyToUID`/`replyToFolder` and the `In-Reply-To`/`References`
  headers. The flag is cleared only when the compose session ends (send, discard, editor
  abort/error/empty, new compose/forward). Test: `TestEditorDoneReplyTrackingSurvivesReEdit`.
- **Threading headers on every reply-ish send** — regular replies, emoji reactions
  (`ctrl+e`), and iCalendar RSVPs all set `In-Reply-To` + `References` so conversations
  thread in Gmail/Outlook/Apple Mail. Tests: `TestBuildReactionMessage_ThreadingHeaders`,
  `TestBuildRSVPMessage_ThreadingHeadersBracketed`.
- **Reply prefix handling** — `Re:` is prepended only when the subject doesn't already
  start with `re:`/`aw:`/`sv:`/`vs:` (case-insensitive); localized prefixes are treated as
  replies, never double-prefixed. Test: `TestHasReplyPrefix`.
- **Reply From auto-selection** — replying picks the From address matching the email's
  To/CC; in the Sent folder the user's own address is in `From` instead
  (`matchFromForReply`, `internal/ui/model.go`).
- **Replying from the Sent folder goes to the original recipients** — `r` on a mail I
  sent addresses the reply to its original `To` (not to me, the sender); `ctrl+r` adds the
  original `Cc` minus own addresses. Outside Sent the classic Reply-To/From → To and
  To+Cc → Cc logic is unchanged (`replyRecipients`, `internal/ui/model.go`). Test:
  `TestReplyFromSentFolderTargetsOriginalRecipients`.
- **Reply-all excludes all own addresses** — both IMAP login addresses (`account.User`)
  and send-as addresses (accounts + `[[senders]]` aliases) are stripped from CC. Test:
  `TestReplyAllExcludesAllOwnAddresses`.
- **Threaded inbox rendering** — threads grouped via `In-Reply-To`/`Message-ID` with
  subject+participant fallback, `│`/`╰` connectors, newest on top; the Sent folder is
  intentionally **not** threaded. Tests: `TestNormalizeSubject`, `TestParticipantMatch`.
- **Sender view (`V`)** — from the inbox list, searches `from:<addr>` (bare address
  from the selected email) across every configured folder via the same
  `SearchAllFolders` IMAP infra as `/`-search, opening results in a `Sender` off-tab
  (`internal/ui/search.go`: `senderAddr`, `fetchSenderCmd`, `handleSenderResult`).
  `V` was chosen over `E`/`F` — both already bound (`E` = continue draft in the reader,
  `F` = mark as Feed). Tests: `TestSenderAddr`, `TestHandleSenderResultSetsOffTabAndEmails`,
  `TestHandleSenderResultNoMatches`.
- **Merged threads (HEY-style)** — `:merge <title>` / `:merge-sender <title>` store
  Message-IDs (never UIDs) in `<config dir>/merges.toml` (`internal/merge`); the list
  collapses members — plus any automatic thread containing a member — into one `≡`
  row placed by its newest member (`collapseMerges`, `internal/ui/thread.go`), rendered
  as `<title> (n)` with `N`/`·` aggregated over members. Collapsing is gated in
  `applyFilter` (`internal/ui/model.go`) to folder views and the `Search`/`Everything`
  off-tabs — the `T` conversation, the `V` sender view and an opened merge must always
  list individual messages. Enter/`l`/`T` on that row opens the members across folders
  (`SearchByMessageIDs`: Message-ID OR In-Reply-To, capped at the `mergeSearchMaxIDs`
  = 100 most recently stored ids) in a `Merged: <title>` off-tab; `T` on a normal row is
  unchanged. `esc` or `h` closes any off-tab view (or clears `/` and `z`) — `h` mirrors
  the reader's "back" and still pages up when nothing is open (test
  `TestInboxHKeyClosesOffTabView`). Bulk keys and `m` expand
  to the members via `targetEmails()`. Sender rules are applied on every folder load
  and persisted; `:unmerge` of a single member of a rule-bearing merge records its
  Message-ID in that merge's `Excluded` list (`merge.Store.Remove`/`IsExcluded`), so the
  rule can never silently undo the removal — an explicit `:merge` clears the exclusion.
  Absorbed replies are display-only until `:merge` is run on the row.
  Tests: `TestCollapseMerges_*`, `TestRenderCollapsedMergeRow`, `TestSetEmails_CollapsesMembers`,
  `TestTargetEmails_ExpandsCollapsedRow`, `TestMarkKey_TogglesAllMembers`,
  `TestApplySenderRules_PersistsMatches`, `TestApplyFilter_NoCollapseInThreadView`,
  `TestCmdLine_AcceptsUnicodeRune`, `TestHandleMergeResult_*`, `TestMergeCmd_*`,
  `TestMergeSenderCmd_*`, `TestUnmergeCmd_*`, `TestTitleCompletions`, `TestMessageIDCriteria`,
  `internal/merge` `TestAddSaveLoad_RoundTrip`, `TestRemove_ExcludesFromSenderRule`,
  `TestRemove_NoRuleDoesNotExclude`.

## Compose → Pre-send → Send Pipeline

- **`$EDITOR` is launched only via `editor.Command(args...)`** (`internal/editor/editor.go`) —
  never `exec.Command(os.Getenv("EDITOR"), …)`. The value may carry arguments
  (Omarchy: `omarchy-launch-editor --inline`; `code --wait`; `nvim -u ~/mail.lua`): it is
  whitespace-split, or run through `sh -c '… "$@"'` when it contains shell syntax; empty →
  `nvim`. Compose, reply, reply-all, forward, continue-draft and read-only view all use it.
  Tests: `TestCommand_SplitsEditorArguments`, `TestCommand_QuotedEditorGoesThroughShell`.
- **Pre-send round-trip preservation** — every path that re-opens the editor or returns to
  pre-send (`e`, `s`, `i`, `ctrl+b`, draft continue, `:recover`) must preserve: body,
  attachments (re-injected as `# [attach]` lines — editor body is source of truth),
  Bcc, selected From, and reply tracking (see above). History: CHANGELOG 2026-04-08,
  2026-05-06, 2026-07-02, 2026-07-09 — this area regresses easily.
- **MIME structure by content** (`BuildMessage` in `internal/smtp/sender.go`):
  no attachments → `multipart/alternative`; file attachments → `mixed > alternative`;
  inline images → `related > (alternative + image parts with Content-ID)`; both →
  `mixed > (related > alt+images) + file parts`. Tests: `TestBuildMessage*`.
- **Inline images** — local `<img src="/abs/path">` rewritten to `cid:`; paths with spaces
  use the `![](<path>)` angle-bracket form and URL-decoding before file read; remote
  `https://` images (HTML signatures) are fetched (10 s timeout) and embedded as `cid:`,
  falling back to the URL on fetch failure. Tests: `TestBuildMessage_WithInlineImage`,
  `TestBuildMessage_InlineImagePathWithSpaces`.
- **`[attach]` markers are visible plain text** — `# [attach] /path` (header form) and
  `[attach] /path` (inline form), never HTML comments (treesitter hides them in nvim).
  Only regular files are accepted (`filterValidAttachments`); skipped paths are surfaced
  in the status bar. Test: `TestFilterValidAttachments`.
- **BCC privacy** — Bcc is excluded from message headers but included in SMTP `RCPT TO`;
  comma-separated recipients are split into individual RCPT commands; `auto_bcc` is
  deduped and visible (never silent). Test: `TestCollectRcptTo`.
- **RFC compliance** — Message-ID uses the sender's domain (never `@neomd`/`@localhost`);
  quoted-printable encodes trailing whitespace before CRLF (`=20`) so Markdown two-space
  hard breaks survive SMTP relays; text/plain part comes before text/html.
- **Signatures** — per-account `[accounts.signature_block]` overrides the global block
  all-or-nothing via `Config.Signature(account)`; text signature goes to editor + plain
  part, HTML signature to HTML part only; `[html-signature]` placeholder controls
  inclusion per-email and is extracted right before send. Test: `TestSignature`.
- **Drafts** — saved as plain text only (multipart caused round-trip corruption), keep
  `Bcc`; every compose session is backed up to `~/.cache/neomd/drafts/` (`:recover`);
  discarding unsent mail always asks y/n confirmation.
- **Draft attachments keep their original filename** — continuing a draft writes
  extracted attachments into a fresh temp dir under their real basename (never a
  mangled `CreateTemp` name — the sent filename is the path's basename). Duplicates
  dedupe as `name-2.ext`; traversal/empty names sanitized (`writeAttachmentsTemp`,
  `internal/ui/model.go`). Test: `TestWriteAttachmentsTempPreservesFilename`.
- **Contact-name decoration is headers-only** — bare To/Cc addresses with a harvested
  contact name become `Name <addr>` in message headers at send time, but
  `collectRcptTo` always uses the raw undecorated fields and Bcc is never decorated
  (BCC privacy + comma-split RCPT must not break). Unsafe names (`,<>"`), and any
  part already containing `<`, are left untouched; `first.last@` derivation never
  fires for role mailboxes (`contacts.Decorate`, `contacts.DeriveName`). Tests:
  `TestHarvestNameAndDecorate`, `TestAddRejectsUnsafeNames`, `TestDeriveName`.
- **Compose autocomplete matches contact names** — To/Cc/Bcc suggestions come from
  the contacts store (matched by display name OR address, suggested as
  `Name <addr>`) plus screener-list addresses (decorated when the name is known);
  nil store never panics. Typed `Name <addr>` recipients are harvested into the
  cache at send/schedule time (`harvestTypedRecipients`) so a name typed once
  persists. Tests: `TestComposeSuggestions_*`, `TestHarvestTypedRecipients`.
- **Send-later queue marker is display-only** — queued messages are identified by
  a peek'd `X-Neomd-Send-At` header-fields fetch (`Email.SendAt`,
  `parseSendAtSection`) and shown with a `[send-later …]` subject prefix at
  render time only; the stored subject/message is NEVER mutated (it is what gets
  delivered) and GTD mail sharing the Scheduled folder stays unmarked. Tests:
  `TestParseSendAtSection`, `TestSendLaterPrefix`, live assert in
  `TestIntegration_Hardening_ScheduledQueueRoundTrip`.
- **Re-saving a working copy replaces, never duplicates — and never deletes
  early** — continuing a queued send-later message OR a saved draft via `E`
  tracks the original (`requeue` in `internal/ui/model.go`); it is moved to
  Trash (recoverable) ONLY after the replacement is successfully scheduled,
  saved, or sent. Regular emails opened with `E` are NEVER tracked (a received
  mail must never be trashed by sending an edit of it). Abort/discard/error
  paths clear the tracking without touching the original; a failed cleanup
  warns loudly (double-delivery risk). Tests:
  `TestContinueDraftTracksQueuedOriginal`, `TestScheduleDoneReplacesQueuedOriginal`,
  `TestSendDoneReplacesQueuedOriginal`, `TestSaveDraftReplacesPreviousVersion`,
  `TestContinueRegularEmailNeverTracked`, `TestEditorAbortKeepsQueuedOriginal`,
  `TestRequeueCleanupFailureWarns`.
- **Send-later watchdog** — the TUI checks Scheduled on startup and every
  background sync; queued messages more than 10 min past due (`overdueGrace`)
  raise a red OVERDUE status warning — a down daemon must never silently
  swallow a scheduled email. Fetch errors are silent (retried next tick).
  Tests: `TestCountOverdueScheduled`, `TestOverdueScheduledWarns`.
- **Send later never double-delivers** — the daemon claims a due Scheduled message
  with `\Flagged` *before* SMTP; flagged leftovers are skipped and logged, never
  retried automatically (`processScheduled`, `internal/daemon/daemon.go`). The
  delivered message and its Sent copy must carry **no** `X-Neomd-*` headers
  (`X-Neomd-Rcpt` contains Bcc!); messages without `X-Neomd-Send-At` in the
  Scheduled folder (GTD items) are never touched. At delivery the Date header
  is rewritten to the actual send time (`schedule.RewriteDate` — only that one
  line may change), so recipients and the Sent copy show when the mail went
  out, not when it was queued. Tests:
  `TestInjectExtractRoundTrip`, `TestExtractIgnoresRegularMail`,
  `TestSMTPConfigFor`, `TestRewriteDate`.
- **Out-of-office replies are screened-in-only and reply exactly once** — the daemon's
  `processOOO` (`internal/daemon/daemon.go`, logic in `internal/ooo/`) answers only
  senders classified `CategoryInbox`, only mail dated after the persisted period start,
  and only to the sender address (Reply-To pref, From fallback) — **never** Cc, never
  our own addresses. The `~/.cache/neomd/ooo_replied` cache is written *before* SMTP
  (crash ≠ duplicate); changing `[ooo].from`/`until` resets it (`ooo.Period`); a set
  `from` arms OOO at that day's midnight or the exact `"YYYY-MM-DD HH:MM"` time,
  interpreted in `[ooo].timezone` (IANA; default daemon-machine local time)
  (`ooo.StartFor`/`parseWhen`/`location`; date-only `until` stays inclusive
  end-of-day; unknown timezone fails safe: inactive). `[ooo].accounts` switches the
  watched inboxes to those accounts (per-account From/signature/Sent, lazy extra IMAP
  clients via `oooClientFor`; unknown or imap_disabled names are hard errors; the
  reply-once cache stays shared across accounts). Tests: `TestResolveOOOAccounts`. Loop guard both directions:
  incoming `Auto-Submitted`/`Precedence: bulk|junk|list`/`List-Id`/`List-Unsubscribe`
  mail is skipped; outgoing replies carry `Auto-Submitted: auto-replied` +
  `X-Auto-Response-Suppress: All`. Replies are built with the same
  `BuildMessageWithThreading` pipeline as composed mail (MIME shape, signatures,
  threading) and copied to Sent; the reply subject is the configured `[ooo].subject`
  verbatim (default "Out of Office" — the original subject is never appended).
  An `ooo.toml` next to config.toml replaces the whole `[ooo]` block and is
  re-read by the daemon EVERY pass (hot reload — `make ooo` syncs it to the server with
  no restart); invalid TOML fails safe (no replies). TUI ignores `[ooo]` entirely.
  Tests: `TestShouldConsider`, `TestIsAutoGenerated`, `TestCache_*`, `TestBuildReply_*`,
  `TestActive_*`, `TestLoadOOOOverride_*`.
- **Callouts** — `> [!note]` / `> [!tip]` / `> [!warning]` (with or without space after
  `>`) render as styled boxes in the HTML part and as emoji text (no blockquote markers)
  in the plain part. Tests: `TestToHTML_Callout_*`, `TestFormatCalloutsForPlainText_*`.
- **Listmonk interception** — sending to a configured trigger address creates a scheduled
  Listmonk campaign instead of SMTP delivery; pre-send shows list IDs + template + delay.
  Tests: `TestResolveListIDs`, `TestResolveTemplateID`.
- **From cycling (`ctrl+f`)** — SMTP credentials, Sent-folder destination, and From header
  must all follow the selected identity (accounts first, then `[[senders]]` aliases).
  Tests: `TestPresendSMTPAccount`, `TestReactionAutoSelectsCorrectFromAndSMTP`,
  `TestSentDraftsIMAPClient_*`.
- **Address headers are always 7-bit** — `buildMessageWithBCC` runs From/To/Cc/Bcc through
  `encodeAddressNames` (`internal/smtp/sender.go`): non-ASCII display names get RFC 2047
  encoding via `net/mail`, addresses are untouched, and an all-ASCII field (including one
  that already carries encoded-words) is returned byte-for-byte. SMTP RCPT TO is derived
  from the caller's original strings, never from the built headers. Tests:
  `TestEncodeAddressNames`, `TestBuildMessage_HeadersAre7BitWithNonASCIINames`.
- **Inline-image Content-IDs are unique per message** — `buildMessageWithBCC` names parts
  `img<n>.<random hex>@<sender domain>` (`newCID`). A fixed id (the old `img0@neomd`)
  collides with the same id inside quoted earlier neomd mails and the new part hijacks
  every quoted picture. Foreign `cid:` references in the HTML are never rewritten.
  Tests: `TestBuildMessage_InlineCIDsAreUniquePerMessage`,
  `TestBuildMessage_QuotedForeignCIDIsNotHijacked`, `TestHardening_RoundTrip_InlineImagePlusAttachment`.
- **Reply/forward re-embed the quoted mail's inline images** — `Model.quotedBody()` →
  `materializeInlineImages` (`internal/ui/inline_images.go`) writes every referenced
  `cid:` part of the open email to `~/.cache/neomd/inline/<folder>-<uid>/` and rewrites
  `(cid:…)` / `"cid:…"` to that path, so the sender's local-image pass embeds it under a
  fresh Content-ID. Unreferenced parts are not written; unknown cids stay as-is; file
  names are sanitised (no traversal). Tests: `TestMaterializeInlineImages_*`.
- **The text/plain alternative never carries image paths or cid: markdown** —
  `prepareEmailBodies` runs `render.ImagePlaceholdersForPlainText` (`![alt](dest)` →
  `[Image: alt-or-filename]`) on the send path only; drafts keep the raw markdown so
  they can be resumed. Tests: `TestImagePlaceholdersForPlainText`,
  `TestBuildMessage_PlainPartHasNoImagePaths`.
- **Draft round trip is byte-exact, attachments and image references included** —
  `BuildDraftMessage` stores the raw markdown as text/plain (+ file parts); `parseBody`'s
  `X-Neomd-Draft` branch returns it verbatim except for undoing the wire CRLF (a
  multipart draft used to come back with `\r\n`, showing `^M` in the editor and drifting
  on every re-save). Test: `TestHardening_DraftRoundTrip_InlineImageAndAttachment`
  (image markdown at its spot, attachment name + bytes, two cycles).
- **Image sizes survive HTML → markdown → HTML** — `htmlToMarkdown` (`sizedImageRule`,
  `internal/imap/client.go`) writes an `<img>`'s explicit width/height (attribute or
  inline `px` style) as the markdown title `![alt](src "WxH")`; `render.ToHTML`
  (`applyImageSizeTitles`) turns that marker back into `width`/`height` attributes and
  drops it, keeping real titles. Without this a signature logo constrained to 70px
  came back at its natural size in every reply that quoted it. The marker is plain
  CommonMark (editor, drafts, plain-text placeholder all cope). Tests:
  `TestHTMLToMarkdown_PreservesImageSizeAsTitle`, `TestToHTML_ImageSizeTitleBecomesWidthHeight`,
  `TestBuildMessage_SizedImageKeepsWidthHeightAfterEmbedding`.

## Screener (HEY-style)

- **Priority order** — spam > screened_out > feed > papertrail > screened_in; per-address
  entries always beat `@domain` entries. Tests: `TestClassify`, `TestClassifyForScreen`.
- **Reclassification is atomic** — classifying removes the address from ALL conflicting
  lists (snapshot/rollback on failure, both files and moved emails). Test:
  `TestCrossListCleanup_Reclassification`.
- **Empty lists pause screening** — TUI (Inbox load AND the 5-minute background sync) and
  headless daemon all skip auto-screening until the first sender is classified (prevents
  sweeping a fresh inbox to ToScreen); `auto_screen_on_load = false` also stops the
  background sync's screening. Tests: `TestScreenInbox_EmptyScreenerLists`,
  `TestBgSync_SkipsScreeningWhenListsEmptyOrDisabled`.
- **`:screen` / `S` run on the plain Inbox tab only; every auto-screen MOVE uses the
  email's own folder as source** — both refuse on any other tab and inside a
  Search/Thread/Everything view ("screen runs on the Inbox tab only"), and
  `autoScreenPlan` captures `screenMove{uid, src: e.Folder, dst}` so a plan can never
  address another folder's UIDs. A failed auto-screen MOVE reloads once without
  auto-screening (`skipAutoScreenOnce`), so a persistent NO cannot loop
  reload→screen→fail; the next load retries. Tests:
  `TestScreenCommand_RefusedOutsideInboxTab`, `TestAutoScreenPlan_UsesEmailFolderAsSource`,
  `TestAutoScreen_ErrorDoesNotLoop`.
- **Screener destinations may never be Trash** — refuses to run otherwise. Test:
  `TestValidateScreenerSafetyRejectsTrashDestination`.
- **ToScreen sender-level classify** — acting on one unmarked message applies to all
  queued mail from that sender.
- **Lists are line-based with `#` comments** (full-line and inline); daemon only reads
  lists and moves mail, never writes classifications.
- **External classify goes through `neomd screen`** (`cmd/neomd/screen.go`) — the CLI
  subcommand for widgets (omarchy bar plugin) must keep TUI parity: list update BEFORE
  any move, sender-level expansion over all queued ToScreen mail, and the
  `ValidateScreenerSafety` Trash gate. Its read-only siblings `neomd list`
  (`cmd/neomd/list.go`) and `neomd read` (`cmd/neomd/read.go`) must never mutate flags
  or folders — `read` goes through `FetchBody`'s `BODY.PEEK`, so a widget glance can
  never set `\Seen`. All three always emit one JSON object and exit 0 even on failure.
  Tests: `TestRunScreen_ApproveMovesAllFromSender`,
  `TestRunScreen_RefusesTrashDestination`, `TestRunList_JSONShape`,
  `TestRunRead_JSONShape`.

## Inbox Display

- **Rows never overflow the terminal width** — complex scripts (Bengali/Arabic/Thai/emoji)
  collapse to `·` for display only; CJK passes through (East Asian Wide is deterministic);
  the original subject is never mutated (reply/forward/thread logic uses the real RFC
  subject). Tests: `TestRowFitsTerminalWidth`, `TestDisplaySafe`.
- **Indicator columns** — unread, `·` replied, `°` spy pixel, `│`/`╰` thread connectors.
- **Undo (`U`)** — reverses the last move/delete using UIDPLUS destination UIDs captured
  on move; batch operations preserve partial-undo info on failure. Integration test:
  `TestIntegration_IMAPMoveAndUndo`.
- **`ctrl+d`/`ctrl+u` page the inbox like `d`/`u`; `esc` clears marks** — the vim
  half-page keys are pure cursor moves and never touch marks. "Clear all marks" is the
  first step of the `esc`/`h` back-one-level cascade in `updateInbox` (marks → temporary
  view → filter/unread-only); the header hint says `esc to clear`. `ctrl+m` is not
  bindable (terminal carriage return = `enter`). Tests: `TestInbox_CtrlDPagesDownLikeD`,
  `TestInbox_CtrlUPagesUpLikeU`, `TestInbox_EscClearsMarks`,
  `TestInboxHeaderMarkHintNamesEsc`.

- **Search matches contact names** — `internal/contacts` harvests `Name <addr>` pairs
  from loaded headers into `~/.cache/neomd/contacts`; the local `/` filter appends
  resolved names to its haystack, and server-side search (`space /`) expands a name
  query into per-address queries (never for `subject:`), deduped by folder+UID.
  Envelope To/CC/BCC keep display names (`formatEnvelopeAddr`) — names that would
  break comma-splitting fall back to the bare address. Tests:
  `TestFormatEnvelopeAddr`, `TestExpandSearchQueries`,
  `TestContactNamesForResolvesBareAddresses`.
- **Optimistic removal is never trusted past an error** — `x`/`A`/`B`/`M*`/`I O F P $`
  and auto-screen drop rows via `removeFromList` (`internal/ui/model.go`) before the
  MOVE runs; `removeFromList` rebuilds the list so the cursor lands on the same
  folder+UID as before the removal, or on the next row when that email itself was
  removed — exactly as a normal reload does. `batchDoneMsg`/`autoScreenDoneMsg` pick
  the redraw path with `optimistic := len(msg.removed) > 0 || !m.loading` (a tagged
  message is optimistic even while a body fetch owns the spinner, and then leaves that
  spinner alone): success ends in a background refresh (`refreshActiveFolderCmd`, header
  `↻`), any error ends in `loading = true` + full reload with the error in the status
  line and partial undo kept. The optimistic completion never clears marks (they were
  cleared at keypress; marks made since are the user's next selection). `U` refuses
  while an optimistic move is in flight (`pendingBatches`, "Move still in progress") —
  the running move's undo entry is only pushed on completion. `I O F P $` keep rows that
  are already in the key's target folder (no MOVE runs for them; the list file is still
  written). `U` undo, `X`, delete-all and `:screen` always take the non-optimistic
  spinner-reload path. Server calls, order and audit lines are unchanged. On error the
  source folders' snapshots are dropped (they were trimmed optimistically), and a
  synthetic view (Search, Thread, …) is left via `leaveSyntheticView` so the tab-folder
  reload is applied. Tests: `TestOptimistic_*`, `TestOptimistic_ErrorDropsOriginFolderCache`,
  `TestOptimistic_ErrorInSearchViewReloadsTabFolder`,
  `TestOptimistic_MarksMadeDuringMoveSurviveCompletion`,
  `TestOptimistic_UndoWaitsForInFlightMove`, `TestOptimistic_ScreenInInboxKeepsRow`.
- **`n` flips `\Seen` locally first; a fetch landing mid-STORE keeps the flipped state**
  — the `n` handler sets the flag in the list and the cached snapshot (`setSeenLocal`),
  records the wanted state in `pendingSeen` (account+folder+UID) and runs the STOREs
  behind the list (`toggleSeenCmd(ops []seenOp)`, plan by value). `withPendingSeen`
  overlays that state on every fetch result (`emailsLoadedMsg`, `bgInboxFetchedMsg`,
  prefetch) until `toggleSeenDoneMsg` releases it; on error only the ops that never
  reached the server are reverted, with the error in the status line. Never reintroduce
  a spinner or a folder reload for `n` — the lag it caused was the bug. Tests:
  `TestToggleSeen_*`.
- **A removed row stays removed until its MOVE finished** — `removeFromList` records
  each target in `pendingRemoval` (account+folder+UID); `optimisticRemove` tags the MOVE
  command's `batchDoneMsg`/`autoScreenDoneMsg` with those keys (`releaseOnDone`) and the
  handlers release exactly them, on success and on error. Until then `emailsLoadedMsg`
  and `bgInboxFetchedMsg` filter those rows out of every fetch result (`withoutPending`)
  before caching or showing it, so a refresh that started before the MOVE cannot
  resurface them. Every `removeFromList` must go through `optimisticRemove` with a
  command that ends in one of those two messages, or its rows stay hidden. Tests:
  `TestOptimistic_LateRefreshDoesNotResurfaceRemovedRow`,
  `TestOptimistic_ErrorReleasesPendingRemoval`.
- **A background refresh keeps marks and the `/` filter** — `emailsLoadedMsg` treats a
  result as a `↻` refresh when `refreshing` is set (whatever `loading` says): marks
  survive for UIDs still in the result, the filter text stays, and `loading` is not
  touched (a body fetch, `T`, `V` … may own the spinner). Every spinner folder load (`R`,
  `:reload`, cache-miss switch, undo/error reloads, `gS`/`gd`/`:go-spam`) sets
  `refreshing = false` together with `loading = true`, so the two can never be
  confused; a spinner reload clears marks and filter as before. The bg sync caches the
  Inbox without the rows it is about to screen. Tests:
  `TestCache_BackgroundRefreshKeepsMarksAndFilter`,
  `TestCache_RefreshLandingDuringBodyFetchKeepsMarksAndSpinner`,
  `TestCache_BgInboxSnapshotExcludesScreenedRows`.
- **A cached list is only shown with `↻` and a fetch in flight** — `loadActiveFolder`
  serves `folderCache[account+folder]` on tab switches (`[ui].instant_folder_switch`,
  default true); `emailsLoadedMsg` always caches and applies to the visible list only
  when folder AND account are still active (late results for another folder are cached,
  not shown). Synthetic off-tab views (Search, Everything, Thread, Sender, Merged: …)
  are never overwritten by a stale folder result — `emailsLoadedMsg` only clears
  `loading`/`refreshing` for them and leaves `m.emails` alone; Drafts and Spam are
  exempt from that guard since they map to a real cached folder (`:go-spam` was fixed
  to set `offTabFolder = "Spam"` so it participates correctly — a latent bug the guard
  surfaced). `R` bypasses the cache. Prefetch (`folderPrefetchedMsg`) fills the cache
  only. Account switch (`ctrl+a`), tab keys, `<space>N`, `g<x>` and tab clicks all leave
  a synthetic view (`leaveSyntheticView`: `offTabFolder`, `imapSearchResults`,
  `imapSearchText`); `ctrl+a` also resets `prefetched` so the new account's tabs are
  warmed. A failed folder fetch (`folderErrMsg`, carries folder+account) for the visible
  folder after a spinner switch shows that folder's snapshot or an empty list — never
  the previous folder's rows; failures for another folder/account are ignored. Tests:
  `TestCache_*`, `TestPrefetch_*`, `TestBgSync_SkipsAutoScreenWhenAccountChanged`,
  `TestCache_AccountSwitchLeavesSyntheticView`, `TestCache_AccountSwitchCapturesNewAccount`,
  `TestCache_FolderFetchErrorClearsPreviousFolderRows`.
- **The user's `[contacts]` file is read-only** — `contacts.MergeFile` only reads;
  neomd persists exclusively to its own cache (`config.ContactsCachePath()`), so the
  cache can be deleted anytime and rebuilds from harvesting + the file. The picker
  (`space c`, `internal/ui/contacts_picker.go`) copies via external clipboard tools
  and never mutates the store. Tests: `TestMergeFileGoogleCSVRealExport`,
  `TestContactsPickerFilterAndSelect`.

## Reading & Security

- **Large mail fetches text first; `Attachment.Data` may be nil** — for a
  `multipart/mixed` message ≥ 1 MB, `FetchBodyOf` (`internal/imap/lazybody.go`) keeps
  every top-level child except the big ones: the body root and every child whose subtree
  is ≤ 256 KB come in one FETCH and are reassembled into a synthetic multipart/mixed
  (`reassembleKept`), so `parseBody` sees the original structure minus the dropped
  parts (plain+HTML siblings, inline `cid:` images, calendar parts, attachment names and
  spy pixels behave exactly as on the full fetch). Children > 256 KB are returned as
  metadata (`Data == nil`, `Part` set, `Size` = subtree size); nothing to drop, or a
  server that does not echo a requested section, means the full fetch. Every consumer of
  `Attachment.Data` must go through `attachmentOnServer` → `fetchAttachmentCmd` →
  `attachmentFetchedMsg` (reader `1`–`9`, `<space> v` chord, `E` draft reopen) or
  tolerate nil (inline images, calendar card). The download result carries folder+UID and
  is dropped unless that email is still open — never applied by index to another mail.
  `E` (continueDraft) downloads every server-side part first (`then: "draft"` loops until
  all are present); `writeAttachmentsTemp` refuses an attachment that was never
  downloaded instead of writing a 0-byte file. `FetchBody`, `.eml` download, headers view
  and the daemon keep the full raw fetch. `FetchHeaders` keeps the BODYSTRUCTURE on
  `imap.Email` so the lazy path costs no extra round trip. Tests: `TestLazy_*`,
  `TestMem_FetchBodyOf_*` (incl. `_PlainAndHTMLSiblings`, `_InlineCIDSibling`,
  `_CalendarFirstChild`), `TestIntegration_LazyBodyOnRealServer`, `TestLazyAttachment_*`
  (incl. `_StaleDownloadForOtherEmailIgnored`, `_ContinueDraftDownloadsFirst`),
  `TestWriteAttachmentsTemp_RefusesServerSideAttachment`.
- **Reader caps displayed blockquote depth at 3** — glamour's blockquote cost is
  superlinear in nesting depth (a 33-deep Gmail reply chain took 2.3 s to render, 0.4 s
  capped). `loadEmailIntoReader` (`internal/ui/reader.go`) passes the body through
  `capQuoteDepth(body, maxReaderQuoteDepth)` before `render.ToANSI`: lines quoted deeper
  are rewritten to three `>` markers, a blank quoted line separates adjacent capped lines
  of different original depth, fenced code is untouched, and a body nested ≤ 3 round-trips
  unchanged. This is display only — never apply it to `Model.openBody`, which feeds
  reply/forward/react quoting, the `e` editor view, `O` browser view and drafts. Tests:
  `TestCapQuoteDepth_*`, `TestReader_DeepQuoteChainRendersFast`.
- **Spy pixels blocked** — two layers: curated denylist with attribution
  (`internal/imap/tracker_list.go`) + generic 1×1 heuristic; glamour never fetches remote
  resources; results cached in `~/.cache/neomd/spy_pixels` (`+key` spy / `-key` clean).
  Tests: `TestSpyPixelDetection`, `TestSpyPixelSpacersNotFlagged`.
- **Browser view (`O`) injects CSP** — `script-src 'none'; frame-src 'none';
  object-src 'none'`; remote images intentionally allowed there. Test:
  `TestIntegration_BrowserSanitization`.
- **Link opening whitelist** — only `http://`, `https://`, `mailto:` schemes. Test:
  `TestURLSchemeValidation`.
- **Raw headers & unsubscribe (`<space>h` / `<space>u`)** — both chords fetch the message
  once via `FetchRaw` (same FETCH as `.eml` download) and cache the header block in
  `Model.openRawHeaders`; it is reset on every `bodyLoadedMsg`. `<space>h` cycles the
  viewport body → curated headers (`weedHeaders`: `weedOrder` list + every `List-*` /
  `X-Spam*`) → full raw block → body (`toggleHeadersView`, `Model.headersMode`).
  `<space>u` resolution order is fixed: `List-Unsubscribe` **https** entry → `openLinkCmd`;
  `List-Unsubscribe` **mailto** entry → prefilled compose (subject from `?subject=`, default
  `unsubscribe`); else first body link whose text/URL contains `unsubscribe`; plain `http`
  entries are ignored. Helpers in `internal/ui/unsubscribe.go`. Tests: `TestHeaderBlock*`,
  `TestHeaderValue*`, `TestWeedHeaders*`, `TestParseListUnsubscribe*`, `TestParseMailto*`,
  `TestFindUnsubscribeLink*`.
- **Attachment open safety** — executable extensions are saved but never auto-opened;
  magic-byte mismatch detection (`http.DetectContentType`) blocks disguised files;
  sender-supplied filenames are sanitized against path traversal (`..`, separators) in
  every write path (downloads, `.ics`, `cid:` temp files).
- **Timer-based mark-as-read** — opening an email marks `\Seen` only after
  `mark_as_read_after_secs` (default 7 s); quick peeks stay unread; reply/forward marks
  immediately.
- **Browser view declares UTF-8** — `SanitizeForBrowser` strips every `<meta charset>` /
  `http-equiv=Content-Type` tag from received HTML (the body is already transcoded to
  UTF-8 by go-message) and injects `<meta charset="utf-8">` + the CSP first in `<head>`;
  Outlook's `charset=Windows-1252` meta otherwise renders "Späti" as "SpÃ¤ti". Own
  goldmark output (already UTF-8 + CSP) passes through unchanged. Test:
  `TestSanitizeForBrowser_ForcesUTF8Charset`.

## IMAP & Runtime Resilience

- **Retry policy** — `withConnRetry` (one retry) only for read-only ops (FETCH/SEARCH/
  STATUS); mutating ops (MOVE/APPEND/STORE) use `withConn`, never retried (duplicate-mail
  risk). NOOP health probe after 2+ min idle handles suspend/resume.
- **The fetch window is selected by INTERNALDATE, never by highest UID** — MOVE/COPY
  give a message a fresh, highest UID in the destination mailbox, so "last n UIDs" is
  "last n moved-in", not "newest n" (issue #34: bulk approve pushed recent Inbox mail
  below the cutoff). `FetchHeaders`, `FetchLatest`, `FetchMoreHeaders` and the
  `searchFolderCap` all narrow through `newestUIDsByInternalDate` (one `UID FETCH (UID
  INTERNALDATE)`); folders within the window and `inbox_count = 0` skip that round trip.
  Never use the SORT extension as the only path (Infomaniak lacks it). Tests:
  `TestMem_FetchHeaders_WindowIsNewestByInternalDateNotUID`, `TestMem_FetchLatest_*`,
  `TestMem_SearchFolder_CapIsNewestByInternalDateNotUID`, `TestMem_FetchMoreHeaders_*`.
- **Load more never duplicates, drops or collapses rows** — `j`/`down`/`d`/`ctrl+d` on
  the last row of a tab folder runs `loadMoreIfAtBottom` → `FetchMoreHeaders(n, loaded
  UIDs)`; the result appends only unknown UIDs, re-sorts, keeps the cursor
  (`reselectEmail`), extends the folder cache, and `windowFor` makes every later
  `fetchFolderCmd`/bg Inbox fetch request the extended size so `R`/↻ keep the list. A
  short page (or an initial load below `inbox_count`) sets `folderComplete`, which also
  hides the hint bar's leading "↓ more below" cue (`inboxHintBar`, `moreBelow`); `G`,
  synthetic views and `inbox_count = 0` never page; a stale result for another folder
  is dropped but clears `loadingMore`. Tests: `TestLoadMore_*`.
- **`safeGo` everywhere** — background goroutines must use `safeGo()` (panic → 
  `~/.cache/neomd/crash.log`), never bare `go func()`. Maps passed to goroutines are
  snapshotted on the main goroutine first (spy-pixel cache race, CHANGELOG 2026-05-08).
- **Nothing blocks the bubbletea Update loop** — notifications have a 2 s timeout, no DNS
  lookups in the send path, background sync never tight-loops on error. Test:
  `TestSend_TimeoutCannotBlockTUI`.
- **`imap_disabled = true` accounts produce nil clients by design** — every helper that
  resolves an IMAP client must skip nil entries (send, Sent-copy, `\Answered`, `:debug`,
  headless). Tests: `internal/ui/imap_client_helpers_test.go`.
- **Every server-side MOVE/EXPUNGE is audited** — `MoveMessage` and `ExpungeAll`
  (`internal/imap/client.go`) append `<RFC3339> MOVE <src> uid=<n> -> <dst> destUID=<m>` /
  `MOVE-FAILED …` / `EXPUNGE <folder> uids=[…]` to `config.AuditLogPath()`
  (`~/.cache/neomd/moves.log`), enabled in `cmd/neomd/main.go` right after `config.Load`
  for TUI, daemon and CLI alike. A "mail vanished" report is traced there first.
  Test: `TestAuditLog_AppendsLines`.
- **Undo never guesses a UID** — `MoveMessage` returns `destUID = 0` when the server
  sends no UIDPLUS COPYUID (it used to fall back to the source UID); `U` runs
  `undoableMoves` and skips such entries with a status message instead of moving whatever
  carries that UID in the destination folder. Test: `TestUndoableMovesSkipsUnknownDestUID`.
- **The cursor follows the email across reloads** — `emailsLoadedMsg` remembers the
  selected folder+UID and `reselectEmail` puts the cursor back on it after the list is
  rebuilt; only when that email is gone does the index stay (cursor lands on the next
  row, as after a delete). Keeping the bare index let the highlighted row change silently
  whenever rows shifted, so the next `x`/`A`/`M` hit mail the user never chose.
  Test: `TestReloadKeepsCursorOnSameEmail`.
- **Envelope names/subjects decode every charset** — every go-imap connection is built by
  `clientOptions()` with `WordDecoder: envelopeWordDecoder` (charset-aware via
  go-message's `charset.Reader`). Without it go-imap's default decoder only knows UTF-8 /
  ISO-8859-1 and Outlook's `=?Windows-1252?Q?...?=` names leak raw into the inbox, reader
  and reply screens. Tests: `TestEnvelopeWordDecoder_DecodesWindows1252`,
  `TestClientOptions_SetWordDecoder`.
- **SELECT is pipelined with the first command; MOVE keeps the mailbox selected** —
  `beginSelect`/`endSelect` (`internal/imap/client.go`) send SELECT and the following
  UID SEARCH / UID FETCH back-to-back (RFC 9051 §5.5); a failed SELECT drains the
  pipelined response and the connection stays usable. `FetchUnseenCounts` sends all
  STATUS commands before waiting. `MoveMessage` no longer clears `selectedMailbox`
  (RFC 9051 §6.4.8: the source stays selected; every later op is UID-addressed).
  Never reintroduce a per-command SELECT. Tests: `TestMem_FetchHeaders_*`,
  `TestMem_FetchUnseenCounts`, `TestMem_MoveMessage_KeepsSelectionAndBatchWorks`,
  `TestIntegration_MoveWithoutReselect`, `TestIntegration_PipelinedFetchMatchesSearch`.
- **Background connection is never used for user actions** — `bgImapCli()` serves
  tab counts, the 5-minute sync, VIP polls, spy scan, overdue check and prefetch;
  folder loads, body fetches, moves, screening, flags, search, undo and the
  auto-screen MOVEs after an Inbox load stay on `imapCli()` so a user's consecutive
  actions are serial. Falls back to the primary when nil. While `bgSyncInProgress`
  an Inbox load skips its own auto-screen pass. Tests: `TestBgImapCli_*`,
  `TestInboxLoadSkipsAutoScreenWhileBgSyncRuns`.
- **Background goroutines never hold pointers into `m.emails` or a `folderCache`
  snapshot** — the UI re-sorts both in place (a cache-hit Tab sorts the snapshot it
  serves). MOVE plans capture UIDs by value at command construction
  (`execAutoScreenCmd`/`bgExecAutoScreenCmd` via `autoScreenPlan`, `batchMoveCmd`), and
  `bgInboxFetchedMsg` caches a copy of the fetched slice. Reading `mv.email.UID` inside
  the goroutine moved the wrong message after a Tab (and was a data race). Tests:
  `TestAutoScreenPlan_CapturesUIDsByValue`, `TestCache_BgInboxSnapshotIsACopy`
  (`go test -race ./internal/ui`).
- **A failed SELECT clears the cached selection** — `selectMailbox` resets
  `selectedMailbox` on error (RFC 9051: a failed SELECT deselects). Test:
  `TestMem_FailedSelectClearsCachedSelection`.
- **`NEOMD_IMAP_TRACE=1`** appends `<time> <op> <ms>` per IMAP operation to
  `~/.cache/neomd/imap-trace.log` (`imap.SetTracePath`, `config.IMAPTracePath`). First
  stop for any "neomd feels slow" report; the number of lines per keypress is the
  round-trip count. Tests: `TestTrace_*`, `TestIMAPTracePath_NextToMovesLog`.

## Notifications & Theming

- **Desktop notifications are VIP-only and TUI-only** — fire solely for senders/domains in
  `notify.txt` (independent of screener categories); the headless daemon never notifies;
  first run records a UID baseline (state key uses the IMAP folder name, not the UI label)
  so enabling never floods; `[notifications].folders` allowlist matches via `LabelFor()`.
  Tests: `TestMaybeNotify_*`, `TestShouldNotify`.
- **Default theme never drifts** — `kanagawa` must stay byte-for-byte identical to the
  pre-theming palette; `[theme]` overrides merge on top of any built-in. Tests:
  `TestKanagawaDefault`, theme override/fallback tests.

## Config & Credentials

- **Config validation on load** — host:port format, port 1–65535, required fields;
  `$VAR`/`${VAR}` expansion in `user`/`password`. Tests: `TestValidate*`, `TestExpandEnv`.
- **`password = "keyring"` sentinel** — resolved in `config.Load()` so IMAP, SMTP, and
  `[[senders]]` aliases all see it; preserved with a warning if the keyring is unavailable.
  Test: `TestUseKeyring`.
- **Secrets never leak** — token files/dirs written with restrictive permissions; error
  messages never include tokens/passwords. Tests: `TestTokenErrors_NoTokenLeak`,
  `TestSaveToken_FilePermissions`.

## Keybindings & Docs

- **`internal/ui/keys.go` is the single source of truth** — drives the `?` overlay and the
  generated `docs/content/docs/keybindings.md` (`make docs`, runs in `make build`). Never hand-edit the
  markdown tables.
- **Avoid modifier keys for new bindings** — user's tmux prefix is `C-t`; `ctrl+a`/`ctrl+e`
  collide with textinput line-start/end. Prefer plain letters, especially on pre-send.
- **README.md syncs to the docs site** (`scripts/sync-readme-to-docs.sh` via `make docs`).
