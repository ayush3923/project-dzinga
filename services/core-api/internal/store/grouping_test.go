package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// An application merged by an older version is split by acknowledgement date.
func TestSplitRolelessApplications(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	d := func(day int) string { return time.Date(2026, 8, day, 10, 0, 0, 0, time.UTC).Format(time.RFC3339) }
	res, _ := st.db.Exec(`INSERT INTO applications (user_id, key, company, role, stage, source_account, applied_at, last_activity_at)
		VALUES ('u', 'google|', 'Google', '', 'applied', 'me@gmail.com', ?, ?)`, d(1), d(25))
	appID, _ := res.LastInsertId()
	for i, e := range []struct {
		day  int
		kind string
	}{{1, "applied"}, {2, "applied"}, {10, "applied"}, {12, "rejected"}, {25, "applied"}} {
		if _, err := st.db.Exec(`INSERT INTO events (user_id, application_id, message_id, account, occurred_at, sender, subject, snippet, kind)
			VALUES ('u', ?, ?, 'me@gmail.com', ?, '', '', '', ?)`, appID, string(rune('a'+i)), d(e.day), e.kind); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.SplitRolelessApplications(ctx)
	if err != nil || n != 2 {
		t.Fatalf("created %d, err %v", n, err)
	}
	if n, _ := st.SplitRolelessApplications(ctx); n != 0 {
		t.Errorf("second run should change nothing, created %d", n)
	}
	rows, _ := st.db.Query(`SELECT applied_at, stage, (SELECT COUNT(*) FROM events e WHERE e.application_id = a.id) FROM applications a ORDER BY applied_at`)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var at, stage string
		var n int
		rows.Scan(&at, &stage, &n)
		got = append(got, at[:10]+" "+stage+" "+string(rune('0'+n)))
	}
	want := []string{"2026-08-01 applied 2", "2026-08-10 rejected 2", "2026-08-25 applied 1"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergeOrphanApplications(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := func(day int) string { return time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC).Format(time.RFC3339) }
	add := func(key, role, at, kind, msg string) {
		res, err := st.db.Exec(`INSERT INTO applications (user_id, key, company, role, stage, source_account, applied_at, last_activity_at)
			VALUES ('u', ?, 'Pearson', ?, ?, 'me@live.in', ?, ?)`, key, role, kind, at, at)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		st.db.Exec(`INSERT INTO events (user_id, application_id, message_id, account, occurred_at, sender, subject, snippet, kind)
			VALUES ('u', ?, ?, 'me@live.in', ?, '', '', '', ?)`, id, msg, at, kind)
	}
	add("pearson|", "", d(29), "rejected", "m2")                                     // orphan reply, made first
	add("pearson|advancedspecialist", "Advanced Specialist", d(10), "applied", "m1") // its application
	n, err := st.MergeOrphanApplications(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("merged %d, err %v", n, err)
	}
	var apps int
	var stage string
	st.db.QueryRow(`SELECT COUNT(*), MAX(stage) FROM applications`).Scan(&apps, &stage)
	if apps != 1 || stage != "rejected" {
		t.Errorf("apps=%d stage=%s", apps, stage)
	}
}
