# Gmail Configuration

Gmail uses special IMAP folder names prefixed with `[Gmail]/`. neomd works with Gmail, but requires adjusted folder mappings and comes with performance caveats.

## Folder Mapping

Gmail has built-in system folders that must be referenced by their IMAP names. Custom neomd folders (ToScreen, Feed, etc.) are created as regular labels.

```toml
[folders]
inbox        = "INBOX"
sent         = "[Gmail]/Sent Mail"
trash        = "[Gmail]/Trash"
drafts       = "[Gmail]/Drafts"
to_screen    = "ToScreen"
feed         = "Feed"
papertrail   = "PaperTrail"
screened_out = "ScreenedOut"
archive      = "Archive Neomd"
waiting      = "Waiting"
scheduled    = "Scheduled Neomd"
someday      = "Someday"
spam         = "[Gmail]/Spam"
tab_order    = ["inbox", "to_screen", "feed", "papertrail", "waiting", "someday", "scheduled", "sent", "archive", "screened_out", "trash"]
```

### Why "Archive Neomd" and "Scheduled Neomd"?

Gmail reserves `[Gmail]/All Mail` for its own archive (which includes all labeled emails, not just archived ones). Using it as neomd's archive would mix archived emails with everything else. `Archive Neomd` creates a clean, dedicated folder.

Similarly, Gmail doesn't expose its scheduled send feature via IMAP, so `[Gmail]/Scheduled` doesn't exist. `Scheduled Neomd` avoids Gmail creating it under `[IMAP]/Scheduled`.

### Gmail system folders reference

| Gmail folder | IMAP name |
|---|---|
| Inbox | `INBOX` |
| Sent Mail | `[Gmail]/Sent Mail` |
| Drafts | `[Gmail]/Drafts` |
| Trash | `[Gmail]/Trash` |
| Spam | `[Gmail]/Spam` |
| All Mail | `[Gmail]/All Mail` |
| Starred | `[Gmail]/Starred` |
| Important | `[Gmail]/Important` |

## Performance Warning

Gmail's IMAP is significantly slower than dedicated email providers. On a sustained session, each IMAP command takes ~180ms on Gmail vs ~12ms on providers like Hostpoint or Fastmail. This means every folder switch, email open, and move feels noticeably slower (~570ms vs ~33ms per folder switch).

See the [Benchmark section](../README.md#benchmark) in the README for detailed measurements.

For the best neomd experience, consider a dedicated email provider. If you still want to use Gmail, it works — just expect ~1 second per action instead of instant.

## App Password

Gmail requires an app-specific password for IMAP access:

1. Enable 2-Step Verification at https://myaccount.google.com/security
2. Generate an app password at https://myaccount.google.com/apppasswords
3. Use the generated password in your `config.toml` (not your Gmail password)
