package imap

import (
	"context"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// AddKeyword STOREs keyword onto the message (RFC 9051 §2.3 — flags without a
// backslash prefix are user keywords; servers that advertise PERMANENTFLAGS
// \* accept arbitrary ones). The keyword is normalized to lowercase: IMAP
// flags compare case-insensitively and some servers normalize the case on
// write, so one canonical spelling keeps the local mirror honest.
//
// Mutating op ⇒ withConn, never retried (a retry could re-add a keyword the
// user just removed).
func (c *Client) AddKeyword(ctx context.Context, folder string, uid uint32, keyword string) error {
	return c.storeKeyword(ctx, folder, uid, keyword, imap.StoreFlagsAdd)
}

// RemoveKeyword removes a user keyword from the message. Mutating op ⇒ withConn.
func (c *Client) RemoveKeyword(ctx context.Context, folder string, uid uint32, keyword string) error {
	return c.storeKeyword(ctx, folder, uid, keyword, imap.StoreFlagsDel)
}

func (c *Client) storeKeyword(ctx context.Context, folder string, uid uint32, keyword string, op imap.StoreFlagsOp) error {
	if ctx == nil {
		ctx = context.Background()
	}
	kw := strings.ToLower(strings.TrimSpace(keyword))
	defer trace(time.Now(), "StoreKeyword %s uid=%d keyword=%s add=%v", folder, uid, kw, op == imap.StoreFlagsAdd)
	return c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(folder); err != nil {
			return err
		}
		var uidSet imap.UIDSet
		uidSet.AddNum(imap.UID(uid))
		return conn.Store(uidSet, &imap.StoreFlags{
			Op:    op,
			Flags: []imap.Flag{imap.Flag(kw)},
		}, nil).Close()
	})
}
