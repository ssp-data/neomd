package ui

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
)

// quotedBody returns the open email's markdown body prepared for quoting in a
// reply or forward: inline (cid:) images are written to disk and the
// references rewritten to those files, so the send pipeline re-embeds them
// under fresh Content-IDs instead of leaving dangling cid: references.
func (m Model) quotedBody() string {
	e := m.openEmail
	if e == nil || len(m.openAttachments) == 0 {
		return m.openBody
	}
	dir := filepath.Join(config.InlineImageDir(), fmt.Sprintf("%s-%d", safeFileComponent(e.Folder), e.UID))
	return materializeInlineImages(m.openBody, m.openAttachments, dir)
}

// materializeInlineImages writes every attachment with a Content-ID that the
// body references as "cid:<id>" into dir and rewrites both the markdown form
// "(cid:<id>)" and the HTML form `"cid:<id>"` to the absolute file path.
// Attachments the body does not reference are not written; cid references
// with no matching part are left untouched. Any I/O failure leaves the body
// as it was for that image.
func materializeInlineImages(body string, atts []imap.Attachment, dir string) string {
	created := false
	used := map[string]bool{}
	for _, a := range atts {
		if a.ContentID == "" || len(a.Data) == 0 {
			continue
		}
		ref := "cid:" + a.ContentID
		if !strings.Contains(body, ref) {
			continue
		}
		if !created {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return body
			}
			created = true
		}
		name := inlineFilename(a, used)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, a.Data, 0o600); err != nil {
			continue
		}
		body = strings.ReplaceAll(body, "("+ref+")", "("+path+")")
		body = strings.ReplaceAll(body, `"`+ref+`"`, `"`+path+`"`)
	}
	return body
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeFileComponent reduces s to a single safe path component.
func safeFileComponent(s string) string {
	s = unsafeFileChars.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._")
	if s == "" {
		return "x"
	}
	return s
}

// inlineFilename picks a safe, unique file name for an inline part: the
// attachment's own base name when it has one, else "image" plus an extension
// derived from the MIME type. Path separators and traversal are stripped.
func inlineFilename(a imap.Attachment, used map[string]bool) string {
	base := filepath.Base(strings.ReplaceAll(a.Filename, `\`, "/"))
	name := safeFileComponent(base)
	if name == "x" || name == "." || name == ".." {
		name = "image"
	}
	if filepath.Ext(name) == "" {
		ext := ".bin"
		if exts, _ := mime.ExtensionsByType(a.ContentType); len(exts) > 0 {
			ext = exts[0]
		}
		name += ext
	}
	if used[name] {
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		for i := 2; ; i++ {
			candidate := fmt.Sprintf("%s-%d%s", stem, i, filepath.Ext(name))
			if !used[candidate] {
				name = candidate
				break
			}
		}
	}
	used[name] = true
	return name
}
