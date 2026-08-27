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

- **`·` reply indicator** — after sending a reply, the original email gets the IMAP
  `\Answered` flag (`MarkAnswered` in `internal/imap/client.go`, called from `sendEmailCmd`
  in `internal/ui/model.go`) and the inbox shows `·` (or `·╰` inside a thread,
  `internal/ui/inbox.go`). The local flag also updates immediately on `sendDoneMsg` without
  a refetch. Tests: `TestSendDoneMsgUpdatesAnsweredFlag`, `TestReplyIndicatorWithThread`.
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

## Compose → Pre-send → Send Pipeline

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
- **HTML signature marker position** — a line-trim-exact `[html-signature]` is removed
  from plain text and replaces its first HTML occurrence in place (before reply history);
  duplicate markers never duplicate the signature, and malformed marker contexts fall
  back without leaking a marker or sentinel. Pinning test:
  `TestBuildMessage_HTMLSignatureMarkerPosition`.
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

## Screener (HEY-style)

- **Priority order** — spam > screened_out > feed > papertrail > screened_in; per-address
  entries always beat `@domain` entries. Tests: `TestClassify`, `TestClassifyForScreen`.
- **Reclassification is atomic** — classifying removes the address from ALL conflicting
  lists (snapshot/rollback on failure, both files and moved emails). Test:
  `TestCrossListCleanup_Reclassification`.
- **Empty lists pause screening** — TUI and headless daemon both skip auto-screening until
  the first sender is classified (prevents sweeping a fresh inbox to ToScreen). Test:
  `TestScreenInbox_EmptyScreenerLists`.
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
- **Undo (`u`)** — reverses the last move/delete using UIDPLUS destination UIDs captured
  on move; batch operations preserve partial-undo info on failure. Integration test:
  `TestIntegration_IMAPMoveAndUndo`.

- **Search matches contact names** — `internal/contacts` harvests `Name <addr>` pairs
  from loaded headers into `~/.cache/neomd/contacts`; the local `/` filter appends
  resolved names to its haystack, and server-side search (`space /`) expands a name
  query into per-address queries (never for `subject:`), deduped by folder+UID.
  Envelope To/CC/BCC keep display names (`formatEnvelopeAddr`) — names that would
  break comma-splitting fall back to the bare address. Tests:
  `TestFormatEnvelopeAddr`, `TestExpandSearchQueries`,
  `TestContactNamesForResolvesBareAddresses`.
- **The user's `[contacts]` file is read-only** — `contacts.MergeFile` only reads;
  neomd persists exclusively to its own cache (`config.ContactsCachePath()`), so the
  cache can be deleted anytime and rebuilds from harvesting + the file. The picker
  (`space c`, `internal/ui/contacts_picker.go`) copies via external clipboard tools
  and never mutates the store. Tests: `TestMergeFileGoogleCSVRealExport`,
  `TestContactsPickerFilterAndSelect`.

## Reading & Security

- **Spy pixels blocked** — two layers: curated denylist with attribution
  (`internal/imap/tracker_list.go`) + generic 1×1 heuristic; glamour never fetches remote
  resources; results cached in `~/.cache/neomd/spy_pixels` (`+key` spy / `-key` clean).
  Tests: `TestSpyPixelDetection`, `TestSpyPixelSpacersNotFlagged`.
- **Browser view (`O`) injects CSP** — `script-src 'none'; frame-src 'none';
  object-src 'none'`; remote images intentionally allowed there. Test:
  `TestIntegration_BrowserSanitization`.
- **Link opening whitelist** — only `http://`, `https://`, `mailto:` schemes. Test:
  `TestURLSchemeValidation`.
- **Attachment open safety** — executable extensions are saved but never auto-opened;
  magic-byte mismatch detection (`http.DetectContentType`) blocks disguised files;
  sender-supplied filenames are sanitized against path traversal (`..`, separators) in
  every write path (downloads, `.ics`, `cid:` temp files).
- **Timer-based mark-as-read** — opening an email marks `\Seen` only after
  `mark_as_read_after_secs` (default 7 s); quick peeks stay unread; reply/forward marks
  immediately.

## IMAP & Runtime Resilience

- **Retry policy** — `withConnRetry` (one retry) only for read-only ops (FETCH/SEARCH/
  STATUS); mutating ops (MOVE/APPEND/STORE) use `withConn`, never retried (duplicate-mail
  risk). NOOP health probe after 2+ min idle handles suspend/resume.
- **`safeGo` everywhere** — background goroutines must use `safeGo()` (panic → 
  `~/.cache/neomd/crash.log`), never bare `go func()`. Maps passed to goroutines are
  snapshotted on the main goroutine first (spy-pixel cache race, CHANGELOG 2026-05-08).
- **Nothing blocks the bubbletea Update loop** — notifications have a 2 s timeout, no DNS
  lookups in the send path, background sync never tight-loops on error. Test:
  `TestSend_TimeoutCannotBlockTUI`.
- **`imap_disabled = true` accounts produce nil clients by design** — every helper that
  resolves an IMAP client must skip nil entries (send, Sent-copy, `\Answered`, `:debug`,
  headless). Tests: `internal/ui/imap_client_helpers_test.go`.

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
  generated `docs/keybindings.md` (`make docs`, runs in `make build`). Never hand-edit the
  markdown tables.
- **Avoid modifier keys for new bindings** — user's tmux prefix is `C-t`; `ctrl+a`/`ctrl+e`
  collide with textinput line-start/end. Prefer plain letters, especially on pre-send.
- **README.md syncs to the docs site** (`scripts/sync-readme-to-docs.sh` via `make docs`).
