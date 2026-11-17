package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The profile photo is a JPEG file in the API's photo folder (see api/photo.go);
// profile_photos records when it changed, so the web app can refresh its copy.

func (s *Store) SetPhoto(ctx context.Context, userID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO profile_photos (user_id, updated_at) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET updated_at = excluded.updated_at`, userID, now.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) DeletePhoto(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM profile_photos WHERE user_id = ?`, userID)
	return err
}

// PhotoUpdatedAt is nil when the user has no photo.
func (s *Store) PhotoUpdatedAt(ctx context.Context, userID string) (*time.Time, error) {
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT updated_at FROM profile_photos WHERE user_id = ?`, userID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	return &t, err
}
