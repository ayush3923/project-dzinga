package analyze

import (
	"context"
	"fmt"
	"strings"

	"github.com/ayush3923/project-dzinga/services/agent/internal/core"
	"github.com/ayush3923/project-dzinga/services/agent/internal/router"
)

const digestSystem = `You write a job seeker's morning digest from facts that have already been computed.
Use only the facts given; never add companies, numbers or advice that aren't there. At most 100 words, plain
and warm, no greeting, no headings, no bullet points. Cover, in this order: new applications, replies
(good news before rejections), the week's trend, the next steps, and suggested companies with their reason.
Skip any part that has nothing in it. ` + untrusted

var digestSchema = obj(map[string]any{"text": str()})

func digestFacts(d core.Digest) string {
	var b strings.Builder
	apps := func(title string, list []core.DigestApp) {
		fmt.Fprintf(&b, "%s (%d):\n", title, len(list))
		for i, a := range list {
			if i == 8 {
				fmt.Fprintf(&b, "- and %d more\n", len(list)-8)
				break
			}
			fmt.Fprintf(&b, "- %s, %s %s\n", a.Company, a.Role, a.Kind)
		}
	}
	apps("New applications since the last refresh", d.NewApplications)
	apps("Replies since the last refresh (kind)", d.Responses)
	t := d.Trends
	fmt.Fprintf(&b, "This week: %d applications (previous week %d), %d replies, %d rejections, %d still waiting; %.0f%% of the last 30 days' applications got a reply.\n",
		t.AppsThisWeek, t.AppsLastWeek, t.ResponsesThisWeek, t.RejectionsThisWeek, t.Waiting, t.ResponseRate30d*100)
	b.WriteString("Next steps:\n")
	for _, s := range d.NextSteps {
		fmt.Fprintf(&b, "- %s: %s (%s)\n", s.Company, s.Title, s.Role)
	}
	b.WriteString("Suggested companies:\n")
	for _, s := range d.Suggested {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Reason)
	}
	return b.String()
}

// Digest writes the morning digest for the latest refresh, once: a digest that a
// model already wrote is not written again. Without credit the template stays.
func (a *Agent) Digest(ctx context.Context) (core.Digest, error) {
	d, err := a.Core.Digest(ctx)
	if err != nil {
		return d, err
	}
	if d.RefreshID == 0 || d.Model != "template" {
		return d, nil
	}
	// Suggested companies come from the organizations service; the digest reads fine without them.
	if s, err := a.Orgs.Suggestions(ctx); err == nil {
		d.Suggested = s
	} else {
		a.Log.Warn("digest_suggestions_unavailable", "reason", err.Error())
	}
	var out struct {
		Text string `json:"text"`
	}
	model, err := a.structured(ctx, 0, router.DigestSummary, digestSystem, "<facts>\n"+digestFacts(d)+"</facts>", digestSchema, &out)
	if err != nil || strings.TrimSpace(out.Text) == "" {
		a.Log.Warn("digest_fallback", "reason", fmt.Sprint(err))
		return d, nil
	}
	text := out.Text
	if w := strings.Fields(text); len(w) > 110 { // the model overshot: keep it short
		text = strings.Join(w[:100], " ") + "…"
	}
	if err := a.Core.SaveDigest(ctx, d.RefreshID, text, model); err != nil {
		return d, err
	}
	d.Text, d.Model = text, model
	return d, nil
}
