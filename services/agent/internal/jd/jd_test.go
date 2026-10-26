package jd

import (
	"context"
	"strings"
	"testing"
)

const jsonLDPage = `<html><head><title>Principal PM | Acme</title>
<script type="application/ld+json">{"@context":"https://schema.org","@graph":[{"@type":"Organization","name":"x"},
{"@type":"JobPosting","title":"Principal Product Manager, Growth","hiringOrganization":{"@type":"Organization","name":"Acme"},
"jobLocation":{"@type":"Place","address":{"addressLocality":"Bengaluru","addressRegion":"KA","addressCountry":"IN"}},
"description":"&lt;p&gt;Acme builds observability tools used by 10,000 engineering teams.&lt;/p&gt;&lt;h3&gt;What you'll do&lt;/h3&gt;&lt;ul&gt;&lt;li&gt;Own the growth roadmap for self-serve signups&lt;/li&gt;&lt;li&gt;Run pricing experiments&lt;/li&gt;&lt;/ul&gt;&lt;h3&gt;What we're looking for&lt;/h3&gt;&lt;ul&gt;&lt;li&gt;10+ years of product management experience&lt;/li&gt;&lt;li&gt;Experience with B2B SaaS and PLG&lt;/li&gt;&lt;/ul&gt;&lt;h3&gt;Nice to have&lt;/h3&gt;&lt;ul&gt;&lt;li&gt;FinOps background&lt;/li&gt;&lt;/ul&gt;&lt;h3&gt;Benefits&lt;/h3&gt;&lt;ul&gt;&lt;li&gt;Free lunch&lt;/li&gt;&lt;/ul&gt;"}]}
</script></head><body>…</body></html>`

func TestParseJSONLD(t *testing.T) {
	p, err := Parse(jsonLDPage)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "json_ld" || p.Title != "Principal Product Manager, Growth" || p.Company != "Acme" || p.Location != "Bengaluru, KA, IN" {
		t.Errorf("header: %+v", p)
	}
	if len(p.Responsibilities) != 2 || len(p.Requirements) != 2 || len(p.Qualifications) != 1 || p.MinYears != 10 {
		t.Errorf("sections: resp=%v req=%v pref=%v years=%d", p.Responsibilities, p.Requirements, p.Qualifications, p.MinYears)
	}
	if strings.Contains(strings.Join(p.Requirements, " "), "lunch") {
		t.Error("benefits leaked into requirements")
	}
	if !strings.Contains(p.About, "observability") {
		t.Errorf("about: %q", p.About)
	}
}

func TestParseHTMLFallback(t *testing.T) {
	page := `<html><title>Senior PM</title><nav><ul><li>Home</li></ul></nav><h2>Responsibilities</h2><ul><li>Define the AI platform strategy</li></ul>
	<p><strong>Requirements:</strong></p><ul><li>5-8 years in product</li><li>Hybrid role in Pune</li></ul></html>`
	p, err := Parse(page)
	if err != nil || p.Source != "html" || len(p.Responsibilities) != 1 || len(p.Requirements) != 2 || p.MinYears != 5 {
		t.Errorf("err=%v %+v", err, p)
	}
}

func TestParseNothing(t *testing.T) {
	if _, err := Parse(`<html><p>Sign in to continue</p></html>`); err != ErrNoPosting {
		t.Errorf("want ErrNoPosting, got %v", err)
	}
}

func TestFetchRefusesPrivateAndInsecure(t *testing.T) {
	c := Client()
	for _, u := range []string{"http://example.com/job", "https://127.0.0.1/job", "https://localhost/job", "https://10.0.0.5/jd", "file:///etc/passwd"} {
		if _, err := Fetch(context.Background(), c, u); err == nil {
			t.Errorf("%s fetched", u)
		}
	}
}

func TestParseLinkedInTitle(t *testing.T) {
	page := `<title>Wayfair hiring Senior Product Manager in Bengaluru, Karnataka, India | LinkedIn Jobs</title>
	<div><h3>Qualifications</h3><ul><li>9+ years in product</li><li>Hybrid, 3 days in office</li></ul></div>`
	p, err := Parse(page)
	if err != nil || p.Company != "Wayfair" || p.Title != "Senior Product Manager" || p.Location != "Bengaluru, Karnataka, India" || p.WorkMode != "hybrid" {
		t.Errorf("err=%v %+v", err, p)
	}
}

func TestParseOtherTitles(t *testing.T) {
	body := `<div><h3>Requirements</h3><ul><li>8+ years in product</li></ul></div>`
	p, _ := Parse(`<title>Senior Product Manager, Product Platform at Okta — Bengaluru, Karnataka, India | LinkedIn Jobs</title>` + body)
	if p.Title != "Senior Product Manager, Product Platform" || p.Company != "Okta" || p.Location != "Bengaluru, Karnataka, India" {
		t.Errorf("%+v", p)
	}
	p, _ = Parse(`<title>Job Application for Principal Product Manager at The Economist Group</title>` + body)
	if p.Title != "Principal Product Manager" || p.Company != "The Economist Group" {
		t.Errorf("%+v", p)
	}
}

func TestParseIgnoresSidebars(t *testing.T) {
	page := `<title>Acme hiring Product Manager in Pune | LinkedIn</title>
	<div class="show-more-less-html__markup"><strong>Requirements</strong><ul><li>Experience with semiconductor equipment</li></ul></div></section>
	<ul class="similar-jobs"><li>Product Manager Product Manager Cisco Bengaluru 1 week ago</li></ul>`
	p, err := Parse(page)
	if err != nil || len(p.Requirements) != 1 || strings.Contains(strings.Join(p.Requirements, " "), "Cisco") {
		t.Errorf("err=%v %+v", err, p.Requirements)
	}
}
