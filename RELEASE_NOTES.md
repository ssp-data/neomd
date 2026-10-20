## What has changed?

neomd 0.10.3 adds private notes on emails. Press `<space>n` on any email to write a note for yourself in your editor; it shows next to the subject in the list and in the reader header, and it syncs over IMAP without touching the email itself. The tag picker also learns to delete a tag everywhere, and the notify chords in the reader move to `<space>b` to make room.

**Highlights**

- **Private notes with `<space>n`** — on the cursor email in the inbox or the open email in the reader, `<space>n` opens a popup with the note or "no note yet". `e` or `enter` edits it as a plain Markdown file in `$EDITOR` with no length limit, `d` deletes after a `y/n` prompt, and `<space>n` or `esc` closes. Saving an emptied file deletes the note.
- **Notes show in the list and the reader** — the first line of a note appears muted after the subject with a `✎` marker, taking at most half the subject's width so rows stay on one line. The reader header gets a `Note:` block with every line.
- **Synced over IMAP, the email is never modified** — each note is stored as a small sidecar message in the folder set by `[notes] folder` (default `neomd/Notes`, created on first save) that references the email's Message-ID. The email keeps its UID and flags, so no other client sees a change. A local copy under the config directory shows the note before the first fetch, notes refresh at startup and on every background sync, and a failed save keeps the note locally and says so. `[notes] enabled = false` turns the feature off.
- **Delete a tag everywhere with `D` in the picker** — in `<space>t`, `D` on a tag that no email carries removes it at once. A tag still in use asks `y/n`, then untags it on every email across the account's configured folders, touching only that keyword. Nothing changes locally until the server confirms; the status line reports how many emails lost the tag and names any folder that could not be searched.
- **Picker and reader chords adjusted** — in the tag picker, `enter` is now the only toggle and `<space>t` closes it, since `space` used to toggle too. In the reader, the notify-list chords move from `<space>n` and `<space>N` to `<space>b` and `<space>B`.

**Fixes**

- A tag that no email carried anymore stayed in the picker forever, and clearing a tag from many emails meant untagging each by hand. `D` in the picker handles both cases.

Notes and tags, with screenshots of the picker, the chips and the note popup, are documented at https://neomd.ssp.sh/docs/tags-and-notes/.
