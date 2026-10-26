package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDigest(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC) // 09:30 IST
	ev := func(company, role, kind string, at time.Time, id string) {
		if _, err := s.Ingest(ctx, "u", []IngestEvent{{MessageID: id, Account: "me@gmail.com", OccurredAt: at, Sender: "jobs@" + strings.ToLower(company) + ".com",
			Subject: kind, Snippet: "", Kind: kind, Company: company, Role: role}}); err != nil {
			t.Fatal(err)
		}
	}
	// Before the previous refresh.
	ev("Acme", "Senior PM", "applied", now.AddDate(0, 0, -20), "a1")
	ev("Beta", "Principal PM", "applied", now.AddDate(0, 0, -3), "b1")
	if _, err := s.RecordRefresh(ctx, "u", time.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	// Brought in by the latest refresh.
	ev("Gamma", "Senior PM", "applied", now.AddDate(0, 0, -1), "g1")
	ev("Beta", "Principal PM", "rejected", now.Add(-2*time.Hour), "b2")
	ev("Delta", "Staff PM", "rejected", now.Add(-3*time.Hour), "d1") // its application email isn't in any mailbox
	ev("Acme", "Senior PM", "update", now.Add(-4*time.Hour), "a2")
	time.Sleep(5 * time.Millisecond)
	refresh, err := s.RecordRefresh(ctx, "u", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	d, err := s.Digest(ctx, "u", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if d.RefreshID != refresh || len(d.NewApplications) != 1 || d.NewApplications[0].Company != "Gamma" {
		t.Errorf("new applications: %+v", d.NewApplications)
	}
	if len(d.Responses) != 3 || d.Responses[0].Company != "Beta" || d.Responses[0].Kind != "rejected" {
		t.Errorf("responses: %+v", d.Responses)
	}
	if len(d.Series) != 7 || d.Model != "template" || len(strings.Fields(d.Text)) > 100 || !strings.Contains(d.Text, "Gamma") {
		t.Errorf("digest: %+v", d)
	}
	// A status update is not good news; a lone rejection is not a new application.
	if strings.Contains(d.Text, "Good news") || !strings.Contains(d.Text, "Status update from Acme") || strings.Contains(d.Text, "new application: Delta") {
		t.Errorf("text: %s", d.Text)
	}

	// Only the latest refresh accepts a written digest.
	if err := s.SaveDigestText(ctx, "u", refresh-1, "old", "claude-sonnet-5", now); err != ErrNotFound {
		t.Errorf("stale refresh accepted: %v", err)
	}
	if err := s.SaveDigestText(ctx, "u", refresh, "Fresh digest.", "claude-sonnet-5", now); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Digest(ctx, "u", time.Now()); d.Text != "Fresh digest." || d.Model != "claude-sonnet-5" {
		t.Errorf("saved text not used: %q %q", d.Text, d.Model)
	}
}

func TestDigestSeries(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, IST)
	add := func(id, company, kind string, at time.Time) {
		s.Ingest(ctx, "u", []IngestEvent{{MessageID: id, Account: "me@gmail.com", OccurredAt: at, Sender: "x@y.com", Kind: kind, Company: company, Role: "PM"}})
	}
	add("1", "A", "applied", now.AddDate(0, 0, -5)) // day 1 of 7
	add("2", "B", "applied", now.AddDate(0, 0, -5))
	add("3", "B", "rejected", now.AddDate(0, 0, -2)) // day 4
	add("4", "C", "applied", now.AddDate(0, 0, -100))
	d, err := s.Digest(ctx, "u", now)
	if err != nil {
		t.Fatal(err)
	}
	got := []int{}
	for _, p := range d.Series {
		got = append(got, p.Applications, p.Rejections, p.Waiting)
	}
	// Waiting: A and B from day 1; B leaves when it's rejected; C is too old to count.
	want := []int{0, 0, 0, 2, 0, 2, 0, 0, 2, 0, 0, 2, 0, 1, 1, 0, 0, 1, 0, 0, 1}
	if len(got) != len(want) {
		t.Fatalf("series %v", d.Series)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("series %+v", d.Series)
		}
	}
	if d.Series[6].Date != "2026-10-23" || d.Trends.AppsThisWeek != 2 || d.Trends.RejectionsThisWeek != 1 || d.Trends.Waiting != 1 {
		t.Errorf("trends %+v last %s", d.Trends, d.Series[6].Date)
	}
}
