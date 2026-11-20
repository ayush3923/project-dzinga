// Package peers calls the services startoff depends on: the organizations service
// (which organizations exist, and their names), the sync service (mail refresh)
// and the quiz service (the day's quiz). Dependencies point one way: none of them
// calls startoff (ADR 0011).
package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ayush3923/project-dzinga/services/startoff/internal/store"
)

var ErrNotFound = errors.New("organization not found")

type client struct {
	name, base, token string
	http              *http.Client
}

func (c client) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", c.name, err)
	}
	defer res.Body.Close()
	if out != nil && res.StatusCode < 300 {
		if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out); err != nil {
			return res.StatusCode, fmt.Errorf("%s: %w", c.name, err)
		}
	}
	return res.StatusCode, nil
}

// ---------- Organizations service ----------

type Orgs struct{ c client }

func NewOrgs(base, token string) *Orgs {
	return &Orgs{client{"organizations service", base, token, &http.Client{Timeout: 20 * time.Second}}}
}

// Org returns an organization's id and name (ErrNotFound when there is none).
func (o *Orgs) Org(ctx context.Context, id int64) (store.Org, error) {
	var body struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	code, err := o.c.do(ctx, "GET", fmt.Sprintf("/api/v1/organizations/%d", id), nil, &body)
	switch {
	case err != nil:
		return store.Org{}, err
	case code == http.StatusNotFound:
		return store.Org{}, ErrNotFound
	case code != http.StatusOK:
		return store.Org{}, fmt.Errorf("organizations service returned %d", code)
	}
	return store.Org{ID: body.ID, Name: body.Name}, nil
}

// Candidates lists every organization with its application count and last activity.
func (o *Orgs) Candidates(ctx context.Context) ([]store.Candidate, error) {
	var body struct {
		Organizations []struct {
			ID             int64      `json:"id"`
			Name           string     `json:"name"`
			Applications   int        `json:"applications"`
			LastActivityAt *time.Time `json:"last_activity_at"`
		} `json:"organizations"`
	}
	code, err := o.c.do(ctx, "GET", "/api/v1/organizations", nil, &body)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("organizations service returned %d", code)
	}
	out := make([]store.Candidate, 0, len(body.Organizations))
	for _, x := range body.Organizations {
		out = append(out, store.Candidate{Org: store.Org{ID: x.ID, Name: x.Name}, Applications: x.Applications, LastActivityAt: x.LastActivityAt})
	}
	return out, nil
}

// ---------- Sync service ----------

type Ingest struct{ c client }

func NewIngest(base, token string) *Ingest {
	return &Ingest{client{"sync service", base, token, &http.Client{Timeout: 60 * time.Second}}}
}

type Mailbox struct {
	Address string `json:"address"`
	Status  string `json:"status"` // pending | scanning | done | error
	Found   int    `json:"found"`
	Added   int    `json:"added"`
	Error   string `json:"error,omitempty"`
}

type SyncState struct {
	Running   bool      `json:"running"`
	Mailboxes []Mailbox `json:"mailboxes"`
	Error     string    `json:"error,omitempty"`
}

// Start begins a refresh of every enabled mailbox. A refresh already running is
// not an error: the run waits for it.
func (i *Ingest) Start(ctx context.Context) error {
	code, err := i.c.do(ctx, "POST", "/v1/sync", map[string]any{"mailbox_ids": []int64{}}, nil)
	if err != nil {
		return err
	}
	if code == http.StatusAccepted {
		return nil
	}
	if st, err := i.State(ctx); err == nil && st.Running {
		return nil
	}
	return fmt.Errorf("sync service returned %d", code)
}

func (i *Ingest) State(ctx context.Context) (SyncState, error) {
	var st SyncState
	code, err := i.c.do(ctx, "GET", "/v1/sync", nil, &st)
	if err == nil && code != http.StatusOK {
		err = fmt.Errorf("sync service returned %d", code)
	}
	return st, err
}

// ---------- Quiz service ----------

type Quiz struct{ c client }

func NewQuiz(base, token string) *Quiz {
	return &Quiz{client{"quiz service", base, token, &http.Client{Timeout: 10 * time.Second}}}
}

// RequestToday asks for today's quiz (Claude builds it on its next run).
func (q *Quiz) RequestToday(ctx context.Context) error {
	code, err := q.c.do(ctx, "POST", "/internal/v1/requests", nil, nil)
	if err == nil && code != http.StatusAccepted {
		err = fmt.Errorf("quiz service returned %d", code)
	}
	return err
}
