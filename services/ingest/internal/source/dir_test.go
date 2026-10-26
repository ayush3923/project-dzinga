package source

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/ingest/internal/classify"
)

// End-to-end over the fictional demo mailbox: parse MIME → prefilter → classify.
func TestDemoMailbox(t *testing.T) {
	d := &Dir{Path: "../../testdata/demo", Account: "demo@example.com"}
	type got struct{ kind, company, role string }
	results := map[string]got{}
	seen := 0
	err := d.Fetch(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), classify.LooksRelevant,
		func(m Message) error {
			seen++
			if m.ID == "" || m.Date.IsZero() {
				t.Errorf("missing id/date: %+v", m)
			}
			if r, ok := classify.Classify(m.Message); ok {
				results[m.ID] = got{r.Kind, r.Company, r.Role}
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]got{
		"01-northwind-applied@demo.dzinga":   {"applied", "Northwind", "Senior Product Manager"},
		"02-northwind-interview@demo.dzinga": {"interview", "Northwind", "Senior Product Manager"},
		"03-contoso-applied@demo.dzinga":     {"applied", "Contoso", "Product Lead"},
		"04-fabrikam-applied@demo.dzinga":    {"applied", "Fabrikam", "Group Product Manager"},
		"05-fabrikam-rejected@demo.dzinga":   {"rejected", "Fabrikam", "Group Product Manager"},
		"06-lumen-assessment@demo.dzinga":    {"assessment", "Lumen Labs", ""},
		"07-orbital-applied@demo.dzinga":     {"applied", "Orbital Pay", "Principal Product Manager"},
		"08-kestrel-offer@demo.dzinga":       {"offer", "Kestrel AI", ""},
		"09-kestrel-applied@demo.dzinga":     {"applied", "Kestrel AI", "Head of Product"},
		"10-quanta-applied@demo.dzinga":      {"applied", "Quanta Health", "Product Analyst"},
	}
	for id, w := range want {
		g, ok := results[id]
		if !ok {
			t.Errorf("%s: not detected", id)
			continue
		}
		if g.kind != w.kind || g.company != w.company || (w.role != "" && g.role != w.role) {
			t.Errorf("%s: got %+v, want %+v", id, g, w)
		}
	}
	// The message without a Message-ID gets a stable hash ID; its encoded subject decodes.
	var tidewater bool
	for id, g := range results {
		if strings.HasPrefix(id, "sha256:") && g.company == "Tidewater" && g.role == "Product Manager, Payments" && g.kind == "applied" {
			tidewater = true
		}
	}
	if !tidewater {
		t.Errorf("Tidewater (no Message-ID, RFC 2047 subject) not detected: %+v", results)
	}
	if len(results) != len(want)+1 {
		t.Errorf("want %d job emails (noise excluded), got %d: %+v", len(want)+1, len(results), results)
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText(`<html><head><title>x</title></head><style>p{color:red}</style><p>Hello&nbsp;<b>there</b></p><script>alert(1)</script><p>Bye</p>`)
	if strings.Contains(got, "alert") || strings.Contains(got, "color") || !strings.Contains(got, "Hello") || !strings.Contains(got, "Bye") {
		t.Errorf("got %q", got)
	}
}
