package imap

import (
	"context"
	"fmt"
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
	return c.storeKeywordUIDs(ctx, folder, []uint32{uid}, keyword, op)
}

// storeKeywordUIDs is storeKeyword for a whole UID set — one STORE round
// trip for every message in uids.
func (c *Client) storeKeywordUIDs(ctx context.Context, folder string, uids []uint32, keyword string, op imap.StoreFlagsOp) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(uids) == 0 {
		return nil
	}
	kw := strings.ToLower(strings.TrimSpace(keyword))
	defer trace(time.Now(), "StoreKeyword %s uids=%d keyword=%s add=%v", folder, len(uids), kw, op == imap.StoreFlagsAdd)
	return c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(folder); err != nil {
			return err
		}
		var uidSet imap.UIDSet
		for _, uid := range uids {
			uidSet.AddNum(imap.UID(uid))
		}
		return conn.Store(uidSet, &imap.StoreFlags{
			Op:    op,
			Flags: []imap.Flag{imap.Flag(kw)},
		}, nil).Close()
	})
}

// SearchKeyword returns the UIDs in folder carrying keyword (UID SEARCH
// KEYWORD, RFC 9051 §6.4.4) — read-only, so retried once on network errors.
func (c *Client) SearchKeyword(ctx context.Context, folder, keyword string) ([]uint32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	kw := strings.ToLower(strings.TrimSpace(keyword))
	defer trace(time.Now(), "SearchKeyword %s keyword=%s", folder, kw)
	var uids []uint32
	err := c.withConnRetry(ctx, func(conn *imapclient.Client) error {
		uids = nil // reset on retry
		if err := c.selectMailbox(folder); err != nil {
			return err
		}
		data, err := conn.UIDSearch(&imap.SearchCriteria{Flag: []imap.Flag{imap.Flag(kw)}}, nil).Wait()
		if err != nil {
			return fmt.Errorf("UID SEARCH KEYWORD: %w", err)
		}
		uidSet, ok := data.All.(imap.UIDSet)
		if !ok {
			return nil
		}
		nums, _ := uidSet.Nums()
		for _, u := range nums {
			uids = append(uids, uint32(u))
		}
		return nil
	})
	return uids, err
}

// PurgeResult reports what PurgeKeyword did: how many messages lost the
// keyword, which folders could not be opened (missing on the server, or a
// SELECT/SEARCH error — the keyword may remain there) and the first STORE
// error, which aborts the purge.
type PurgeResult struct {
	Removed int
	Skipped []string
	Err     error
}

// PurgeKeyword removes keyword from every message in folders: one UID SEARCH
// KEYWORD plus (when it hits) one STORE -FLAGS per folder. Only that keyword
// is touched — no other flag, nothing in the message. A folder that cannot
// be opened or searched is skipped and reported, a failing STORE stops the
// purge (mutating op, never retried).
func (c *Client) PurgeKeyword(ctx context.Context, folders []string, keyword string) PurgeResult {
	var res PurgeResult
	for _, folder := range folders {
		if folder == "" {
			continue
		}
		uids, err := c.SearchKeyword(ctx, folder, keyword)
		if err != nil {
			res.Skipped = append(res.Skipped, folder)
			continue
		}
		if len(uids) == 0 {
			continue
		}
		if err := c.storeKeywordUIDs(ctx, folder, uids, keyword, imap.StoreFlagsDel); err != nil {
			res.Err = fmt.Errorf("%s: %w", folder, err)
			return res
		}
		res.Removed += len(uids)
	}
	return res
}
