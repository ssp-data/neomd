// Package imap provides a minimal IMAP client for neomd.
// Adapted from github.com/wesm/msgvault/internal/imap/client.go.
package imap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	htmlmd "github.com/JohannesKaufmann/html-to-markdown"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
)

// Email is a fully parsed email message.
// Attachment holds a decoded email attachment (file or inline binary part).
type Attachment struct {
	Filename    string // from Content-Disposition filename or Content-Type name param
	ContentType string // e.g. "application/pdf"
	Data        []byte
}

type Email struct {
	UID           uint32
	From          string
	To            string
	CC            string // comma-separated CC addresses (may be empty)
	ReplyTo       string // Reply-To address if present (may be empty)
	Subject       string
	Date          time.Time
	Seen          bool
	Folder        string
	Size          uint32 // RFC822 size in bytes
	HasAttachment bool   // true if BODYSTRUCTURE contains an attachment part
	MessageID     string // Message-ID from envelope (for threading)
	InReplyTo     string // first In-Reply-To message ID (for threading)
}

// Config holds connection parameters.
type Config struct {
	Host     string // e.g. "imap.example.com"
	Port     string // e.g. "993" or "143"
	User     string
	Password string
	TLS      bool // implicit TLS (port 993)
	STARTTLS bool // STARTTLS upgrade (port 143)
}

// Client wraps an IMAP connection with reconnection management.
type Client struct {
	cfg    Config
	logger *slog.Logger

	mu              sync.Mutex
	conn            *imapclient.Client
	selectedMailbox string
}

// New creates a new IMAP client (does not connect yet).
func New(cfg Config) *Client {
	return &Client{cfg: cfg, logger: slog.Default()}
}

func (c *Client) addr() string {
	return c.cfg.Host + ":" + c.cfg.Port
}

// connect establishes and authenticates the connection. Caller must hold mu.
func (c *Client) connect(_ context.Context) error {
	if c.conn != nil {
		return nil
	}
	addr := c.addr()
	opts := &imapclient.Options{}
	var (
		conn *imapclient.Client
		err  error
	)
	switch {
	case c.cfg.TLS:
		conn, err = imapclient.DialTLS(addr, opts)
	case c.cfg.STARTTLS:
		conn, err = imapclient.DialStartTLS(addr, opts)
	default:
		return fmt.Errorf("refusing unencrypted connection to %s — use port 993 (TLS) or 143 (STARTTLS)", addr)
	}
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	if err := conn.Login(c.cfg.User, c.cfg.Password).Wait(); err != nil {
		_ = conn.Close()
		return fmt.Errorf("IMAP login: %w", err)
	}
	c.conn = conn
	c.selectedMailbox = ""
	return nil
}

func (c *Client) reconnect(ctx context.Context) error {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.selectedMailbox = ""
	return c.connect(ctx)
}

func (c *Client) withConn(ctx context.Context, fn func(*imapclient.Client) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.connect(ctx); err != nil {
		return err
	}
	if err := fn(c.conn); err != nil {
		if isNetErr(err) {
			_ = c.conn.Close()
			c.conn = nil
			c.selectedMailbox = ""
			// Retry once after reconnect for transient network failures.
			if reconnErr := c.connect(ctx); reconnErr != nil {
				return err // return original error
			}
			if retryErr := fn(c.conn); retryErr != nil {
				if isNetErr(retryErr) {
					_ = c.conn.Close()
					c.conn = nil
					c.selectedMailbox = ""
				}
				return retryErr
			}
			return nil // retry succeeded
		}
		return err
	}
	return nil
}

func (c *Client) selectMailbox(mailbox string) error {
	if c.selectedMailbox == mailbox {
		return nil
	}
	if _, err := c.conn.Select(mailbox, nil).Wait(); err != nil {
		return fmt.Errorf("SELECT %q: %w", mailbox, err)
	}
	c.selectedMailbox = mailbox
	return nil
}

// Close logs out and closes the IMAP connection.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Logout().Wait()
		_ = c.conn.Close()
		c.conn = nil
	}
}

// Addr returns the IMAP server address (host:port).
func (c *Client) Addr() string { return c.addr() }

// User returns the IMAP username.
func (c *Client) User() string { return c.cfg.User }

// Ping tests the IMAP connection by issuing a NOOP command.
func (c *Client) Ping(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.withConn(ctx, func(conn *imapclient.Client) error {
		return conn.Noop().Wait()
	})
}

// FetchHeaders fetches the latest n message summaries from folder.
func (c *Client) FetchHeaders(ctx context.Context, folder string, n int) ([]Email, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var emails []Email
	err := c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(folder); err != nil {
			return err
		}

		searchData, err := conn.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
		if err != nil {
			return fmt.Errorf("UID SEARCH: %w", err)
		}

		uidSet, ok := searchData.All.(imap.UIDSet)
		if !ok {
			return nil
		}
		allUIDs, _ := uidSet.Nums()
		if len(allUIDs) == 0 {
			return nil
		}

		// Take the last n UIDs (most recent) and reverse to newest-first.
		// n=0 means no limit — fetch all.
		sort.Slice(allUIDs, func(i, j int) bool { return allUIDs[i] < allUIDs[j] })
		if n > 0 && len(allUIDs) > n {
			allUIDs = allUIDs[len(allUIDs)-n:]
		}
		for i, j := 0, len(allUIDs)-1; i < j; i, j = i+1, j-1 {
			allUIDs[i], allUIDs[j] = allUIDs[j], allUIDs[i]
		}

		var fetchSet imap.UIDSet
		for _, uid := range allUIDs {
			fetchSet.AddNum(uid)
		}

		msgs, err := conn.Fetch(fetchSet, &imap.FetchOptions{
			UID:           true,
			Flags:         true,
			Envelope:      true,
			RFC822Size:    true,
			BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		}).Collect()
		if err != nil {
			return fmt.Errorf("FETCH headers: %w", err)
		}

		byUID := make(map[imap.UID]*imapclient.FetchMessageBuffer, len(msgs))
		for _, m := range msgs {
			byUID[m.UID] = m
		}

		for _, uid := range allUIDs {
			m, ok := byUID[uid]
			if !ok {
				continue
			}
			e := Email{UID: uint32(m.UID), Folder: folder}
			for _, f := range m.Flags {
				if f == imap.FlagSeen {
					e.Seen = true
				}
			}
			if m.Envelope != nil {
				e.Subject = m.Envelope.Subject
				e.Date = m.Envelope.Date
				if len(m.Envelope.From) > 0 {
					a := m.Envelope.From[0]
					if a.Name != "" {
						e.From = a.Name + " <" + a.Addr() + ">"
					} else {
						e.From = a.Addr()
					}
				}
				if len(m.Envelope.To) > 0 {
					e.To = m.Envelope.To[0].Addr()
				}
				if len(m.Envelope.Cc) > 0 {
					cc := make([]string, 0, len(m.Envelope.Cc))
					for _, a := range m.Envelope.Cc {
						cc = append(cc, a.Addr())
					}
					e.CC = strings.Join(cc, ", ")
				}
				if len(m.Envelope.ReplyTo) > 0 {
					e.ReplyTo = m.Envelope.ReplyTo[0].Addr()
				}
				e.MessageID = m.Envelope.MessageID
				if len(m.Envelope.InReplyTo) > 0 {
					e.InReplyTo = m.Envelope.InReplyTo[0]
				}
			}
			e.Size = uint32(m.RFC822Size)
			e.HasAttachment = hasAttachment(m.BodyStructure)
			emails = append(emails, e)
		}
		return nil
	})
	return emails, err
}

// SearchUIDs returns all UIDs in folder without fetching any headers.
// Very fast — a single IMAP UID SEARCH ALL command regardless of mailbox size.
func (c *Client) SearchUIDs(ctx context.Context, folder string) ([]uint32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var uids []uint32
	err := c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(folder); err != nil {
			return err
		}
		searchData, err := conn.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
		if err != nil {
			return fmt.Errorf("UID SEARCH: %w", err)
		}
		uidSet, ok := searchData.All.(imap.UIDSet)
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

// FetchUnseenCounts returns the number of unseen messages for each folder using
// IMAP STATUS — fast and does not SELECT/open the mailbox.
// folders maps a display label (e.g. "Inbox") to an IMAP mailbox name.
// Missing or inaccessible mailboxes are skipped silently.
func (c *Client) FetchUnseenCounts(ctx context.Context, folders map[string]string) (map[string]int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	counts := make(map[string]int, len(folders))
	err := c.withConn(ctx, func(conn *imapclient.Client) error {
		for label, mailbox := range folders {
			data, err := conn.Status(mailbox, &imap.StatusOptions{NumUnseen: true}).Wait()
			if err != nil {
				continue // folder may not exist; skip
			}
			if data.NumUnseen != nil {
				counts[label] = int(*data.NumUnseen)
			}
		}
		return nil
	})
	return counts, err
}

// hasAttachment reports whether a BODYSTRUCTURE tree contains at least one
// part with Content-Disposition: attachment. Extended bodystructure must have
// been requested (FetchItemBodyStructure{Extended: true}) for Disposition to
// be populated; returns false if bs is nil.
func hasAttachment(bs imap.BodyStructure) bool {
	if bs == nil {
		return false
	}
	switch v := bs.(type) {
	case *imap.BodyStructureSinglePart:
		d := v.Disposition() // method call; returns nil if Extended is nil
		if d != nil && strings.EqualFold(d.Value, "attachment") {
			return true
		}
		// Inline images (Content-Disposition: inline or missing) also count
		return strings.EqualFold(v.Type, "image")
	case *imap.BodyStructureMultiPart:
		for _, child := range v.Children {
			if hasAttachment(child) {
				return true
			}
		}
	}
	return false
}

// SearchMessages searches a folder using IMAP SEARCH and returns matching emails
// with headers fetched. The query is matched against From, Subject, and To headers.
// Supports prefixes: "from:x", "subject:x", "to:x". Plain text searches all three.
// Searches ALL messages on the server, not just loaded ones.
func (c *Client) SearchMessages(ctx context.Context, folder, query string) ([]Email, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if query == "" {
		return nil, nil
	}

	criteria := buildSearchCriteria(query)

	var uids []uint32
	err := c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(folder); err != nil {
			return err
		}

		searchData, err := conn.UIDSearch(criteria, nil).Wait()
		if err != nil {
			return fmt.Errorf("UID SEARCH: %w", err)
		}
		uidSet, ok := searchData.All.(imap.UIDSet)
		if !ok {
			return nil
		}
		nums, _ := uidSet.Nums()
		for _, u := range nums {
			uids = append(uids, uint32(u))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(uids) == 0 {
		return nil, nil
	}

	// Cap results per folder to avoid huge fetches
	if len(uids) > 100 {
		uids = uids[len(uids)-100:] // keep newest (highest UIDs)
	}

	return c.FetchHeadersByUID(ctx, folder, uids)
}

// SearchAllFolders searches across multiple folders and returns combined results.
// Each folder is SELECTed and searched individually (IMAP limitation).
func (c *Client) SearchAllFolders(ctx context.Context, folders []string, query string) ([]Email, error) {
	if query == "" {
		return nil, nil
	}
	var all []Email
	for _, folder := range folders {
		emails, err := c.SearchMessages(ctx, folder, query)
		if err != nil {
			// Skip folders that fail (e.g. don't exist on this server)
			continue
		}
		all = append(all, emails...)
	}
	return all, nil
}

// buildSearchCriteria parses a query string into IMAP SearchCriteria.
// Supports prefixes: "from:value", "subject:value", "to:value".
// Plain text without a prefix searches OR(FROM, SUBJECT, TO).
func buildSearchCriteria(query string) *imap.SearchCriteria {
	q := strings.TrimSpace(query)
	lower := strings.ToLower(q)

	switch {
	case strings.HasPrefix(lower, "from:"):
		val := strings.TrimSpace(q[5:])
		return &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: val}},
		}
	case strings.HasPrefix(lower, "subject:"):
		val := strings.TrimSpace(q[8:])
		return &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: val}},
		}
	case strings.HasPrefix(lower, "to:"):
		val := strings.TrimSpace(q[3:])
		return &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{{Key: "To", Value: val}},
		}
	default:
		// Plain text: OR(FROM q, OR(SUBJECT q, TO q))
		return &imap.SearchCriteria{
			Or: [][2]imap.SearchCriteria{
				{
					{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: q}}},
					{Or: [][2]imap.SearchCriteria{
						{
							{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: q}}},
							{Header: []imap.SearchCriteriaHeaderField{{Key: "To", Value: q}}},
						},
					}},
				},
			},
		}
	}
}

// FetchLatest fetches the N most recent emails (by UID, descending) from a folder.
// Uses UID SEARCH ALL to get all UIDs, takes the last N, and fetches headers.
func (c *Client) FetchLatest(ctx context.Context, folder string, n int) ([]Email, error) {
	uids, err := c.SearchUIDs(ctx, folder)
	if err != nil {
		return nil, err
	}
	if len(uids) == 0 {
		return nil, nil
	}
	// UIDs are ascending; take the last N (newest)
	if len(uids) > n {
		uids = uids[len(uids)-n:]
	}
	return c.FetchHeadersByUID(ctx, folder, uids)
}

// FetchLatestAllFolders fetches the N most recent emails across all given folders,
// sorted by date descending. Takes a few per folder proportionally.
func (c *Client) FetchLatestAllFolders(ctx context.Context, folders []string, total int) ([]Email, error) {
	perFolder := total / len(folders)
	if perFolder < 5 {
		perFolder = 5
	}
	var all []Email
	for _, folder := range folders {
		emails, err := c.FetchLatest(ctx, folder, perFolder)
		if err != nil {
			continue // skip folders that fail
		}
		all = append(all, emails...)
	}
	// Sort by date descending and cap
	sort.Slice(all, func(i, j int) bool {
		return all[i].Date.After(all[j].Date)
	})
	if len(all) > total {
		all = all[:total]
	}
	return all, nil
}

// FetchHeadersByUID fetches envelope headers for a specific slice of UIDs.
// Callers should pass small batches (≤200) to avoid oversized IMAP requests.
func (c *Client) FetchHeadersByUID(ctx context.Context, folder string, uids []uint32) ([]Email, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(uids) == 0 {
		return nil, nil
	}
	var emails []Email
	err := c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(folder); err != nil {
			return err
		}
		var fetchSet imap.UIDSet
		for _, uid := range uids {
			fetchSet.AddNum(imap.UID(uid))
		}
		msgs, err := conn.Fetch(fetchSet, &imap.FetchOptions{
			UID:      true,
			Flags:    true,
			Envelope: true,
		}).Collect()
		if err != nil {
			return fmt.Errorf("FETCH headers: %w", err)
		}
		for _, m := range msgs {
			e := Email{UID: uint32(m.UID), Folder: folder}
			for _, f := range m.Flags {
				if f == imap.FlagSeen {
					e.Seen = true
				}
			}
			if m.Envelope != nil {
				e.Subject = m.Envelope.Subject
				e.Date = m.Envelope.Date
				if len(m.Envelope.From) > 0 {
					a := m.Envelope.From[0]
					if a.Name != "" {
						e.From = a.Name + " <" + a.Addr() + ">"
					} else {
						e.From = a.Addr()
					}
				}
			}
			emails = append(emails, e)
		}
		return nil
	})
	return emails, err
}

// FetchBody fetches the body of a single message.
// Returns (markdownBody, rawHTML, webURL, attachments, error).
func (c *Client) FetchBody(ctx context.Context, folder string, uid uint32) (string, string, string, []Attachment, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var markdown, rawHTML, webURL string
	var attachments []Attachment
	err := c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(folder); err != nil {
			return err
		}

		var fetchSet imap.UIDSet
		fetchSet.AddNum(imap.UID(uid))

		msgs, err := conn.Fetch(fetchSet, &imap.FetchOptions{
			UID:         true,
			BodySection: []*imap.FetchItemBodySection{{Peek: true}},
		}).Collect()
		if err != nil {
			return fmt.Errorf("FETCH body uid=%d: %w", uid, err)
		}
		if len(msgs) == 0 {
			return fmt.Errorf("message uid=%d not found in %s", uid, folder)
		}

		if len(msgs[0].BodySection) > 0 {
			markdown, rawHTML, webURL, attachments = parseBody(msgs[0].BodySection[0].Bytes)
		}
		return nil
	})
	return markdown, rawHTML, webURL, attachments, err
}

// MoveMessage moves uid from src to dst using the IMAP MOVE command (RFC 6851).
// Returns the UID assigned at the destination (may differ from src UID on some
// servers). Falls back to the original uid if the server does not report UIDPLUS
// COPYUID data. Callers that need the dest UID for undo should capture it.
func (c *Client) MoveMessage(ctx context.Context, src string, uid uint32, dst string) (destUID uint32, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	destUID = uid // default: assume same UID
	err = c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(src); err != nil {
			return err
		}
		var uidSet imap.UIDSet
		uidSet.AddNum(imap.UID(uid))
		data, moveErr := conn.Move(uidSet, dst).Wait()
		if moveErr != nil {
			var imapErr *imap.Error
			if errors.As(moveErr, &imapErr) && imapErr.Code == imap.ResponseCodeTryCreate {
				if cerr := conn.Create(dst, nil).Wait(); cerr != nil {
					return fmt.Errorf("CREATE %s: %w", dst, cerr)
				}
				_ = conn.Subscribe(dst).Wait()
				c.selectedMailbox = ""
				if err := c.selectMailbox(src); err != nil {
					return err
				}
				var err2 error
				data, err2 = conn.Move(uidSet, dst).Wait()
				if err2 != nil {
					return fmt.Errorf("MOVE %d → %s (after CREATE): %w", uid, dst, err2)
				}
			} else {
				return fmt.Errorf("MOVE %d → %s: %w", uid, dst, moveErr)
			}
		}
		// Extract destination UID from UIDPLUS COPYUID response when available.
		if data != nil && data.DestUIDs != nil {
			if us, ok := data.DestUIDs.(imap.UIDSet); ok {
				if nums, ok2 := us.Nums(); ok2 && len(nums) > 0 {
					destUID = uint32(nums[0])
				}
			}
		}
		c.selectedMailbox = ""
		return nil
	})
	return destUID, err
}

// EnsureFolders creates and subscribes any folders in the list that do not
// yet exist on the server. Already-existing folders are silently skipped.
// Returns the names of folders that were actually created.
func (c *Client) EnsureFolders(ctx context.Context, folders []string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var created []string
	err := c.withConn(ctx, func(conn *imapclient.Client) error {
		for _, folder := range folders {
			if folder == "" {
				continue
			}
			err := conn.Create(folder, nil).Wait()
			if err != nil {
				var imapErr *imap.Error
				if errors.As(err, &imapErr) && imapErr.Code == imap.ResponseCodeAlreadyExists {
					continue // already there, nothing to do
				}
				return fmt.Errorf("CREATE %s: %w", folder, err)
			}
			_ = conn.Subscribe(folder).Wait()
			created = append(created, folder)
		}
		return nil
	})
	return created, err
}

// ExpungeAll permanently deletes every message in folder by marking all
// messages \Deleted then issuing UID EXPUNGE.
func (c *Client) ExpungeAll(ctx context.Context, folder string, uids []uint32) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(uids) == 0 {
		return nil
	}
	return c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(folder); err != nil {
			return err
		}
		var uidSet imap.UIDSet
		for _, uid := range uids {
			uidSet.AddNum(imap.UID(uid))
		}
		// Mark all \Deleted (silent — no need to read the response)
		if err := conn.Store(uidSet, &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Silent: true,
			Flags:  []imap.Flag{imap.FlagDeleted},
		}, nil).Close(); err != nil {
			return fmt.Errorf("STORE \\Deleted: %w", err)
		}
		// Expunge only the UIDs we just marked (safe for concurrent clients)
		if _, err := conn.UIDExpunge(uidSet).Collect(); err != nil {
			return fmt.Errorf("UID EXPUNGE: %w", err)
		}
		c.selectedMailbox = ""
		return nil
	})
}

// MarkSeen marks a message as \Seen.
func (c *Client) MarkSeen(ctx context.Context, folder string, uid uint32) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(folder); err != nil {
			return err
		}
		var uidSet imap.UIDSet
		uidSet.AddNum(imap.UID(uid))
		return conn.Store(uidSet, &imap.StoreFlags{
			Op:    imap.StoreFlagsAdd,
			Flags: []imap.Flag{imap.FlagSeen},
		}, nil).Close()
	})
}

// MarkUnseen removes the \Seen flag, marking a message as unread.
func (c *Client) MarkUnseen(ctx context.Context, folder string, uid uint32) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(folder); err != nil {
			return err
		}
		var uidSet imap.UIDSet
		uidSet.AddNum(imap.UID(uid))
		return conn.Store(uidSet, &imap.StoreFlags{
			Op:    imap.StoreFlagsDel,
			Flags: []imap.Flag{imap.FlagSeen},
		}, nil).Close()
	})
}

// SaveSent APPENDs a raw MIME message to the given folder with the \Seen flag.
// Call this after a successful SMTP send to keep a copy in the Sent mailbox.
func (c *Client) SaveSent(ctx context.Context, folder string, raw []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.withConn(ctx, func(conn *imapclient.Client) error {
		opts := &imap.AppendOptions{
			Flags: []imap.Flag{imap.FlagSeen},
			Time:  time.Now(),
		}
		cmd := conn.Append(folder, int64(len(raw)), opts)
		if _, err := cmd.Write(raw); err != nil {
			return fmt.Errorf("APPEND write: %w", err)
		}
		if err := cmd.Close(); err != nil {
			return fmt.Errorf("APPEND close: %w", err)
		}
		if _, err := cmd.Wait(); err != nil {
			return fmt.Errorf("APPEND wait: %w", err)
		}
		return nil
	})
}

// SaveDraft APPENDs a raw MIME message to the given folder with the \Draft flag.
// The folder must already exist (use EnsureFolders if needed).
func (c *Client) SaveDraft(ctx context.Context, folder string, raw []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.withConn(ctx, func(conn *imapclient.Client) error {
		opts := &imap.AppendOptions{
			Flags: []imap.Flag{imap.FlagDraft, imap.FlagSeen},
			Time:  time.Now(),
		}
		cmd := conn.Append(folder, int64(len(raw)), opts)
		if _, err := cmd.Write(raw); err != nil {
			return fmt.Errorf("APPEND write: %w", err)
		}
		if err := cmd.Close(); err != nil {
			return fmt.Errorf("APPEND close: %w", err)
		}
		if _, err := cmd.Wait(); err != nil {
			return fmt.Errorf("APPEND wait: %w", err)
		}
		return nil
	})
}

// parseBody extracts the best available content from a raw MIME message.
// Returns (markdownBody, rawHTML, webURL).
//   - markdownBody: what the TUI renders (HTML→markdown or normalised plain text)
//   - rawHTML:      original HTML part verbatim (empty for plain-text emails)
//   - webURL:       "view online" URL extracted from List-Post header or plain-text
//     preamble (e.g. Substack's "View this post on the web at https://…")
func parseBody(raw []byte) (markdown, rawHTML, webURL string, attachments []Attachment) {
	e, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return string(raw), "", "", nil
	}

	// List-Post header contains the canonical article URL on most newsletters:
	//   List-Post: <https://newsletter.example.com/p/slug>
	if lp := e.Header.Get("List-Post"); lp != "" {
		lp = strings.TrimSpace(lp)
		if strings.HasPrefix(lp, "<") && strings.HasSuffix(lp, ">") {
			u := lp[1 : len(lp)-1]
			if strings.HasPrefix(u, "http") {
				webURL = u
			}
		}
	}

	mr := mail.NewReader(e)
	var plainText, htmlText string
	cidToName := make(map[string]string) // Content-ID → filename for inline images

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if !message.IsUnknownCharset(err) {
				break
			}
			if p == nil {
				continue
			}
		}

		var ct string
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			var params map[string]string
			ct, params, _ = h.ContentType()
			if ct != "text/plain" && ct != "text/html" {
				// Inline binary part (e.g. image/png) — treat as download
				filename := params["name"]
				if filename == "" {
					if parts := strings.SplitN(ct, "/", 2); len(parts) == 2 {
						filename = "attachment." + parts[1]
					} else {
						filename = "attachment.bin"
					}
				}
				// Map Content-ID → filename so we can inject alt text into the HTML
				if cid := strings.Trim(h.Get("Content-ID"), "<>"); cid != "" {
					cidToName[cid] = filename
				}
				data, _ := io.ReadAll(p.Body)
				attachments = append(attachments, Attachment{
					Filename:    filename,
					ContentType: ct,
					Data:        data,
				})
				continue
			}
		case *mail.AttachmentHeader:
			ct, _, _ = h.ContentType()
			filename, _ := h.Filename()
			data, _ := io.ReadAll(p.Body)
			if filename != "" {
				attachments = append(attachments, Attachment{
					Filename:    filename,
					ContentType: ct,
					Data:        data,
				})
			}
			continue
		}

		data, _ := io.ReadAll(p.Body)
		switch ct {
		case "text/plain":
			if plainText == "" {
				plainText = string(data)
			}
		case "text/html":
			if htmlText == "" {
				htmlText = string(data)
			}
		}
	}

	// Inject alt attributes for CID images so cleanMarkdown keeps them as
	// [Image: filename.png] placeholders instead of stripping them as empty.
	if htmlText != "" && len(cidToName) > 0 {
		htmlText = injectCIDAlt(htmlText, cidToName)
	}

	// If List-Post didn't give us a URL, try the plain-text preamble.
	// Substack and some others open with "View this post on the web at https://…"
	if webURL == "" && plainText != "" {
		webURL = extractPlainTextWebURL(plainText)
	}

	// Prefer HTML: newsletters and modern emails have rich HTML while the
	// text/plain part is typically a stripped dump with raw redirect URLs.
	// Fall back to plain text for plain-text-only emails (e.g. direct replies).
	if htmlText != "" {
		return htmlToMarkdown(htmlText), htmlText, webURL, attachments
	}
	if plainText != "" {
		return normalizePlainText(plainText), "", webURL, attachments
	}
	return "(no body)", "", webURL, attachments
}

// extractPlainTextWebURL looks for a "View … on the web at https://…" line
// in the first few hundred bytes of a plain-text email body.
func extractPlainTextWebURL(text string) string {
	top := text
	if len(top) > 500 {
		top = top[:500]
	}
	re := regexp.MustCompile(`(?i)(?:view|read)[^\n]{0,40}(?:web|browser|online)[^\n]{0,20}(?:at\s+)?(https://\S+)`)
	if m := re.FindStringSubmatch(top); m != nil {
		return strings.TrimRight(m[1], ".,;)")
	}
	return ""
}

// cidImgRe matches <img ...src="cid:XYZ"...> tags (with or without alt).
var cidImgRe = regexp.MustCompile(`(?i)<img\b([^>]*?)src="cid:([^"]+)"([^>]*?)>`)

// emptyAltRe matches alt="" or alt='' (empty alt attribute).
var emptyAltRe = regexp.MustCompile(`(?i)\s*alt=["']\s*["']`)

// injectCIDAlt adds alt="filename" to <img src="cid:..."> tags that lack an alt
// attribute or have an empty alt="", so that html-to-markdown produces
// ![filename](cid:...) instead of ![](cid:...) which cleanMarkdown would strip.
func injectCIDAlt(html string, cidToName map[string]string) string {
	return cidImgRe.ReplaceAllStringFunc(html, func(m string) string {
		parts := cidImgRe.FindStringSubmatch(m)
		if parts == nil {
			return m
		}
		cid := parts[2]
		name := "image"
		if n, ok := cidToName[cid]; ok {
			name = n
		}
		full := parts[0]
		attrs := parts[1] + parts[3]
		lAttrs := strings.ToLower(attrs)
		if strings.Contains(lAttrs, "alt=") {
			// Replace empty alt="" with alt="filename"
			if emptyAltRe.MatchString(full) {
				return emptyAltRe.ReplaceAllString(full, ` alt="`+name+`"`)
			}
			// Non-empty alt already present — keep it
			return m
		}
		// No alt at all — inject one
		before, after := parts[1], parts[3]
		return `<img` + before + `alt="` + name + `" src="cid:` + cid + `"` + after + `>`
	})
}

// htmlToMarkdown converts an HTML email body to Markdown so glamour can render
// it with proper formatting: bold, italic, links, headings, lists, and image
// placeholders (![alt](url) → [Image: alt] in the terminal).
func htmlToMarkdown(h string) string {
	// Remove <wbr> tags and join newlines inside href/src attribute values.
	// Newsletter services (Substack, Mailchimp) insert line breaks inside URLs
	// for HTML rendering; html-to-markdown preserves them, breaking link syntax.
	h = regexp.MustCompile(`<wbr\s*/?>|&ZeroWidthSpace;`).ReplaceAllString(h, "")
	// Collapse whitespace (including newlines) inside href="..." and src="..."
	reAttr := regexp.MustCompile(`(?s)((?:href|src)=")(.*?)(")`)
	h = reAttr.ReplaceAllStringFunc(h, func(m string) string {
		parts := reAttr.FindStringSubmatch(m)
		if len(parts) != 4 {
			return m
		}
		clean := regexp.MustCompile(`\s+`).ReplaceAllString(parts[2], "")
		return parts[1] + clean + parts[3]
	})

	converter := htmlmd.NewConverter("", true, nil)
	result, err := converter.ConvertString(h)
	if err != nil {
		return stripHTMLFallback(h)
	}
	return cleanMarkdown(strings.TrimSpace(result))
}

// cleanMarkdown post-processes html-to-markdown output to remove newsletter
// noise: invisible Unicode spacers, tracking pixels, bare URL lines, and
// excessive blank lines.
func cleanMarkdown(s string) string {
	// 1. Strip invisible Unicode characters used as email preheader spacers:
	//    U+034F COMBINING GRAPHEME JOINER, U+00AD SOFT HYPHEN,
	//    U+200B ZERO WIDTH SPACE, U+200C/D ZWNJ/ZWJ, U+FEFF BOM
	reInvis := regexp.MustCompile(`[\x{034F}\x{00AD}\x{200B}\x{200C}\x{200D}\x{FEFF}]+`)
	s = reInvis.ReplaceAllString(s, "")

	// 2. Remove empty image tags (tracking pixels): ![](...) or ![  ](...)
	reEmptyImg := regexp.MustCompile(`!\[\s*\]\([^)]*\)`)
	s = reEmptyImg.ReplaceAllString(s, "")

	// 3. Remove empty link anchors left behind when image-only links are cleaned:
	//    [](url) or [  ](url)
	reEmptyLink := regexp.MustCompile(`\[\s*\]\([^)]*\)`)
	s = reEmptyLink.ReplaceAllString(s, "")

	// 4. Remove lines that are only a bare URL (no surrounding text).
	//    These come from <a href="url"><img/></a> after the image is stripped,
	//    or from Substack's share/subscribe buttons whose text was an image.
	reBareURL := regexp.MustCompile(`(?m)^https?://\S+$`)
	s = reBareURL.ReplaceAllString(s, "")

	// 5. Remove lines that are only whitespace — including U+00A0 (&nbsp;) which
	//    &nbsp; gets decoded to and is NOT matched by \s in Go's regexp.
	reWhitespaceOnly := regexp.MustCompile("(?m)^[ \t\u00A0\u202F\u2003\u2009]+$")
	s = reWhitespaceOnly.ReplaceAllString(s, "")

	// 6. Collapse 3+ consecutive blank lines to 2
	reExcessBlank := regexp.MustCompile(`\n{4,}`)
	s = reExcessBlank.ReplaceAllString(s, "\n\n\n")

	return strings.TrimSpace(s)
}

// normalizePlainText prepares a plain-text email body for glamour rendering.
// Glamour treats single \n as paragraph continuation (Markdown spec), so bare
// line breaks in plain-text emails collapse into run-on text. We add two
// trailing spaces before each single newline, which Markdown treats as a hard
// line break, preserving the original layout.
func normalizePlainText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	// Add hard-break markers before single newlines (not before blank lines).
	var b strings.Builder
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		b.WriteString(line)
		if i < len(lines)-1 {
			next := lines[i+1]
			if next == "" {
				// Blank line: keep as paragraph separator (no trailing spaces needed)
				b.WriteByte('\n')
			} else {
				// Single newline: add trailing double-space for Markdown hard break
				b.WriteString("  \n")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// stripHTMLFallback is the last-resort plain-text extractor when the
// html-to-markdown converter fails.
func stripHTMLFallback(h string) string {
	reBlock := regexp.MustCompile(`(?is)<(style|script)[^>]*>.*?</(style|script)>`)
	h = reBlock.ReplaceAllString(h, "")
	reNewline := regexp.MustCompile(`(?i)</(p|div|br|li|tr|h[1-6]|blockquote)>`)
	h = reNewline.ReplaceAllString(h, "\n")
	reTags := regexp.MustCompile(`<[^>]+>`)
	h = reTags.ReplaceAllString(h, "")
	lines := strings.Split(h, "\n")
	var out []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isNetErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "EOF")
}
