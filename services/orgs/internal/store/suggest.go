package store

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Suggestions: companies worth applying to now, from the user's own history. They
// were part of core-api's morning digest; they need the preferred list, so they
// live here (ADR 0010) and the Today page reads them from this service.

type Suggestion struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func (s *Store) Suggestions(ctx context.Context, userID string, apps []App, now time.Time) ([]Suggestion, error) {
	out := []Suggestion{}
	rep, err := s.Organizations(ctx, userID, apps, now)
	if err != nil {
		return out, err
	}
	add := func(o OrgSummary, reason string) {
		for _, x := range out {
			if x.ID == o.ID {
				return
			}
		}
		if len(out) < 3 {
			out = append(out, Suggestion{ID: o.ID, Name: o.Name, Reason: reason})
		}
	}
	for _, o := range rep.Preferred {
		if o.Open == 0 {
			add(o, "on your preferred list, with nothing open there now")
		}
	}
	warm := append([]OrgSummary{}, rep.Organizations...)
	sort.SliceStable(warm, func(i, j int) bool {
		return warm[i].Interviewed+warm[i].Shortlisted > warm[j].Interviewed+warm[j].Shortlisted
	})
	for _, o := range warm {
		if o.Open == 0 && o.Interviewed+o.Shortlisted > 0 {
			add(o, fmt.Sprintf("shortlisted or interviewed you before (%d)", o.Interviewed+o.Shortlisted))
		}
	}
	fast := append([]OrgSummary{}, rep.Organizations...)
	sort.SliceStable(fast, func(i, j int) bool {
		if fast[i].AvgResponseDays == nil || fast[j].AvgResponseDays == nil {
			return fast[j].AvgResponseDays == nil && fast[i].AvgResponseDays != nil
		}
		return *fast[i].AvgResponseDays < *fast[j].AvgResponseDays
	})
	for _, o := range fast {
		if o.Open == 0 && o.AvgResponseDays != nil && *o.AvgResponseDays <= 7 && o.Responded > o.Rejected {
			add(o, fmt.Sprintf("replies fast (about %.0f days)", *o.AvgResponseDays))
		}
	}
	return out, nil
}
