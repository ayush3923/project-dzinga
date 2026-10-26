// Package jd fetches a public job description and extracts its parts with plain
// code: no model calls. It reads, in order of reliability:
//
//  1. the applicant-tracking system's own JSON API (Greenhouse, Lever, Workday),
//  2. schema.org JobPosting data embedded in the page (most careers sites),
//  3. the page's headings and bullet lists.
//
// Fetching is restricted to public https hosts, with size and time limits.
package jd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Posting struct {
	URL              string
	Source           string // greenhouse_api | lever_api | workday_api | json_ld | html
	Title            string
	Company          string
	Location         string
	WorkMode         string // onsite | hybrid | remote | ""
	MinYears         int    // -1 when not stated
	Requirements     []string
	Qualifications   []string // preferred / nice to have
	Responsibilities []string
	About            string // first paragraph(s) about the team or product
}

var ErrNoPosting = errors.New("no job description found at this link")

const maxBody = 2 << 20 // 2 MiB

// Client returns an HTTP client that refuses non-public addresses (also after
// redirects), so a link can't be used to reach this machine or the local network.
func Client() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
			return fmt.Errorf("refusing to connect to non-public address %s", host)
		}
		return nil
	}}
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 || req.URL.Scheme != "https" {
				return errors.New("too many or insecure redirects")
			}
			return nil
		},
	}
}

func get(ctx context.Context, c *http.Client, u string, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Dzinga job-tracker; personal use)")
	req.Header.Set("Accept", accept)
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusTooManyRequests { // rate limited: wait once, then retry
		res.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(4 * time.Second):
		}
		if res, err = c.Do(req.Clone(ctx)); err != nil {
			return nil, err
		}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the page returned %s", res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, maxBody))
}

// Fetch reads a job description from a public https link.
func Fetch(ctx context.Context, c *http.Client, link string) (Posting, error) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return Posting{}, errors.New("only https links can be read")
	}
	// The ATS's JSON API first; if it fails, the page itself.
	if p, ok, err := fromATSAPI(ctx, c, u); ok && err == nil {
		p.URL = link
		return p, nil
	}
	body, err := get(ctx, c, link, "text/html,application/xhtml+xml")
	if err != nil {
		return Posting{}, err
	}
	p, err := Parse(string(body))
	p.URL = link
	return p, err
}

// ---------- ATS APIs ----------

var (
	reGreenhouse = regexp.MustCompile(`^(?:boards|job-boards)(\.eu)?\.greenhouse\.io$`)
	reGHPath     = regexp.MustCompile(`^/([^/]+)/jobs/(\d+)`)
	reLeverPath  = regexp.MustCompile(`^/([^/]+)/([0-9a-f-]{20,})`)
	reWorkday    = regexp.MustCompile(`^([\w-]+)\.(wd\d+)\.myworkdayjobs\.com$`)
	reWDPath     = regexp.MustCompile(`^/(?:[a-z]{2}-[A-Z]{2}/)?([^/]+)(/job/.+)$`)
)

func fromATSAPI(ctx context.Context, c *http.Client, u *url.URL) (Posting, bool, error) {
	host := strings.ToLower(u.Hostname())
	switch {
	case reGreenhouse.MatchString(host):
		m := reGHPath.FindStringSubmatch(u.Path)
		if m == nil {
			return Posting{}, false, nil
		}
		// The same API serves EU-hosted boards (job-boards.eu.greenhouse.io).
		body, err := get(ctx, c, fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs/%s", m[1], m[2]), "application/json")
		if err != nil {
			return Posting{}, true, err
		}
		var j struct {
			Title    string `json:"title"`
			Content  string `json:"content"` // HTML, entity-escaped
			Location struct {
				Name string `json:"name"`
			} `json:"location"`
			Company string `json:"company_name"`
		}
		if err := json.Unmarshal(body, &j); err != nil {
			return Posting{}, true, ErrNoPosting
		}
		p := fromHTMLDescription(html.UnescapeString(j.Content))
		p.Source, p.Title, p.Location, p.Company = "greenhouse_api", j.Title, j.Location.Name, j.Company
		p.WorkMode = workMode(p.Location + " " + j.Content)
		return p, true, nil
	case host == "jobs.lever.co":
		m := reLeverPath.FindStringSubmatch(u.Path)
		if m == nil {
			return Posting{}, false, nil
		}
		body, err := get(ctx, c, fmt.Sprintf("https://api.lever.co/v0/postings/%s/%s", m[1], m[2]), "application/json")
		if err != nil {
			return Posting{}, true, err
		}
		var j struct {
			Text        string `json:"text"`
			Description string `json:"description"`
			Lists       []struct {
				Text    string `json:"text"`
				Content string `json:"content"`
			} `json:"lists"`
			Categories struct {
				Location string `json:"location"`
			} `json:"categories"`
			WorkplaceType string `json:"workplaceType"`
		}
		if err := json.Unmarshal(body, &j); err != nil {
			return Posting{}, true, ErrNoPosting
		}
		var b strings.Builder
		b.WriteString(j.Description)
		for _, l := range j.Lists {
			fmt.Fprintf(&b, "<h3>%s</h3><ul>%s</ul>", l.Text, l.Content)
		}
		p := fromHTMLDescription(b.String())
		p.Source, p.Title, p.Location, p.Company = "lever_api", j.Text, j.Categories.Location, m[1]
		p.WorkMode = workMode(j.WorkplaceType + " " + j.Categories.Location)
		return p, true, nil
	case reWorkday.MatchString(host):
		hm := reWorkday.FindStringSubmatch(host)
		pm := reWDPath.FindStringSubmatch(u.Path)
		if pm == nil {
			return Posting{}, false, nil
		}
		api := fmt.Sprintf("https://%s/wday/cxs/%s/%s%s", host, hm[1], pm[1], pm[2])
		body, err := get(ctx, c, api, "application/json")
		if err != nil {
			return Posting{}, true, err
		}
		var j struct {
			Info struct {
				Title       string `json:"title"`
				Description string `json:"jobDescription"`
				Location    string `json:"location"`
				Remote      string `json:"remoteType"`
			} `json:"jobPostingInfo"`
			Org struct {
				Name string `json:"name"`
			} `json:"hiringOrganization"`
		}
		if err := json.Unmarshal(body, &j); err != nil || j.Info.Description == "" {
			return Posting{}, true, ErrNoPosting
		}
		p := fromHTMLDescription(j.Info.Description)
		p.Source, p.Title, p.Location, p.Company = "workday_api", j.Info.Title, j.Info.Location, j.Org.Name
		p.WorkMode = workMode(j.Info.Remote + " " + j.Info.Location)
		return p, true, nil
	}
	return Posting{}, false, nil
}

// ---------- page parsing ----------

var (
	reLinkedInTitle  = regexp.MustCompile(`^(.+?) hiring (.+?) in (.+?)\s*\|\s*LinkedIn.*$`) // Acme hiring PM in Pune | LinkedIn
	reAtDashTitle    = regexp.MustCompile(`^(.+?) at (.+?)\s+[—–-]\s+(.+)$`)                 // PM at Acme — Pune
	reSiteSuffix     = regexp.MustCompile(`\s*\|\s*LinkedIn.*$`)
	reApplicationFor = regexp.MustCompile(`^Job Application for (.+?) at (.+)$`) // Greenhouse page title
)

var (
	reLDJSON  = regexp.MustCompile(`(?is)<script[^>]+type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	reTag     = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace   = regexp.MustCompile(`\s+`)
	reBlocks  = regexp.MustCompile(`(?is)<(script|style|noscript|svg|nav|footer|header)[^>]*>.*?</(script|style|noscript|svg|nav|footer|header)>`)
	reYears   = regexp.MustCompile(`(?i)(\d{1,2})\s*\+?\s*(?:-\s*\d{1,2}\s*)?(?:\+\s*)?years?`)
	reHeading = regexp.MustCompile(`(?is)<(h[1-6]|strong|b|p)[^>]*>(.*?)</(?:h[1-6]|strong|b|p)>|<li[^>]*>(.*?)</li>`)
)

func text(s string) string {
	return strings.TrimSpace(reSpace.ReplaceAllString(html.UnescapeString(reTag.ReplaceAllString(s, " ")), " "))
}

// Parse extracts a posting from an HTML page.
func Parse(page string) (Posting, error) {
	for _, m := range reLDJSON.FindAllStringSubmatch(page, -1) {
		if p, ok := fromJSONLD(m[1]); ok {
			return p, nil
		}
	}
	p := fromHTMLDescription(reBlocks.ReplaceAllString(descriptionRegion(page), " "))
	if len(p.Requirements)+len(p.Responsibilities)+len(p.Qualifications) == 0 {
		return p, ErrNoPosting
	}
	p.Source = "html"
	if m := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`).FindStringSubmatch(page); m != nil {
		p.Title = text(m[1])
	}
	// LinkedIn: "Acme hiring Senior PM in Bengaluru, Karnataka, India | LinkedIn"
	if m := reLinkedInTitle.FindStringSubmatch(p.Title); m != nil {
		p.Company, p.Title, p.Location = m[1], m[2], m[3]
	} else if m := reAtDashTitle.FindStringSubmatch(reSiteSuffix.ReplaceAllString(p.Title, "")); m != nil {
		p.Title, p.Company, p.Location = m[1], m[2], m[3]
	} else if m := reApplicationFor.FindStringSubmatch(p.Title); m != nil {
		p.Title, p.Company = m[1], m[2]
	} else if i := strings.LastIndexAny(p.Title, "|–"); i > 0 { // "Senior PM | Careers at Acme"
		p.Title = strings.TrimSpace(p.Title[:i])
	}
	if p.WorkMode == "" {
		p.WorkMode = workMode(p.Location + " " + strings.Join(p.Requirements, " "))
	}
	return p, nil
}

// descriptionRegion narrows a page to its job description when the site marks
// it, so sidebars ("similar jobs", "people also viewed") aren't read as requirements.
func descriptionRegion(page string) string {
	for _, marker := range []string{
		`class="show-more-less-html__markup`,         // LinkedIn
		`class="job__description`,                    // Greenhouse
		`class="posting-page`,                        // Lever
		`data-automation-id="jobPostingDescription"`, // Workday
		`id="job-description"`, `class="job-description`, `id="jobDescriptionText"`,
	} {
		if i := strings.Index(page, marker); i >= 0 {
			rest := page[i:]
			for _, end := range []string{"</section>", "<footer", `class="similar-jobs`, `class="people-also-viewed`} {
				if j := strings.Index(rest, end); j > 0 {
					rest = rest[:j]
				}
			}
			return "<div " + rest
		}
	}
	return page
}

func fromJSONLD(raw string) (Posting, bool) {
	var v any
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &v) != nil {
		return Posting{}, false
	}
	var jp map[string]any
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			if jp != nil {
				return
			}
			if ty, _ := t["@type"].(string); ty == "JobPosting" {
				jp = t
				return
			}
			if g, ok := t["@graph"]; ok {
				walk(g)
			}
		}
	}
	walk(v)
	if jp == nil {
		return Posting{}, false
	}
	str := func(k string) string { s, _ := jp[k].(string); return s }
	p := fromHTMLDescription(html.UnescapeString(str("description")))
	p.Source, p.Title = "json_ld", text(str("title"))
	if org, ok := jp["hiringOrganization"].(map[string]any); ok {
		p.Company, _ = org["name"].(string)
	}
	p.Location = jsonLDLocation(jp["jobLocation"])
	if t, _ := jp["jobLocationType"].(string); strings.EqualFold(t, "TELECOMMUTE") {
		p.WorkMode = "remote"
	} else {
		p.WorkMode = workMode(p.Location + " " + str("description"))
	}
	for _, k := range []string{"qualifications", "skills", "educationRequirements"} {
		if s := str(k); s != "" && len(p.Requirements) == 0 {
			p.Requirements = bullets(s)
		}
	}
	if s := str("responsibilities"); s != "" && len(p.Responsibilities) == 0 {
		p.Responsibilities = bullets(s)
	}
	if exp, ok := jp["experienceRequirements"].(map[string]any); ok {
		if months, ok := exp["monthsOfExperience"].(float64); ok && months > 0 {
			p.MinYears = int(months / 12)
		}
	}
	return p, p.Title != ""
}

func jsonLDLocation(v any) string {
	switch t := v.(type) {
	case []any:
		var parts []string
		for _, e := range t {
			if s := jsonLDLocation(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "; ")
	case map[string]any:
		if a, ok := t["address"].(map[string]any); ok {
			var parts []string
			for _, k := range []string{"addressLocality", "addressRegion", "addressCountry"} {
				switch x := a[k].(type) {
				case string:
					parts = append(parts, x)
				case map[string]any:
					if n, _ := x["name"].(string); n != "" {
						parts = append(parts, n)
					}
				}
			}
			return strings.Join(parts, ", ")
		}
	}
	return ""
}

func bullets(s string) []string {
	if strings.Contains(s, "<li") {
		return fromHTMLDescription("<h3>x</h3>" + s).Requirements
	}
	var out []string
	for _, line := range regexp.MustCompile(`\n|•|·|;\s`).Split(text(s), -1) {
		if l := strings.TrimSpace(line); len(l) > 3 {
			out = append(out, l)
		}
	}
	return out
}

type section int

const (
	secNone section = iota
	secAbout
	secReq
	secPref
	secResp
	secOther
)

var (
	headReq   = regexp.MustCompile(`(?i)requirement|qualification|what you('|’)?ll (bring|need)|what we('|’)?re looking for|you (have|bring|are)|must[- ]have|who you are|your (background|profile|experience)|skills|about you|ideal candidate|basic|minimum`)
	headPref  = regexp.MustCompile(`(?i)prefer|nice[- ]to[- ]have|bonus|plus|desired|good to have`)
	headResp  = regexp.MustCompile(`(?i)responsibilit|what you('|’)?ll do|what you will do|the role|your role|your impact|in this role|day[- ]to[- ]day|you will|key (duties|accountabilities)|what you('|’)?ll own`)
	headAbout = regexp.MustCompile(`(?i)about (the|our|us|this)|who we are|the team|overview|company description|job description|summary`)
	headOther = regexp.MustCompile(`(?i)benefit|perks|what we offer|compensation|salary|equal opportunity|eeo|privacy|apply|how to|our values|life at`)
)

// fromHTMLDescription walks headings and list items, filing each bullet under the
// section its nearest heading announces.
func fromHTMLDescription(h string) Posting {
	p := Posting{MinYears: -1}
	cur := secNone
	var about []string
	for _, m := range reHeading.FindAllStringSubmatch(h, -1) {
		if m[3] != "" { // <li>
			item := text(m[3])
			if len(item) < 4 || len(item) > 600 {
				continue
			}
			switch cur {
			case secReq, secNone:
				p.Requirements = append(p.Requirements, item)
			case secPref:
				p.Qualifications = append(p.Qualifications, item)
			case secResp:
				p.Responsibilities = append(p.Responsibilities, item)
			}
			continue
		}
		t := text(m[2])
		if t == "" {
			continue
		}
		isHeading := m[1] != "p" || (len(t) < 70 && (strings.HasSuffix(t, ":") || strings.EqualFold(t, strings.TrimSuffix(t, ":"))))
		if isHeading && len(t) < 90 {
			switch {
			case headOther.MatchString(t):
				cur = secOther
			case headPref.MatchString(t):
				cur = secPref
			case headResp.MatchString(t):
				cur = secResp
			case headReq.MatchString(t):
				cur = secReq
			case headAbout.MatchString(t):
				cur = secAbout
			}
			if m[1] != "p" {
				continue
			}
		}
		if (cur == secAbout || cur == secNone) && len(t) > 60 && len(about) < 3 {
			about = append(about, t)
		}
	}
	p.About = strings.Join(about, " ")
	if len(p.About) > 1200 {
		p.About = p.About[:1200]
	}
	all := strings.Join(append(append([]string{}, p.Requirements...), p.Qualifications...), " ")
	if m := reYears.FindStringSubmatch(all); m != nil {
		p.MinYears, _ = strconv.Atoi(m[1])
	}
	p.Requirements, p.Qualifications, p.Responsibilities = cap20(p.Requirements), cap20(p.Qualifications), cap20(p.Responsibilities)
	return p
}

func cap20(s []string) []string {
	if len(s) > 20 {
		return s[:20]
	}
	return s
}

func workMode(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "hybrid"):
		return "hybrid"
	case strings.Contains(l, "remote") || strings.Contains(l, "work from home") || strings.Contains(l, "telecommute"):
		return "remote"
	case strings.Contains(l, "on-site") || strings.Contains(l, "onsite") || strings.Contains(l, "in office") || strings.Contains(l, "in-office"):
		return "onsite"
	}
	return ""
}
