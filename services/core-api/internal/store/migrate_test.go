package store

import (
	"strings"
	"testing"
)

// Existing databases are upgraded by position, so each migration must keep its
// number. (2026-10-23: a migration declared in digest.go ran before research.go's.)
func TestMigrationOrder(t *testing.T) {
	want := map[int]string{6: "job_url", 7: "research_requests", 8: "refreshes", 9: "rounds_done", 10: "quiz_answers", 11: "DROP TABLE quizzes", 12: "profile_photos", 13: "interview_reviews", 14: "DROP TABLE organizations", 15: "job_ref"}
	for v, marker := range want {
		if i := v - 2; i >= len(migrations) || !strings.Contains(migrations[i], marker) {
			t.Errorf("v%d should contain %q", v, marker)
		}
	}
}
