// Package classify decides whether an email is about one of the user's job
// applications, and if so extracts the company, role and stage. It is rules-based
// (no model calls) so it is free, fast and deterministic; an LLM pass can be layered
// on later for the emails it can't decide.
package classify

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Message is a provider-neutral email.
type Message struct {
	FromName string
	FromAddr string
	Subject  string
	Text     string   // plain text body (HTML already stripped)
	Links    []string // https links found in the body
}

type Result struct {
	Kind    string // applied | assessment | interview | offer | rejected | update
	Company string
	Role    string
}

// Applicant-tracking systems and job boards that send application emails.
var atsDomains = []string{
	"greenhouse.io", "greenhouse-mail.io", "lever.co", "hire.lever.co", "myworkday.com", "myworkdayjobs.com",
	"workday.com", "ashbyhq.com", "smartrecruiters.com", "icims.com", "jobvite.com", "taleo.net",
	"successfactors.com", "successfactors.eu", "workable.com", "workablemail.com", "bamboohr.com",
	"breezy.hr", "recruitee.com", "jazzhr.com", "zohorecruit.com", "zoho.com", "freshteam.com",
	"darwinbox.in", "darwinbox.com", "keka.com", "naukri.com", "naukrigulf.com", "instahyre.com",
	"iimjobs.com", "hirist.com", "hirist.tech", "foundit.in", "monsterindia.com", "indeed.com",
	"indeedemail.com", "linkedin.com", "wellfound.com", "angel.co", "glassdoor.com", "ziprecruiter.com",
	"dice.com", "hackerrank.com", "hackerrankforwork.com", "codility.com", "codesignal.com", "mettl.com",
	"hackerearth.com", "testgorilla.com", "oraclecloud.com", "eightfold.ai", "phenom.com", "avature.net",
	"cutshort.io", "apna.co", "shine.com", "turing.com", "uplers.com", "toptal.com",
}

// Display names that are the ATS/job board itself rather than the employer.
var platformNames = map[string]bool{
	"greenhouse": true, "lever": true, "workday": true, "ashby": true, "smartrecruiters": true,
	"icims": true, "jobvite": true, "taleo": true, "workable": true, "linkedin": true, "naukri": true,
	"naukri.com": true, "indeed": true, "instahyre": true, "iimjobs": true, "hirist": true,
	"foundit": true, "glassdoor": true, "wellfound": true, "hackerrank": true, "codility": true,
	"no-reply": true, "noreply": true, "notifications": true, "do not reply": true,
}

// Words that are never a company even though they appear where one would.
var genericNames = map[string]bool{"no-reply": true, "noreply": true, "notifications": true, "do not reply": true, "careers": true, "recruiting": true}

var (
	// Decisions often come with a soft subject ("Thank you for your interest in X"), so
	// those words count too; Classify still reads the body before anything is kept.
	subjectHint = re(`\b(application|applying|applied|interview|candidacy|candidate|position|opportunity|assessment|offer|hiring|recruit|role|` +
		`your interest|interest in|regret|unfortunately|next steps?|status|decision|outcome)\b`)

	// Job alerts, recommendations and marketing: not applications.
	noise = re(`job alert|jobs? (for|matching) you|jobs? you (may|might)|recommended (jobs|for you)|new jobs|` +
		`is hiring|are hiring|apply now|top job picks|job recommendations|jobs? similar to|webinar|newsletter|` +
		`profile views?|who viewed|people (also )?viewed|salary insights|premium|% off|upgrade|verify your|otp|password reset|` +
		`job seekers?|career fair|hiring event|interview (questions|tips|playbook|prep guide)|job vacancies|immediate (job|openings)|` +
		`exclusive:|^\[?(newsletter|digest)`)
	noiseSenders = re(`jobalerts?-noreply@|jobs-listings@|jobs-noreply@.*indeed|alert@indeed|info@naukri\.com|` +
		`naukrialerts|recommendations@|newsletters?@|marketing@|news@|digest@|docusign\.(net|com)$`)

	// Forums and digests talk about offers and interviews but aren't the user's applications.
	noiseNames = re(`community|digest|newsletter|forum|bowls?\b`)
	// Phrases that make an extracted "company" actually a job title.
	titleWords = re(`\b(` + titleList + `)\b`)
	// A role must name a job; this rejects fragments like "future if a" or a date.
	roleWords = re(`\b(` + titleList + `|sales|marketing|account|qa|sre|devops|data|` +
		`researcher|writer|recruiter|owner|coordinator|representative|strategist|tester|accountant|partner|support|` +
		`success|operations|finance|legal)\b`)
	roleJunk = re(`\b(monday|tuesday|wednesday|thursday|friday|saturday|sunday|january|february|march|april|may|june|` +
		`july|august|september|october|november|december|reminder|scheduled|asia/|20\d\d)\b`)
	freemail = map[string]bool{"gmail": true, "yahoo": true, "outlook": true, "hotmail": true, "live": true, "icloud": true, "rediffmail": true, "proton": true}
	// Sender domains whose name differs from the brand people know.
	domainAlias = map[string]string{"facebook": "Meta", "amazon": "Amazon", "lamresearch": "Lam Research", "openai": "OpenAI"}
	legalSuffix = re(`[,\s]+(?:&\s*co\.?|inc\.?|ltd\.?|llc|limited|pvt\.?|private|corp\.?|corporation|plc)$`)

	offer    = re(`pleased to (extend|offer)|offer letter|offer of employment|extend (you )?an offer|happy to offer|formal offer`)
	rejected = re(`unfortunately|not (to )?(be )?mov(e|ing) forward|won'?t be moving forward|decided to (pursue|proceed|move forward) with other|` +
		`move forward with other candidates|regret to inform|not been selected|were not selected|position has been filled|` +
		`other candidates whose|not a match at this time|decided not to proceed|no longer under consideration|will not be progressing`)
	interview = re(`invit\w* (you )?(to|for) (an? |the )?(\w+ )?interview|interview (invitation|invite|confirm\w*|schedul\w*|request)|` +
		`schedule (an? |the |your )?(\w+ )?(interview|call|chat|conversation)|your (upcoming )?interview|phone screen|` +
		`availability for (an? )?(interview|call)|calendly\.com|book a time|next round|technical round|hiring manager (round|call|interview)|` +
		`(onsite|on-site|in-person|virtual|video|phone|technical|final|panel|loop)\s+(\w+\s+)?interview (for|with|on|at)\b`)
	assessment = re(`assessment|coding (challenge|test|exercise)|take[- ]home|online test|aptitude test|` +
		`(hackerrank|codility|codesignal|mettl|hackerearth|testgorilla)\b.{0,40}\b(test|challenge|invite|invitation)`)
	// "Applied" needs an explicit acknowledgement. Looser phrases ("thank you for your
	// interest", "you applied", "application submitted") let in marketing and loan mail.
	applied = re(`thank(s| you) for (applying|your (job )?application|submitting your application)|` +
		`(we('ve| have)? |has been )?received your (job )?application|receipt of your application|` +
		`your application (has been|was|is) received|application (has been )?received|` +
		`your application was sent to|successfully applied (to|for)`)
	// Applications that aren't jobs: loans, cards, insurance, admissions, KYC…
	nonJob = re(`\b(loan|credit card|card|insurance|policy|mutual fund|demat|kyc|visa|admission|account opening|emi|mortgage|cashback)\b`)
	viewed = re(`application (was )?viewed|viewed your application|application status|update on your application|regarding your application`)
)

const titleList = `manager|engineer|developer|architect|analyst|director|lead|head of|principal|senior|staff|` +
	`specialist|consultant|designer|scientist|associate|intern|vice president|officer|executive|administrator|program|product`

func re(p string) *regexp.Regexp { return regexp.MustCompile(`(?i)` + p) }

func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return strings.ToLower(addr[i+1:])
	}
	return ""
}

func isATS(addr string) bool {
	d := domainOf(addr)
	for _, a := range atsDomains {
		if d == a || strings.HasSuffix(d, "."+a) {
			return true
		}
	}
	return false
}

// LooksRelevant is a cheap pre-filter on headers only, so the IMAP connector downloads
// full bodies just for likely candidates.
func LooksRelevant(fromAddr, subject string) bool {
	if noiseSenders.MatchString(fromAddr) {
		return false
	}
	return isATS(fromAddr) || subjectHint.MatchString(subject)
}

// Classify returns ok=false for anything that isn't about one of the user's applications.
func Classify(m Message) (Result, bool) {
	if noiseSenders.MatchString(m.FromAddr) || noiseNames.MatchString(m.FromName) {
		return Result{}, false
	}
	if noise.MatchString(m.Subject) && !re(`your application`).MatchString(m.Subject) {
		return Result{}, false
	}
	// Job boards also send posts, newsletters and job matches; only their notices about
	// the user's own applications count.
	if d := domainOf(m.FromAddr); (d == "linkedin.com" || strings.HasSuffix(d, ".linkedin.com") || strings.HasSuffix(d, "indeed.com")) &&
		!re(`\byour application\b|\bapplied\b|application (sent|received|submitted|viewed)`).MatchString(m.Subject) {
		return Result{}, false
	}
	if nonJob.MatchString(m.Subject) {
		return Result{}, false
	}
	// A vague subject ("Update on your application") defers to a clearer body.
	kind := kindOf(m.Subject, true)
	if kind == "" || kind == "update" {
		if b := kindOf(firstChars(m.Text, 3000), false); b != "" {
			kind = b
		}
	}
	if kind == "" {
		return Result{}, false
	}
	company, role := extract(m)
	return Result{Kind: kind, Company: company, Role: role}, true
}

// kindOf checks the subject first (subjects are crisp), then the body. In bodies,
// "applied" is checked before "interview" because acknowledgement emails often say
// "we'll be in touch to schedule an interview".
func kindOf(s string, isSubject bool) string {
	switch {
	case s == "":
		return ""
	case offer.MatchString(s):
		return "offer"
	case rejected.MatchString(s):
		return "rejected"
	case isSubject && interview.MatchString(s):
		return "interview"
	case isSubject && assessment.MatchString(s):
		return "assessment"
	case applied.MatchString(s):
		return "applied"
	case !isSubject && interview.MatchString(s):
		return "interview"
	case !isSubject && assessment.MatchString(s):
		return "assessment"
	case viewed.MatchString(s):
		return "update"
	}
	return ""
}

var (
	subjCompany = []*regexp.Regexp{
		re(`application (?:to|at|with) ([^-–—|,!]+?)\s+[-–—|]\s+.+$`),                             // "…application to GitLab - Senior SA"
		regexp.MustCompile(`your ([A-Z][\w&.']+(?: [A-Z][\w&.']+){0,2}) (?:job )?[Aa]pplication`), // "your Target job application", "your Accenture Application"
		re(`your application (?:to|with|at) ([^-–—|,!:]+)`),
		re(`your application was (?:sent|submitted) to (.+)$`),
		re(`your application was viewed by (.+)$`),
		re(`(?:applying|application|interest|applied)(?: to| for)? .+? (?:at|with) ([^,!|.]+)`),
		re(`thank(?:s| you) for (?:applying|your application|your interest)(?: to| at| with| in)(?: the)? ([^,!|.]+)`),
		re(`(?:interview|assessment)(?: invitation| request)? (?:with|from|at) ([^,!|.]+)`),
		re(`(?:interview|assessment)(?: invitation| request)?\s*[-|:]\s*([^|,]+?)\s*(?:\||$)`),
		re(`^([^|:\-]{2,40}?)\s*[|:\-]\s*(?:thank|your application|application|interview|update)`),
		re(`^(?:your )?update from (.+)$`),
	}
	bodyCompany = []*regexp.Regexp{
		re(`(?:applying|application|interest)(?: to| for| in) (?:the )?[^.\n]{2,80}? (?:position|role|opening|job) (?:at|with) ([A-Z][\w&'.\- ]{1,40})`),
		re(`thank(?:s| you) for (?:applying|your interest)(?: to| at| in| with) ([A-Z][\w&'.\- ]{1,40})`),
		re(`(?:role|position|opening) (?:at|with) ([A-Z][\w&'.\- ]{1,40})`),
		re(`(?:welcome to|join) (?:the )?([A-Z][\w&'.\- ]{1,30}) (?:team|family)`),
	}
	subjRole = []*regexp.Regexp{
		re(`application (?:to|at|with) [^-–—|,!]+?\s+[-–—|]\s+(.+)$`),
		re(`application (?:for|to)(?: the)? (.+?) (?:position |role |opening )?(?:at|with|-|\|)`),
		re(`(?:applying|applied) (?:for|to)(?: the)? (.+?) (?:position |role )?(?:at|with)`),
		re(`(?:interview|application|assessment)[^|]*\|\s*(.+)$`),
		re(`your application (?:to|with|at) [^—–|]+?\s+[—–|-]\s+(.+)$`),
		re(`^(.+?) at .+?(?: - |$)`),
	}
	bodyRole = []*regexp.Regexp{
		re(`(?:for|to|in) the (?:position of |role of )?([^.\n]{2,80}?) (?:position|role|opening|job)\b`),
		re(`(?:position|role|job title|job)\s*[:\-]\s*([^\n]{2,80})`),
		re(`(?:application|applying) for (?:the )?([^.\n]{2,80}?) (?:at|with)\b`),
		re(`(?m)^\s*(?:.*?\. )?([^\n·.]{2,80}?) · [^\n·]+ · `), // LinkedIn: "Role · Company · City"
	}
	companySuffix = re(`\s*(careers?|recruiting|recruitment|talent acquisition|talent( team)?|hiring team|hr( team)?|jobs|team|people( team)?|` +
		`workday|greenhouse|lever|icims|smartrecruiters|taleo|` +
		`via [\w.]+|@ ?\w+|notifications?|no-?reply)\s*$`)
	roleNoise = re(`^(your|our|this|the|an?|job|position|role|application|opportunity)$`)
)

func extract(m Message) (company, role string) {
	subject := strings.TrimSpace(re(`^(re|fwd?|fw)\s*:\s*`).ReplaceAllString(m.Subject, ""))
	company = firstMatch(subjCompany, subject, cleanCompany)
	// "Thank you for applying to Senior Product Manager": that's the role, not the company.
	var titleFromSubject string
	if titleWords.MatchString(company) {
		titleFromSubject, company = cleanRole(company), ""
	}
	if company == "" {
		company = firstMatch(bodyCompany, firstChars(m.Text, 3000), cleanCompany)
	}
	if company == "" || titleWords.MatchString(company) {
		company = companyFromSender(m)
	}
	if titleWords.MatchString(company) {
		company = ""
	}
	// A company's own domain (guidewire.com) beats names pulled from the subject or the
	// sender's display name, which are often a recruiter ("Kavitha S") or a word
	// ("REMINDER"). The extracted name is kept only when it matches the domain, for its
	// nicer spelling ("Lam Research" for lamresearch.com).
	if dc := domainCompany(m.FromAddr); dc != "" && !sameName(company, dc) {
		company = dc
	}
	role = firstMatch(subjRole, subject, cleanRole)
	if role == "" || strings.EqualFold(role, company) {
		role = titleFromSubject
	}
	if role == "" || strings.EqualFold(role, company) {
		role = firstMatch(bodyRole, firstChars(m.Text, 3000), cleanRole)
	}
	if strings.EqualFold(role, company) {
		role = ""
	}
	if company == "" {
		company = "Unknown company"
	}
	return company, role
}

func firstMatch(pats []*regexp.Regexp, s string, clean func(string) string) string {
	for _, p := range pats {
		if m := p.FindStringSubmatch(s); m != nil {
			if v := clean(m[len(m)-1]); v != "" {
				return v
			}
		}
	}
	return ""
}

func cleanCompany(s string) string {
	if i := strings.Index(s, "@"); i > 0 { // "Cisco@myworkday.com"
		s = s[:i]
	}
	s = strings.ReplaceAll(html.UnescapeString(s), "\u00a0", " ") // "LinkedIn&nbsp"
	s = strings.TrimSpace(strings.Trim(s, ` "'!.,:;-|`))
	if i := strings.Index(s, ". "); i > 0 { // "Fabrikam. Unfortunately…"; before suffix stripping
		s = s[:i]
	}
	s = re(`^(?:(?:a |an )?careers? (?:at|with) |the )`).ReplaceAllString(s, "")
	for _, sep := range []string{" and the ", " - ", " – ", " — "} {
		if i := strings.Index(strings.ToLower(s), sep); i > 0 {
			s = s[:i]
		}
	}
	for i := 0; i < 2; i++ {
		s = strings.TrimSpace(legalSuffix.ReplaceAllString(s, "")) // "JPMorgan Chase & Co" = "JPMorganChase"
	}
	for i := 0; i < 3; i++ {
		s = strings.TrimSpace(companySuffix.ReplaceAllString(s, ""))
	}
	if i := strings.IndexAny(s, "!|\n"); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "" || len(s) > 60 || !hasLetter(s) || genericNames[strings.ToLower(s)] {
		return ""
	}
	if wc := len(strings.Fields(s)); wc > 6 {
		return ""
	}
	return s
}

func cleanRole(s string) string {
	s = strings.TrimSpace(strings.Trim(s, ` "'!.,:;-|`))
	s = re(`^(the|a|an)\s+`).ReplaceAllString(s, "")
	s = re(`^(?:the\s+)?(?:role|position|post) of\s+`).ReplaceAllString(s, "") // "role of Principal PM"
	s = re(`^R?\d{4,}\s+`).ReplaceAllString(s, "")                             // "R171043 Principal Product Manager"
	s = re(`\s*\((?:ref|req|job)[^)]*\)?\s*$`).ReplaceAllString(s, "")         // "(Ref ID: 123)"
	s = re(`\s+[-–—]\s*\d{3,}.*$`).ReplaceAllString(s, "")                     // "- 2601234"
	s = re(`\s+(position|role|opening|job)$`).ReplaceAllString(s, "")
	if s == "" || len(s) > 80 || len(strings.Fields(s)) > 8 || roleNoise.MatchString(s) || !hasLetter(s) {
		return ""
	}
	if re(`thank|application|your|we |you `).MatchString(s) || roleJunk.MatchString(s) || !roleWords.MatchString(s) {
		return ""
	}
	return s
}

// companyFromSender uses "Acme Recruiting <jobs@acme.com>" → "Acme", falling back to
// the sender's domain when it isn't a job platform.
func companyFromSender(m Message) string {
	name := m.FromName
	if mm := re(`^.+? (?:from|at|@) (.+)$`).FindStringSubmatch(name); mm != nil { // "Asha from Northwind"
		name = mm[1]
	}
	if c := cleanCompany(name); c != "" && !platformNames[strings.ToLower(c)] {
		return c
	}
	return domainCompany(m.FromAddr)
}

// domainCompany names the employer from a corporate sender domain, or "" for job
// platforms and personal mail. mail.amazon.jobs → Amazon, jobvite.nutanix.com → Nutanix.
func domainCompany(addr string) string {
	if isATS(addr) {
		return ""
	}
	parts := strings.Split(domainOf(addr), ".")
	if len(parts) < 2 {
		return ""
	}
	name := parts[len(parts)-2]
	if len(parts) >= 3 && (name == "co" || name == "com") { // acme.co.in
		name = parts[len(parts)-3]
	}
	if name == "" || freemail[name] || platformNames[name] {
		return ""
	}
	if a, ok := domainAlias[name]; ok {
		return a
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// sameName: "Lam Research" ~ "Lamresearch", "Kestrel AI" ~ "Kestrel".
func sameName(a, b string) bool {
	na, nb := AppKeyPart(a), AppKeyPart(b)
	return na != "" && nb != "" && (strings.Contains(na, nb) || strings.Contains(nb, na))
}

// AppKeyPart lower-cases and keeps only letters and digits.
func AppKeyPart(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func firstChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Snippet returns a short, whitespace-collapsed excerpt. Only this is stored, never the
// full body (data minimisation).
func Snippet(text string, n int) string {
	s := strings.Join(strings.Fields(text), " ")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	cut := s[:n]
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// Job-posting URL shapes on common applicant-tracking systems and job sites.
var jobLink = regexp.MustCompile(`(?i)^https://(` +
	`(boards|job-boards)\.greenhouse\.io/[^/]+/jobs/\d+|` +
	`jobs\.lever\.co/[^/]+/[0-9a-f-]{20,}|` +
	`jobs\.ashbyhq\.com/[^/]+/[0-9a-f-]{20,}|` +
	`[\w-]+\.(wd\d+\.)?myworkdayjobs\.com/.+/job/|` +
	`jobs\.smartrecruiters\.com/[^/]+/\d+|` +
	`[\w.-]+\.icims\.com/jobs/\d+|` +
	`apply\.workable\.com/[^/]+/j/|` +
	`(www\.)?fitboard\.in/job/[0-9a-f-]{20,}|` +
	`(www\.)?linkedin\.com/jobs/view/\d+|` +
	`(www\.)?naukri\.com/job-listings-|` +
	`(www\.)?amazon\.jobs/[a-z-]+/jobs/\d+|` +
	`jobs\.careers\.microsoft\.com/.+/job/\d+|` +
	`(www\.)?metacareers\.com/jobs/\d+|` +
	`[\w.-]+/(careers?|jobs?)/[^?]*\d{4,}` +
	`)`)

var trackingParam = regexp.MustCompile(`(?i)([?&])(utm_[a-z]+|gh_src|src|source|ref|trk|refId|trackingId)=[^&]*`)

// JobURL picks the first link that looks like a job posting, without tracking
// parameters. Unsubscribe and preference links never match the patterns above.
func JobURL(links []string) string {
	for _, l := range links {
		if jobLink.MatchString(l) && !re(`unsubscribe|preferences|privacy`).MatchString(l) {
			u := trackingParam.ReplaceAllString(l, "$1")
			u = regexp.MustCompile(`[?&]+$`).ReplaceAllString(regexp.MustCompile(`\?&+`).ReplaceAllString(u, "?"), "")
			return u
		}
	}
	return ""
}
