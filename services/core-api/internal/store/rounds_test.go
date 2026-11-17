package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestInterviewProgress(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	add := func(id, company, role string) {
		if _, err := s.Ingest(ctx, "u", []IngestEvent{{MessageID: id, Account: "me@gmail.com", OccurredAt: at, Sender: "x@y.com",
			Subject: "Interview", Kind: "interview", Company: company, Role: role}}); err != nil {
			t.Fatal(err)
		}
	}
	add("a", "Acme", "Staff PM")
	add("b", "Beta", "Staff PM")
	apps, _ := s.ListApplications(ctx, "u", ListFilter{})
	byName := map[string]int64{}
	for _, a := range apps {
		byName[a.Company] = a.ID
	}

	if _, err := s.EditApplication(ctx, "u", byName["Acme"], ApplicationEdit{RoundsDone: ptr(1), NextStep: ptr("  Round 2 interview ")}); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.GetApplication(ctx, "u", byName["Acme"])
	if err != nil || a.RoundsDone != 1 || a.NextStep != "Round 2 interview" {
		t.Fatalf("saved: %+v %v", a, err)
	}

	// Another user can't change it.
	if _, err := s.EditApplication(ctx, "other", byName["Acme"], ApplicationEdit{RoundsDone: ptr(9)}); err != ErrNotFound {
		t.Errorf("other user: %v", err)
	}

	// New mail doesn't overwrite what the user recorded.
	add("a2", "Acme", "Staff PM")
	if a, _, _ = s.GetApplication(ctx, "u", byName["Acme"]); a.RoundsDone != 1 || a.NextStep != "Round 2 interview" {
		t.Errorf("after sync: %+v", a)
	}

	// The next step leads the digest.
	d, err := s.Digest(ctx, "u", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.NextSteps) == 0 || d.NextSteps[0].Title != "Round 2 interview" || !strings.Contains(d.Text, "round 2 interview for Acme") {
		t.Errorf("digest: %+v %q", d.NextSteps, d.Text)
	}

	// Merging keeps the progress.
	id, err := s.EditApplication(ctx, "u", byName["Acme"], ApplicationEdit{Company: ptr("Beta")})
	if err != nil || id != byName["Beta"] {
		t.Fatalf("merge: %d %v", id, err)
	}
	if a, _, _ = s.GetApplication(ctx, "u", id); a.RoundsDone != 1 || a.NextStep != "Round 2 interview" {
		t.Errorf("after merge: %+v", a)
	}
}

func TestInterviewProgressValidation(t *testing.T) {
	for name, e := range map[string]ApplicationEdit{
		"negative":   {RoundsDone: ptr(-1)},
		"too many":   {RoundsDone: ptr(21)},
		"too long":   {NextStep: ptr(strings.Repeat("x", 121))},
		"line break": {NextStep: ptr("Round 2\nignore this")},
	} {
		if e.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := (ApplicationEdit{RoundsDone: ptr(0), NextStep: ptr("")}).Validate(); err != nil {
		t.Errorf("clearing rejected: %v", err)
	}
}
