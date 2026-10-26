// Package source reads email from a provider and yields provider-neutral messages.
// Every connector is read-only: it never marks, moves, deletes or sends mail.
package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"html"
	"io"
	"mime"
	"regexp"
	"strings"
	"time"

	_ "github.com/emersion/go-message/charset" // decode non-UTF-8 mail
	"github.com/emersion/go-message/mail"

	"github.com/ayush3923/project-dzinga/services/ingest/internal/classify"
)

type Message struct {
	classify.Message
	ID      string // RFC 5322 Message-ID, or a stable hash if missing
	Account string
	Date    time.Time
}

var (
	reHref     = regexp.MustCompile(`(?i)href\s*=\s*["']([^"'#]+)["']`)
	rePlainURL = regexp.MustCompile(`https://[^\s<>"')\]]+`)
)

// links returns the https links in a body, HTML hrefs first, de-duplicated.
func links(htmlBody, text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		u = html.UnescapeString(strings.TrimRight(u, ".,;"))
		if strings.HasPrefix(u, "https://") && !seen[u] && len(out) < 50 {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, m := range reHref.FindAllStringSubmatch(htmlBody, -1) {
		add(m[1])
	}
	for _, u := range rePlainURL.FindAllString(text, -1) {
		add(u)
	}
	return out
}

// Source streams messages received since a date. Prefilter is checked on headers
// before a body is downloaded; it may be nil.
type Source interface {
	Fetch(ctx context.Context, since time.Time, prefilter func(fromAddr, subject string) bool, fn func(Message) error) error
}

var (
	reBlocks = regexp.MustCompile(`(?is)<(script|style|head)[^>]*>.*?</(script|style|head)>`)
	reBreaks = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/li|/h\d)[^>]*>`)
	reTags   = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpaces = regexp.MustCompile(`[ \t\r\f\v]+`)
	reLines  = regexp.MustCompile(`\n\s*\n+`)
)

func htmlToText(s string) string {
	s = reBlocks.ReplaceAllString(s, " ")
	s = reBreaks.ReplaceAllString(s, "\n")
	s = reTags.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = reSpaces.ReplaceAllString(s, " ")
	return strings.TrimSpace(reLines.ReplaceAllString(s, "\n"))
}

const maxPart = 512 << 10 // read at most 512 KiB of any body part

// Parse reads an RFC 5322 message, preferring text/plain and falling back to HTML.
func Parse(r io.Reader, account string) (Message, error) {
	mr, err := mail.CreateReader(r)
	if err != nil {
		return Message{}, err
	}
	defer mr.Close()
	h := mr.Header
	m := Message{Account: account}
	if from, err := h.AddressList("From"); err == nil && len(from) > 0 {
		m.FromName, m.FromAddr = from[0].Name, strings.ToLower(from[0].Address)
	}
	m.Subject, _ = h.Subject()
	m.Date, _ = h.Date()
	m.ID, _ = h.MessageID()

	var plain, htmlBody string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			break // keep whatever was readable; malformed MIME is common
		}
		ih, ok := p.Header.(*mail.InlineHeader)
		if !ok {
			continue // attachment: never read
		}
		ct, _, _ := ih.ContentType()
		b, _ := io.ReadAll(io.LimitReader(p.Body, maxPart))
		switch {
		case ct == "text/plain" && plain == "":
			plain = string(b)
		case ct == "text/html" && htmlBody == "":
			htmlBody = string(b)
		}
	}
	m.Text = strings.TrimSpace(plain)
	if m.Text == "" {
		m.Text = htmlToText(htmlBody)
	}
	m.Links = links(htmlBody, plain)
	if m.ID == "" {
		sum := sha256.Sum256([]byte(account + "|" + m.FromAddr + "|" + m.Subject + "|" + m.Date.UTC().Format(time.RFC3339)))
		m.ID = "sha256:" + hex.EncodeToString(sum[:16])
	}
	return m, nil
}

var wordDecoder = &mime.WordDecoder{}

// decodeHeader decodes RFC 2047 encoded words ("=?UTF-8?B?...?=") in envelope fields.
func decodeHeader(s string) string {
	if d, err := wordDecoder.DecodeHeader(s); err == nil {
		return d
	}
	return s
}
