package analyze

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ayush3923/project-dzinga/services/agent/internal/core"
	"github.com/ayush3923/project-dzinga/services/agent/internal/router"
)

// Company profiles are researched in the Claude app and saved through core-api;
// the agent no longer researches companies over the API.

const diagnosisSummarySystem = `You summarise a diagnosis that has already been made about why a company isn't
responding to a candidate's applications. Use only the causes and facts given; do not add causes. Write 2-3
plain, candid sentences: the most likely reason first, then what to do about it. ` + untrusted

var diagnosisSummarySchema = obj(map[string]any{"summary": str()})

func appLines(o core.Organization) string {
	var b strings.Builder
	for _, a := range o.Applications {
		var steps []string
		for _, s := range a.Timeline {
			steps = append(steps, fmt.Sprintf("%s %s", s.Label, s.At.Format("2006-01-02")))
		}
		fit := "no fit score"
		if a.Fit != nil {
			fit = fmt.Sprintf("fit %d/100: %s", a.Fit.Total, a.Fit.Verdict)
		}
		fmt.Fprintf(&b, "- %s: %s (%s)\n", a.Role, strings.Join(steps, " → "), fit)
	}
	return b.String()
}

// DiagnoseRules finds the likely causes from the history with one company.
func DiagnoseRules(o core.Organization, now time.Time) core.Diagnosis {
	var d core.Diagnosis
	add := func(conf, text string) { d.Causes = append(d.Causes, core.DiagnosisItem{Text: text, Confidence: conf}) }
	act := func(text string) { d.Actions = append(d.Actions, text) }

	n := len(o.Applications)
	var applied []time.Time
	var fits []int
	fastRejects, silentOld, recent := 0, 0, 0
	roles := map[string]bool{}
	for _, a := range o.Applications {
		roles[strings.ToLower(a.Role)] = true
		if a.Fit != nil {
			fits = append(fits, a.Fit.Total)
		}
		if len(a.Timeline) == 0 {
			continue
		}
		start := a.Timeline[0].At
		applied = append(applied, start)
		last := a.Timeline[len(a.Timeline)-1]
		age := now.Sub(start).Hours() / 24
		switch {
		case strings.Contains(strings.ToLower(last.Label), "reject") && last.At.Sub(start).Hours() < 24*7:
			fastRejects++
		case len(a.Timeline) == 1 && age > 30:
			silentOld++
		case len(a.Timeline) == 1 && age <= 14:
			recent++
		}
	}
	sort.Slice(applied, func(i, j int) bool { return applied[i].Before(applied[j]) })

	if len(fits) > 0 {
		sum := 0
		for _, f := range fits {
			sum += f
		}
		avg := sum / len(fits)
		switch {
		case avg < 55:
			add("high", fmt.Sprintf("Low fit: the scored roles average %d/100, so screeners likely see a mismatch with the posting.", avg))
			act("Apply only to roles here that score 65 or more, and tailor your CV to the posting's own wording.")
		case avg < 70:
			add("medium", fmt.Sprintf("Moderate fit: the scored roles average %d/100; gaps in the posting's requirements may be filtering you out.", avg))
			act("Close the top gaps from the fit score in your CV summary before the next application.")
		}
	} else {
		add("low", "No fit scores yet, so role fit can't be judged.")
		act("Add the posting links and run the fit score for these applications.")
	}
	if fastRejects > 0 {
		add("high", fmt.Sprintf("%d rejection(s) came within a week of applying, which usually means an automated or recruiter screen on years, level, location or keywords.", fastRejects))
		act("Check the posting's must-haves (years, location, level) against your CV's first half-page; that's what the screen reads.")
	}
	if n >= 3 && len(applied) >= 3 && applied[len(applied)-1].Sub(applied[0]).Hours() < 24*45 {
		add("medium", fmt.Sprintf("%d applications in %d days may read as unfocused to the same recruiting team.", n,
			int(applied[len(applied)-1].Sub(applied[0]).Hours()/24)+1))
		act("Pick the single best-fitting role here and pursue it through a referral rather than more portal applications.")
	}
	if len(roles) >= 3 {
		add("medium", fmt.Sprintf("You applied for %d different roles here, which can blur how the team sees your profile.", len(roles)))
	}
	if silentOld > 0 {
		add("medium", fmt.Sprintf("%d application(s) have had no reply for over 30 days: portal applications without a referral are often never reviewed.", silentOld))
		act("Find a current employee in the target team on LinkedIn and ask for a referral or a 15-minute chat.")
	}
	if recent > 0 && recent == n {
		add("high", "Every application here is under two weeks old: it's likely too early for a reply.")
		act("Wait until two weeks have passed, then send one short follow-up to the recruiter.")
	}
	if len(d.Causes) == 0 {
		add("low", "No clear pattern in the history with this company.")
		act("Ask a recruiter or employee here for feedback on your profile.")
	}
	d.Summary = d.Causes[0].Text
	if len(d.Actions) > 0 {
		d.Summary += " Next: " + d.Actions[0]
	}
	return d
}

// Diagnose explains why a company isn't responding: rules find the causes, and
// one small Sonnet 5 call writes the summary (the rules' own summary otherwise).
func (a *Agent) Diagnose(ctx context.Context, orgID int64) (core.Diagnosis, error) {
	org, err := a.Core.Organization(ctx, orgID)
	if err != nil {
		return core.Diagnosis{}, err
	}
	if len(org.Applications) == 0 {
		return core.Diagnosis{}, fmt.Errorf("no applications to %s to diagnose", org.Name)
	}
	d := DiagnoseRules(org, time.Now())
	d.Model = "rules"
	var causes strings.Builder
	for _, c := range d.Causes {
		fmt.Fprintf(&causes, "- [%s] %s\n", c.Confidence, c.Text)
	}
	var out struct {
		Summary string `json:"summary"`
	}
	model, err := a.structured(ctx, 0, router.DiagnosisSummary, diagnosisSummarySystem,
		fmt.Sprintf("<company>%s</company>\n<pattern>%s; responses %d, rejections %d</pattern>\n<applications>\n%s</applications>\n<causes>\n%s</causes>\n<actions>\n- %s\n</actions>",
			org.Name, org.DiagnosisReason, org.Responded, org.Rejected, appLines(org), causes.String(), strings.Join(d.Actions, "\n- ")),
		diagnosisSummarySchema, &out)
	if err == nil && out.Summary != "" {
		d.Summary, d.Model = out.Summary, "rules+"+model
	} else {
		a.Log.Warn("diagnosis_summary_fallback", "organization_id", orgID, "reason", fmt.Sprint(err))
	}
	return d, a.Core.SaveDiagnosis(ctx, orgID, d)
}
