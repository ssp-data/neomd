// Package imap provides a minimal IMAP client for neomd.
// Adapted from github.com/wesm/msgvault/internal/imap/client.go.
package imap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
)

// Email is a fully parsed email message.
type Email struct {
	UID     uint32
	From    string
	To      string
	Subject string
	Date    time.Time
	Seen    bool
	Folder  string
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
		conn, err = imapclient.DialInsecure(addr, opts)
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
		sort.Slice(allUIDs, func(i, j int) bool { return allUIDs[i] < allUIDs[j] })
		if len(allUIDs) > n {
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
			UID:      true,
			Flags:    true,
			Envelope: true,
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
			}
			emails = append(emails, e)
		}
		return nil
	})
	return emails, err
}

// FetchBody fetches and returns the plain-text body of a single message.
func (c *Client) FetchBody(ctx context.Context, folder string, uid uint32) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var body string
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
			body = parsePlainText(msgs[0].BodySection[0].Bytes)
		}
		return nil
	})
	return body, err
}

// MoveMessage copies uid from src to dst, then deletes it from src.
func (c *Client) MoveMessage(ctx context.Context, src string, uid uint32, dst string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.withConn(ctx, func(conn *imapclient.Client) error {
		if err := c.selectMailbox(src); err != nil {
			return err
		}

		var uidSet imap.UIDSet
		uidSet.AddNum(imap.UID(uid))

		if _, err := conn.Copy(uidSet, dst).Wait(); err != nil {
			return fmt.Errorf("COPY to %s: %w", dst, err)
		}

		if err := conn.Store(uidSet, &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Silent: true,
			Flags:  []imap.Flag{imap.FlagDeleted},
		}, nil).Close(); err != nil {
			return fmt.Errorf("STORE \\Deleted: %w", err)
		}

		if err := conn.Expunge().Close(); err != nil {
			return fmt.Errorf("EXPUNGE: %w", err)
		}
		c.selectedMailbox = "" // state changed after EXPUNGE
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

// parsePlainText extracts the best available plain text from a raw MIME message.
func parsePlainText(raw []byte) string {
	e, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return string(raw)
	}

	mr := mail.NewReader(e)
	var plainText, htmlText string

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
			ct, _, _ = h.ContentType()
		case *mail.AttachmentHeader:
			ct, _, _ = h.ContentType()
		}

		body, _ := io.ReadAll(p.Body)
		switch ct {
		case "text/plain":
			if plainText == "" {
				plainText = string(body)
			}
		case "text/html":
			if htmlText == "" {
				htmlText = string(body)
			}
		}
	}

	if plainText != "" {
		return plainText
	}
	if htmlText != "" {
		return stripHTML(htmlText)
	}
	return "(no body)"
}

// stripHTML removes HTML tags, leaving readable plain text.
func stripHTML(h string) string {
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
