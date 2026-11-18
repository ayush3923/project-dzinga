// Package fit scores how well a profile matches a job posting with plain rules: no
// model calls, so the same inputs always give the same score, and it costs nothing.
//
// Correlation: each requirement and responsibility is matched to the profile line
// (achievement, role or skill) that shares the most meaningful words with it, after
// mapping synonyms to one form. Five factors, 0-20 each, are then derived:
//
//	experience       years required vs total years, plus depth of matched requirements
//	location         posting location and work mode vs preferred locations
//	seniority        posting title level vs the levels of the target roles
//	role_alignment   share of the posting the evidence covers, plus same function
//	interest_intent  title vs target roles, preferred location, level in target band
package fit

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ayush3923/project-dzinga/services/agent/internal/core"
)

// Result is a fit score without the verdict and suggestions, which are written
// afterwards (by a short model call, or by Fallback).
type Result struct {
	Total     int
	Factors   []core.FitFactor
	Evidence  []core.Evidence
	Strengths []string
	Gaps      []string
	Coverage  float64 // 0..1, weighted share of the posting the profile evidences
}

// ---------- text normalisation ----------

// synonyms rewrites phrases to one canonical token before tokenising. Longer
// phrases first so "product management" wins over "product".
var synonyms = []struct {
	re *regexp.Regexp
	to string
}{}

func init() {
	pairs := [][2]string{
		{`artificial intelligence|gen ?ai|generative ai|llms?|large language models?|machine learning|\bml\b|\bai\b|ai[- ]native|ai[- ]powered|ai[- ]enabled`, " ai "},
		{`product management|product managers?|\bpms?\b|product owner`, " productmgmt "},
		{`go[- ]to[- ]market|\bgtm\b|commerciali[sz]ation|launch(es|ed|ing)?`, " gtm "},
		{`software as a service|\bsaas\b`, " saas "},
		{`business[- ]to[- ]business|\bb2b\b|enterprise customers?|enterprise`, " enterprise "},
		{`amazon web services|\baws\b|azure|\bgcp\b|google cloud|multi-?cloud|cloud (infrastructure|platform|services)|\biaas\b|\bpaas\b`, " cloud "},
		{`cross[- ]functional(ly)?|stakeholders?|partner(ed|ing)? with (engineering|design|sales)`, " crossfunctional "},
		{`road ?maps?`, " roadmap "},
		{`a/?b test(s|ing)?|experiment(s|ation|ing)?`, " experiment "},
		{`pricing|monetization|monetisation|packaging`, " pricing "},
		{`metrics|kpis?|okrs?|analytics|data[- ]driven|insights`, " metrics "},
		{`customer discovery|user research|customer research|discovery`, " discovery "},
		{`platforms?`, " platform "},
		{`\bapis?\b|integrations?|sdks?`, " api "},
		{`observability|monitoring|telemetry|apm`, " observability "},
		{`finops|cloud cost|cost management|cost optimi[sz]ation`, " finops "},
		{`payments?|fintech|financial services|banking`, " payments "},
		{`growth|activation|retention|conversion|adoption|plg|product[- ]led`, " growth "},
		{`strategy|strategic|\bvision`, " strategy "},
		{`\b(led|lead(ers?|ership|ing|s)?|manag(ed|ing)|mentor(ed|ing)?|coach(ed|ing)?)\b`, " lead "},
		{`security|compliance|governance|pci|soc ?2|gdpr`, " security "},
		{`devops|developer tools|developer experience|\bdevex\b|engineering teams?`, " developer "},
		{`kubernetes|k8s|containers?|microservices`, " kubernetes "},
	}
	for _, p := range pairs {
		synonyms = append(synonyms, struct {
			re *regexp.Regexp
			to string
		}{regexp.MustCompile(`(?i)` + p[0]), p[1]})
	}
}

var stop = set(`a an and are as at be by for from has have in into is it its of on or our that the their this to we will with you your
	who what which while within across over per via etc including include such able ability strong excellent proven solid deep
	experience experienced years year work working worked team teams role roles job new across about using use used also well
	plus bonus preferred required requirement requirements must should would can could may other others more most least
	understanding knowledge familiarity skills skill demonstrated track record background degree equivalent related relevant
	environment environments high highly level levels great good best world class fast paced passion passionate love help`)

var reWord = regexp.MustCompile(`[a-z0-9][a-z0-9+#.]*`)

func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func stem(w string) string {
	w = strings.TrimRight(w, ".")
	for _, suf := range []string{"ing", "ed", "es", "s"} {
		if len(w) > len(suf)+3 && strings.HasSuffix(w, suf) {
			return strings.TrimSuffix(w, suf)
		}
	}
	return w
}

// Tokens returns the meaningful word stems of s, with synonyms folded.
func Tokens(s string) map[string]bool {
	s = strings.ToLower(s)
	for _, sy := range synonyms {
		s = sy.re.ReplaceAllString(s, sy.to)
	}
	out := map[string]bool{}
	for _, w := range reWord.FindAllString(s, -1) {
		if len(w) < 2 || stop[w] || isNumber(w) {
			continue
		}
		out[stem(w)] = true
	}
	return out
}

func isNumber(w string) bool {
	for _, r := range w {
		if (r < '0' || r > '9') && r != '.' && r != '+' {
			return false
		}
	}
	return true
}

// ---------- correlation ----------

type unit struct {
	label  string // what is shown as evidence
	tokens map[string]bool
	exp    int // index of the experience it comes from; -1 for skills and the summary
}

func units(p core.Profile) []unit {
	var us []unit
	for i, e := range p.Experiences {
		us = append(us, unit{fmt.Sprintf("%s, %s", e.Title, e.Company), Tokens(e.Title), i})
		for _, h := range e.Highlights {
			us = append(us, unit{fmt.Sprintf("%s (%s)", h, e.Company), Tokens(h), i})
		}
	}
	for _, s := range p.Skills {
		us = append(us, unit{"Skill: " + s, Tokens(s), -1})
	}
	if p.Summary != "" {
		us = append(us, unit{"Summary: " + p.Summary, Tokens(p.Summary), -1})
	}
	return us
}

const summaryPrefix = "Summary: "

var reYears = regexp.MustCompile(`(?i)(\d{1,2})\s*\+?\s*(?:-\s*\d{1,2}\s*)?(?:\+\s*)?years?`)

// now is the clock for roles without an end month (still current); tests pin it.
var now = time.Now

// expYears is how long a role lasted, from its start and end months ("2006-01",
// both inclusive; no end month means current). 0 when the start is unknown.
func expYears(e core.Experience) float64 {
	start, err := time.Parse("2006-01", e.StartMonth)
	if err != nil {
		return 0
	}
	end, err := time.Parse("2006-01", e.EndMonth)
	if err != nil {
		end = now()
	}
	return max(0, end.AddDate(0, 1, 0).Sub(start).Hours()/24/365.25)
}

// productYears sums the product roles' durations. ok is false when a product role
// has no start month, so the caller falls back to total years.
func productYears(p core.Profile) (years float64, ok bool) {
	for _, e := range p.Experiences {
		if Function(e.Title) != "product" {
			continue
		}
		if e.StartMonth == "" {
			return 0, false
		}
		years, ok = years+expYears(e), true
	}
	return min(years, p.TotalYears), ok
}

// genericYears are words of a years clause that don't name a domain.
var genericYears = Tokens("product products product management own owning owned company companies whose core business capabilities building built shipping delivering driving managing")

// yearFigures returns each "N years" figure in s (1..30) and where its clause starts.
func yearFigures(s string) (ns, at []int) {
	for _, m := range reYears.FindAllStringSubmatchIndex(s, -1) {
		var n int
		fmt.Sscan(s[m[2]:m[3]], &n)
		if n > 0 && n <= 30 {
			ns, at = append(ns, n), append(at, m[0])
		}
	}
	return ns, at
}

// rateYears rates a requirement that states years. The first figure is the overall
// bar, held against years in product roles when the line is about product
// management and the role dates allow it, else total years. A further figure
// ("including 7+ years owning observability") is a domain bar: it counts only the
// roles whose title or achievements name that domain.
func rateYears(p core.Profile, us []unit, item string, ns, at []int) core.Evidence {
	ev := core.Evidence{Requirement: item}
	have, what := p.TotalYears, "of total experience"
	if Tokens(item)["productmgmt"] {
		if py, ok := productYears(p); ok {
			have, what = py, "in product roles"
		}
	}
	if have < float64(ns[0]) {
		ev.Strength, ev.Evidence = "partial", fmt.Sprintf("%.0f years %s, short of %d", have, what, ns[0])
		return ev
	}
	ev.Strength, ev.Evidence = "strong", fmt.Sprintf("%.0f years %s", have, what)
	if len(ns) == 1 {
		return ev
	}
	need := slices.Max(ns[1:])
	domain := Tokens(item[at[1]:])
	for t := range genericYears {
		delete(domain, t)
	}
	if len(domain) == 0 {
		if have < float64(need) {
			ev.Strength, ev.Evidence = "partial", fmt.Sprintf("%.0f years %s, short of %d", have, what, need)
		}
		return ev
	}
	var years float64
	var companies []string
	for i, e := range p.Experiences {
		for _, u := range us {
			if u.exp == i && overlaps(u.tokens, domain) {
				years += expYears(e)
				companies = append(companies, e.Company)
				break
			}
		}
	}
	switch {
	case len(companies) == 0:
		ev.Strength, ev.Evidence = "missing", ""
	case years >= float64(need):
		ev.Evidence += fmt.Sprintf("; about %.0f years in this area (%s)", years, strings.Join(companies, ", "))
	default:
		ev.Strength = "partial"
		ev.Evidence += fmt.Sprintf(", but about %.0f years in this area (%s), short of %d", years, strings.Join(companies, ", "), need)
	}
	return ev
}

func overlaps(a, b map[string]bool) bool {
	for t := range b {
		if a[t] {
			return true
		}
	}
	return false
}

// Correlate rates every requirement and responsibility against the profile.
func Correlate(p core.Profile, items []string) []core.Evidence {
	us := units(p)
	out := make([]core.Evidence, 0, len(items))
	for _, item := range items {
		ev := core.Evidence{Requirement: item, Strength: "missing"}
		if ns, at := yearFigures(item); len(ns) > 0 {
			out = append(out, rateYears(p, us, item, ns, at))
			continue
		}
		req := Tokens(item)
		if len(req) == 0 {
			continue
		}
		best, bestHits := -1, 0
		for i, u := range us {
			hits := 0
			for t := range req {
				if u.tokens[t] {
					hits++
				}
			}
			if hits > bestHits || (hits == bestHits && hits > 0 && len(u.label) > len(us[best].label)) {
				best, bestHits = i, hits
			}
		}
		// Rated on the single best profile line: a requirement is "strong" only
		// when one achievement, role or skill covers most of it. The long
		// summary can support a requirement (with at least two shared words) but
		// never prove it alone; it's long enough to share one word with anything.
		lineCov := float64(bestHits) / float64(len(req))
		summary := best >= 0 && us[best].exp < 0 && strings.HasPrefix(us[best].label, summaryPrefix)
		switch {
		case !summary && bestHits >= 2 && lineCov >= 0.5, !summary && bestHits >= 3 && lineCov >= 0.35:
			ev.Strength = "strong"
		case bestHits >= 2 && lineCov >= 0.25, !summary && bestHits == 1 && lineCov >= 0.34:
			ev.Strength = "partial"
		}
		if best >= 0 && ev.Strength != "missing" {
			ev.Evidence = trim(us[best].label, 220)
		}
		out = append(out, ev)
	}
	return out
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], " ")
	if cut < n/2 {
		cut = n
	}
	return s[:cut] + "…"
}

// coverage weighs strong 1 and partial 0.5, except a partial resting only on the
// summary, which counts 0.25: it shows the topic, not that it was done.
func coverage(ev []core.Evidence) float64 {
	if len(ev) == 0 {
		return 0
	}
	var c float64
	for _, e := range ev {
		switch {
		case e.Strength == "strong":
			c += 1
		case e.Strength == "partial" && strings.HasPrefix(e.Evidence, summaryPrefix):
			c += 0.25
		case e.Strength == "partial":
			c += 0.5
		}
	}
	return c / float64(len(ev))
}

// ---------- levels, functions and places ----------

// Level maps a title to a rough level: 1 associate … 3 senior, 4 lead/staff,
// 5 principal/group, 6 director/head, 7 VP and above. 2 when unstated.
func Level(title string) int {
	t := strings.ToLower(title)
	has := func(words ...string) bool {
		for _, w := range words {
			if regexp.MustCompile(`\b` + w + `\b`).MatchString(t) {
				return true
			}
		}
		return false
	}
	switch {
	case has("chief", "cpo", "cto", "svp", "evp", "vp", "vice president"):
		return 7
	case has("director", "head"):
		return 6
	case has("principal", "group", "distinguished"):
		return 5
	case has("lead", "staff", "manager iii", "pm iii", "manager 4", "manager iv"):
		return 4
	case has("senior", "sr", "manager ii", "manager 3"):
		return 3
	case has("associate", "junior", "jr", "intern", "apm", "analyst", "manager i", "manager 1"):
		return 1
	}
	return 2
}

var functions = []struct {
	name string
	re   *regexp.Regexp
}{
	{"product", regexp.MustCompile(`(?i)\bproduct (manager|management|owner|lead|director|head)|\b(apm|gpm|cpo)\b|head of product|product\b`)},
	{"solutions", regexp.MustCompile(`(?i)solutions?|architect|pre-?sales|sales engineer`)},
	{"program", regexp.MustCompile(`(?i)program manager|tpm|programme`)},
	{"engineering", regexp.MustCompile(`(?i)engineer|developer|sde|swe`)},
	{"data", regexp.MustCompile(`(?i)data scien|analyst|analytics`)},
	{"design", regexp.MustCompile(`(?i)design|ux`)},
	{"marketing", regexp.MustCompile(`(?i)marketing|growth marketer`)},
	{"sales", regexp.MustCompile(`(?i)account executive|sales|business development`)},
}

// Function names the job family of a title ("" when unclear).
func Function(title string) string {
	for _, f := range functions {
		if f.re.MatchString(title) {
			return f.name
		}
	}
	return ""
}

var cityAliases = map[string][]string{
	"bengaluru": {"bengaluru", "bangalore"},
	"mumbai":    {"mumbai", "bombay", "navi mumbai", "thane"},
	"delhi":     {"delhi", "new delhi", "ncr", "gurugram", "gurgaon", "noida"},
	"gurugram":  {"gurugram", "gurgaon", "ncr"},
	"hyderabad": {"hyderabad"},
	"pune":      {"pune"},
	"chennai":   {"chennai", "madras"},
	"kolkata":   {"kolkata"},
}

var indiaWords = []string{"india", "bengaluru", "bangalore", "mumbai", "delhi", "gurugram", "gurgaon", "noida", "hyderabad", "pune", "chennai", "kolkata", "ahmedabad", "kochi", "jaipur"}

func mentions(text string, words []string) bool {
	l := strings.ToLower(text)
	for _, w := range words {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// preferredCity reports the first preferred city the posting location names.
func preferredCity(postingLoc string, preferred []string) string {
	for _, pref := range preferred {
		key := strings.ToLower(strings.TrimSpace(strings.Split(pref, ",")[0]))
		aliases, ok := cityAliases[key]
		if !ok {
			aliases = []string{key}
		}
		if key != "" && !strings.HasPrefix(key, "remote") && mentions(postingLoc, aliases) {
			return pref
		}
	}
	return ""
}

func wantsRemote(preferred []string) bool {
	return mentions(strings.Join(preferred, " "), []string{"remote"})
}

func inIndia(s string) bool { return mentions(s, indiaWords) }

// ---------- factors ----------

func f(name string, score int, why string) core.FitFactor {
	return core.FitFactor{Factor: name, Score: max(0, min(20, score)), Rationale: why}
}

func count(ev []core.Evidence, strength string) int {
	n := 0
	for _, e := range ev {
		if e.Strength == strength {
			n++
		}
	}
	return n
}

// Score computes the five factors. The posting must have at least a title.
func Score(p core.Profile, in core.Intent, post core.Posting) Result {
	items := append(append([]string{}, post.Requirements...), post.Responsibilities...)
	ev := Correlate(p, items)
	prefEv := Correlate(p, post.Qualifications)
	cov := coverage(ev)
	r := Result{Evidence: append(ev, prefEv...), Coverage: cov}

	// Experience: 10 for years, 10 for depth.
	minYears := -1
	if post.MinYears != nil {
		minYears = *post.MinYears
	}
	var yrs int
	var yrsWhy string
	switch {
	case minYears < 0:
		yrs, yrsWhy = 7, fmt.Sprintf("The posting doesn't state years; you have %.0f.", p.TotalYears)
	case p.TotalYears >= float64(minYears):
		yrs, yrsWhy = 10, fmt.Sprintf("It asks for %d+ years; you have %.0f.", minYears, p.TotalYears)
	case p.TotalYears >= float64(minYears)-2:
		yrs, yrsWhy = 6, fmt.Sprintf("It asks for %d+ years; you have %.0f, slightly short.", minYears, p.TotalYears)
	default:
		yrs, yrsWhy = 2, fmt.Sprintf("It asks for %d+ years; you have %.0f.", minYears, p.TotalYears)
	}
	depth := int(math.Round(cov * 10))
	if len(ev) == 0 {
		depth = 4
	}
	r.Factors = append(r.Factors, f("experience", yrs+depth, fmt.Sprintf("%s Your roles and achievements directly cover %d of %d requirements and responsibilities, and partly cover %d.",
		yrsWhy, count(ev, "strong"), len(ev), count(ev, "partial"))))

	// Location.
	prefs := append(append([]string{}, in.Locations...), p.Location)
	loc := post.Location
	mode := post.WorkMode
	var locScore int
	var locWhy string
	city := preferredCity(loc, prefs)
	switch {
	case strings.TrimSpace(loc) == "" && mode == "":
		locScore, locWhy = 10, "The posting doesn't say where the role is based; confirm before investing more time."
	case mode == "remote" && (inIndia(loc) || loc == "" || mentions(loc, []string{"apac", "asia", "anywhere", "global"})):
		locScore, locWhy = 20, "Remote, and open to candidates in India."
	case mode == "remote":
		locScore, locWhy = 12, fmt.Sprintf("Remote, but tied to %s; check that India-based candidates are eligible.", loc)
	case city != "":
		locScore, locWhy = 20, fmt.Sprintf("Based in %s, one of your preferred locations%s.", loc, modeNote(mode))
	case inIndia(loc):
		locScore, locWhy = 10, fmt.Sprintf("Based in %s%s, in India but not one of your preferred cities (%s).", loc, modeNote(mode), strings.Join(in.Locations, ", "))
		if wantsRemote(in.Locations) && mode == "hybrid" {
			locScore = 9
		}
	default:
		locScore, locWhy = 4, fmt.Sprintf("Based in %s%s, which would mean relocating abroad.", loc, modeNote(mode))
	}
	r.Factors = append(r.Factors, f("location", locScore, locWhy))

	// Seniority: the posting's level against the band of target roles.
	lvl := Level(post.Title)
	// The band comes from target roles in the posting's job family that state a
	// level ("Senior …", "Principal …"); a bare title such as "Solutions
	// Architect" says nothing about level.
	lo, hi := 99, 0
	for _, role := range in.Roles {
		if l := Level(role); l != 2 && (Function(post.Title) == "" || Function(role) == Function(post.Title)) {
			lo, hi = min(lo, l), max(hi, l)
		}
	}
	if in.Designation != "" {
		if l := Level(in.Designation + " x"); l != 2 {
			lo, hi = min(lo, l), max(hi, l)
		}
	}
	if hi == 0 {
		lo = Level(p.CurrentTitle)
		hi = lo
	}
	cur := Level(p.CurrentTitle)
	var senScore int
	var senWhy string
	switch {
	case lvl >= lo && lvl <= hi:
		senScore, senWhy = 20, fmt.Sprintf("%q is within your target level (%s).", post.Title, levelName(lo, hi))
	case lvl == hi+1:
		senScore, senWhy = 14, fmt.Sprintf("%q is a level above your target (%s): a stretch, so lead with scope and leadership.", post.Title, levelName(lo, hi))
	case lvl == lo-1:
		senScore, senWhy = 12, fmt.Sprintf("%q is a level below your target (%s) and your current %q.", post.Title, levelName(lo, hi), p.CurrentTitle)
	case lvl > hi:
		senScore, senWhy = 7, fmt.Sprintf("%q is well above your target (%s).", post.Title, levelName(lo, hi))
	default:
		senScore, senWhy = 5, fmt.Sprintf("%q is well below your level (%q).", post.Title, p.CurrentTitle)
	}
	if minYears >= 0 && p.TotalYears-float64(minYears) >= 9 && lvl <= cur {
		senScore -= 3
		senWhy += fmt.Sprintf(" It asks for only %d+ years against your %.0f, which suggests a smaller scope.", minYears, p.TotalYears)
	}
	r.Factors = append(r.Factors, f("seniority", senScore, senWhy))

	// Role alignment: evidence coverage (15) and same job family (5).
	fn := Function(post.Title)
	same := false
	for _, role := range in.Roles {
		same = same || (fn != "" && Function(role) == fn)
	}
	align := int(math.Round(cov * 15))
	alignWhy := fmt.Sprintf("Your evidence covers about %d%% of what the posting lists", int(math.Round(cov*100)))
	if len(ev) == 0 {
		align, alignWhy = 5, "The posting lists no requirements to compare against"
	}
	if same {
		align += 5
		alignWhy += fmt.Sprintf(", and it's a %s role like the ones you target.", fn)
	} else {
		alignWhy += fmt.Sprintf(", but %q is outside the job families you target.", post.Title)
	}
	r.Factors = append(r.Factors, f("role_alignment", align, alignWhy))

	// Interest and intent: title vs target roles (10), location (5), level (5).
	var ii int
	var iiWhy []string
	switch bestRole, sim := closestRole(post.Title, in.Roles); {
	case sim >= 0.6:
		ii += 10
		iiWhy = append(iiWhy, fmt.Sprintf("matches your target role %q", bestRole))
	case same:
		ii += 6
		iiWhy = append(iiWhy, "is in the same family as your target roles but not an exact match")
	default:
		iiWhy = append(iiWhy, "isn't one of your target roles")
	}
	if locScore >= 15 {
		ii += 5
		iiWhy = append(iiWhy, "the location suits you")
	} else if locScore >= 9 {
		ii += 2
		iiWhy = append(iiWhy, "the location is a compromise")
	} else {
		iiWhy = append(iiWhy, "the location doesn't suit you")
	}
	if senScore >= 14 {
		ii += 5
		iiWhy = append(iiWhy, "the level fits your goals")
	} else {
		iiWhy = append(iiWhy, "the level is off your goal")
	}
	r.Factors = append(r.Factors, f("interest_intent", ii, "The role "+strings.Join(iiWhy, "; ")+". Salary isn't compared: the posting rarely states it."))

	for _, ff := range r.Factors {
		r.Total += ff.Score
	}
	r.Strengths, r.Gaps = highlights(ev, prefEv, r.Factors)
	return r
}

func modeNote(mode string) string {
	if mode == "" {
		return ""
	}
	return " (" + mode + ")"
}

func levelName(lo, hi int) string {
	names := map[int]string{1: "associate", 2: "mid-level", 3: "senior", 4: "lead/staff", 5: "principal", 6: "director", 7: "VP"}
	if lo == hi {
		return names[lo]
	}
	return names[lo] + " to " + names[hi]
}

// closestRole returns the target role sharing most title words with title.
func closestRole(title string, roles []string) (string, float64) {
	tt := Tokens(title)
	best, bestSim := "", 0.0
	for _, r := range roles {
		rt := Tokens(r)
		inter := 0
		for t := range rt {
			if tt[t] {
				inter++
			}
		}
		if len(rt) == 0 {
			continue
		}
		if sim := float64(inter) / float64(len(rt)); sim > bestSim {
			best, bestSim = r, sim
		}
	}
	return best, bestSim
}

func highlights(ev, prefEv []core.Evidence, factors []core.FitFactor) (strengths, gaps []string) {
	seen := map[string]bool{}
	for _, e := range ev {
		if e.Strength == "strong" && len(strengths) < 4 && !seen[e.Evidence] {
			seen[e.Evidence] = true
			strengths = append(strengths, fmt.Sprintf("%s: %s", trim(e.Requirement, 90), e.Evidence))
		}
	}
	for _, list := range [][]core.Evidence{ev, prefEv} {
		for _, e := range list {
			if e.Strength == "missing" && len(gaps) < 4 {
				gaps = append(gaps, trim(e.Requirement, 160))
			}
		}
	}
	for _, ff := range factors {
		if ff.Score <= 8 && len(gaps) < 5 && (ff.Factor == "location" || ff.Factor == "seniority") {
			gaps = append(gaps, ff.Rationale)
		}
	}
	return strengths, gaps
}

// Bands is the verdict band scale, as the Claude queue runbook states it.
const Bands = "Strong ≥ 80, Good 75–79, Moderate 60–74, Weak < 60"

// Band names the verdict band for a total. The cut-offs are core-api's
// store.FitBand, which the web app shows; keep the two (and Bands) in step.
func Band(total int) string {
	switch {
	case total >= 80:
		return "strong"
	case total >= 75:
		return "good"
	case total >= 60:
		return "moderate"
	}
	return "weak"
}

// Fallback writes the verdict and suggestions without a model, used when the
// summary call is unavailable (no API credit, or it failed).
func Fallback(r Result, post core.Posting) (string, []string) {
	band := "a " + Band(r.Total) + " fit"
	low := r.Factors[0]
	for _, ff := range r.Factors {
		if ff.Score < low.Score {
			low = ff
		}
	}
	verdict := fmt.Sprintf("%s is %s (%d/100); the weakest factor is %s at %d/20.", post.Title, band, r.Total, strings.ReplaceAll(low.Factor, "_", " "), low.Score)
	var sugg []string
	for i, g := range r.Gaps {
		if i == 2 {
			break
		}
		sugg = append(sugg, fmt.Sprintf("Address “%s” with your closest concrete example in your CV or outreach.", trim(g, 120)))
	}
	if len(r.Strengths) > 0 {
		sugg = append(sugg, "Lead with: "+trim(r.Strengths[0], 160))
	}
	if low.Factor == "location" {
		sugg = append(sugg, "Confirm the location and work mode with the recruiter before investing more time.")
	}
	return verdict, sugg
}
