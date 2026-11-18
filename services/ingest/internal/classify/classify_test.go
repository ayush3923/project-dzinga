package classify

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name                string
		msg                 Message
		ok                  bool
		kind, company, role string
	}{
		{
			name: "linkedin easy apply",
			msg: Message{FromName: "LinkedIn", FromAddr: "jobs-noreply@linkedin.com",
				Subject: "Ayush, your application was sent to Razorpay",
				Text:    "Your application was sent to Razorpay. Senior Product Manager · Razorpay · Bengaluru"},
			ok: true, kind: "applied", company: "Razorpay",
		},
		{
			name: "greenhouse acknowledgement with role",
			msg: Message{FromName: "Postman Hiring Team", FromAddr: "no-reply@us.greenhouse-mail.io",
				Subject: "Thank you for applying to Postman",
				Text:    "Hi Ayush, thanks for your interest in the Senior Product Manager role at Postman. We'll be in touch to schedule an interview if there's a fit."},
			ok: true, kind: "applied", company: "Postman", role: "Senior Product Manager",
		},
		{
			name: "workday application for role at company",
			msg: Message{FromName: "Workday", FromAddr: "acme@myworkday.com",
				Subject: "Application for Data Engineer at Acme Corp",
				Text:    "We have received your application."},
			ok: true, kind: "applied", company: "Acme", role: "Data Engineer", // legal suffix dropped
		},
		{
			name: "rejection mentioning interview",
			msg: Message{FromName: "Freshworks Talent Acquisition", FromAddr: "careers@freshworks.com",
				Subject: "Update on your application",
				Text:    "Thank you for interviewing with us. Unfortunately, we have decided to move forward with other candidates."},
			ok: true, kind: "rejected", company: "Freshworks",
		},
		{
			name: "interview invitation in subject",
			msg: Message{FromName: "Priya from Swiggy", FromAddr: "priya@swiggy.in",
				Subject: "Interview invitation - Swiggy | Product Analyst",
				Text:    "Please share your availability."},
			ok: true, kind: "interview",
		},
		{
			name: "assessment",
			msg: Message{FromName: "HackerRank", FromAddr: "support@hackerrankforwork.com",
				Subject: "Invitation: Zeta coding assessment",
				Text:    "You have been invited to take the assessment for the Backend Engineer role at Zeta."},
			ok: true, kind: "assessment",
		},
		{
			name: "offer",
			msg: Message{FromName: "CRED People Team", FromAddr: "people@cred.club",
				Subject: "Your offer from CRED",
				Text:    "We are pleased to extend an offer for the role of Product Lead."},
			ok: true, kind: "offer", company: "CRED",
		},
		{
			name: "naukri applied",
			msg: Message{FromName: "Naukri", FromAddr: "naukri@naukri.com",
				Subject: "You have successfully applied to Business Analyst at Infosys",
				Text:    "Your application has been sent to the recruiter."},
			ok: true, kind: "applied", company: "Infosys",
		},
		// ---- patterns seen in a real mailbox (2026-10) ----
		{
			name: "role in place of company",
			msg: Message{FromName: "Talent Acquisition", FromAddr: "no-reply@myworkday.com",
				Subject: "Thank you for applying to Senior Manager-Digital Product Management",
				Text:    "We have received your application."},
			ok: true, kind: "applied", role: "Senior Manager-Digital Product Management",
		},
		{
			name: "role with requisition number",
			msg: Message{FromName: "Acme Careers", FromAddr: "careers@acme.example",
				Subject: "Thank you for applying to Manager-Delivery Management - 260123",
				Text:    "We received your application."},
			ok: true, kind: "applied", company: "Acme", role: "Manager-Delivery Management",
		},
		{
			name: "company then role after a dash",
			msg: Message{FromName: "GitLab", FromAddr: "no-reply@greenhouse.io",
				Subject: "Information about your application to GitLab - Senior Solutions Architect",
				Text:    "Unfortunately we will not be moving forward."},
			ok: true, kind: "rejected", company: "GitLab", role: "Senior Solutions Architect",
		},
		{
			name: "brand before 'job application'",
			msg: Message{FromName: "Careers", FromAddr: "noreply@myworkday.com",
				Subject: "We’ve received your Target job application", Text: "Thanks for applying."},
			ok: true, kind: "applied", company: "Target",
		},
		{
			name: "a career at",
			msg: Message{FromName: "Oracle", FromAddr: "no-reply@oracle.com",
				Subject: "We received your application", Text: "Thank you for your interest in a career at Oracle."},
			ok: true, kind: "applied", company: "Oracle",
		},
		{
			name: "legal suffix stripped",
			msg: Message{FromName: "JPMorgan Chase & Co", FromAddr: "jpmc@oraclecloud.com",
				Subject: "We received your job application (Job Number: 210000001)", Text: "Thank you."},
			ok: true, kind: "applied", company: "JPMorgan Chase",
		},
		{
			name: "outcome email with role only",
			msg: Message{FromName: "Recruiting", FromAddr: "jobs@contoso.example",
				Subject: "We have an update on the outcome of your Senior Product Manager application",
				Text:    "Unfortunately we have decided to move forward with other candidates."},
			ok: true, kind: "rejected", company: "Contoso",
		},
		{
			name: "legal suffix followed by a sentence",
			msg: Message{FromName: "Careers", FromAddr: "jpmc@oraclecloud.com",
				Subject: "Your job application status",
				Text:    "Thank you for your interest in JPMorgan Chase & Co. We received your job application."},
			ok: true, kind: "applied", company: "JPMorgan Chase",
		},
		{
			name: "glassdoor community post",
			msg: Message{FromName: "Glassdoor Community", FromAddr: "noreply@glassdoor.com",
				Subject: "Guys I got a offer of 40 LPA, but I will have to relocate", Text: "offer letter"},
			ok: false,
		},
		// ---- not applications ----
		{
			name: "linkedin job alert",
			msg: Message{FromName: "LinkedIn Job Alerts", FromAddr: "jobalerts-noreply@linkedin.com",
				Subject: "30+ new jobs for Product Manager in Bengaluru", Text: "Apply now"},
			ok: false,
		},
		{
			name: "company is hiring",
			msg: Message{FromName: "LinkedIn", FromAddr: "jobs-noreply@linkedin.com",
				Subject: "Google is hiring a Product Manager", Text: "See jobs"},
			ok: false,
		},
		{
			name: "naukri recommendations",
			msg: Message{FromName: "Naukri", FromAddr: "info@naukri.com",
				Subject: "Recommended jobs for you", Text: "Apply to these jobs"},
			ok: false,
		},
		// ---- patterns seen in a real Outlook mailbox (2026-10) ----
		{
			name: "interviewer name in subject; company from domain",
			msg: Message{FromName: "Karat", FromAddr: "no-reply@karat.com",
				Subject: "Rescheduling your interview with Arun!", Text: "Please pick a new time."},
			ok: true, kind: "interview", company: "Karat",
		},
		{
			name: "recruiter as sender name; company from domain",
			msg: Message{FromName: "Kavitha S", FromAddr: "kavitha.s@guidewire.com",
				Subject: "REMINDER: Interview Scheduled on Wednesday, September 23, 2026", Text: "See you then."},
			ok: true, kind: "interview", company: "Guidewire",
		},
		{
			name: "subdomain of the employer",
			msg: Message{FromName: "noreply@mail.amazon.jobs", FromAddr: "noreply@mail.amazon.jobs",
				Subject: "Thank you for Applying to Amazon!", Text: "We received your application."},
			ok: true, kind: "applied", company: "Amazon",
		},
		{
			name: "recruiting domain alias",
			msg: Message{FromName: "Meta Recruiting", FromAddr: "noreply@recruiting.facebook.com",
				Subject: "Thank you for applying to Meta", Text: "We received your application."},
			ok: true, kind: "applied", company: "Meta",
		},
		{
			name: "email address as sender name",
			msg: Message{FromName: "Cisco@myworkday.com", FromAddr: "cisco@myworkday.com",
				Subject: "Thanks for Considering Cisco", Text: "Unfortunately we will not be moving forward."},
			ok: true, kind: "rejected", company: "Cisco",
		},
		{
			name: "requisition id before role; ATS platform word dropped",
			msg: Message{FromName: "GE HealthCare Workday", FromAddr: "gehc@myworkday.com",
				Subject: "Thank you for Applying", Text: "Thank you for applying to the R171043 Principal Product Manager position."},
			ok: true, kind: "applied", company: "GE HealthCare", role: "Principal Product Manager",
		},
		{
			name: "domain alias used when nothing else names the company",
			msg: Message{FromName: "careers@lamresearch.com", FromAddr: "careers@lamresearch.com",
				Subject: "Ayush, we have received your application", Text: "Thank you."},
			ok: true, kind: "applied", company: "Lam Research",
		},
		{
			name: "e-signature notice ignored",
			msg: Message{FromName: "Kavitha S via Docusign", FromAddr: "dse_na4@docusign.net",
				Subject: "Guidewire NDA Agreement for Onsite Interview", Text: "Please sign."},
			ok: false,
		},
		// ---- patterns seen in a real Gmail mailbox (2026-10) ----
		{
			name: "linkedin post is not an application",
			msg: Message{FromName: "Aditi Khare", FromAddr: "notifications-noreply@linkedin.com",
				Subject: "SearchJev: Engineering Intelligence for Agentic Search", Text: "Take the assessment of your skills"},
			ok: false,
		},
		{
			name: "linkedin application notice still counts",
			msg: Message{FromName: "LinkedIn", FromAddr: "jobs-noreply@linkedin.com",
				Subject: "Ayush, your application was sent to LinkedIn", Text: "Your application was sent to LinkedIn."},
			ok: true, kind: "applied", company: "LinkedIn",
		},
		{
			name: "indeed job match is not an application",
			msg: Message{FromName: "Indeed", FromAddr: "match@match.indeed.com",
				Subject: "Senior Software Engineer - AI Trainer at DataAnnotation", Text: "Apply to this assessment-based role"},
			ok: false,
		},
		{
			name: "newsletter about interviews",
			msg: Message{FromName: "PM Exercises", FromAddr: "hello@productmanagementexercises.com",
				Subject: "Most Recent PM Interview Questions & Answers This Week", Text: "We will schedule an interview series"},
			ok: false,
		},
		{
			name: "news article with interview in the title",
			msg: Message{FromName: "The Information", FromAddr: "news@theinformation.com",
				Subject: "Exclusive: first interview since joining", Text: "..."},
			ok: false,
		},
		{
			name: "credit card assessment",
			msg: Message{FromName: "Team BankBazaar", FromAddr: "alerts@bankbazaar.com",
				Subject: "INT-ID:CI1116 — IndusInd Platinum Card assessment", Text: "Your assessment is pending"},
			ok: false,
		},
		{
			name: "applying to HackerRank the company is not an assessment",
			msg: Message{FromName: "HackerRank Careers", FromAddr: "careers@hackerrankcareers.com",
				Subject: "Thank you for applying to HackerRank - Principal Product Manager", Text: "We received your application."},
			ok: true, kind: "applied",
		},
		{
			name: "company before Application",
			msg: Message{FromName: "Accenture", FromAddr: "accenture@myworkday.com",
				Subject: "News on your Accenture Application", Text: "Unfortunately we have decided to move forward with others for the role of AI LLM Technology Architect."},
			ok: true, kind: "rejected", company: "Accenture",
		},
		{
			name: "html entity in company",
			msg: Message{FromName: "LinkedIn&nbsp;Recruiting", FromAddr: "jobs@smartrecruiters.com",
				Subject: "Your Application for Principal Product Manager - LinkedIn", Text: "Thank you for applying to LinkedIn&nbsp;"},
			ok: true, kind: "applied",
		},
		{
			name: "in-person interview without invite words",
			msg: Message{FromName: "Kavitha S", FromAddr: "kavitha.s@guidewire.com",
				Subject: "Onsite In-person Interview for Senior Platform Product Manager", Text: "Details below."},
			ok: true, kind: "interview", company: "Guidewire",
		},
		{
			name: "role of prefix dropped",
			msg: Message{FromName: "UiPath", FromAddr: "no-reply@ashbyhq.com",
				Subject: "Thank you for your application", Text: "Thank you for applying for the role of Principal Product Manager at UiPath."},
			ok: true, kind: "applied", role: "Principal Product Manager",
		},
		// ---- strict "applied" rule ----
		{
			name: "your application is received",
			msg: Message{FromName: "Acme Careers", FromAddr: "jobs@acme.example",
				Subject: "Your application is received", Text: "We will review it shortly."},
			ok: true, kind: "applied", company: "Acme",
		},
		{
			name: "thank you for your interest is not enough",
			msg: Message{FromName: "Acme", FromAddr: "news@acme.example",
				Subject: "Thank you for your interest in Acme", Text: "Check out our new product line!"},
			ok: false,
		},
		{
			name: "you applied is not enough",
			msg: Message{FromName: "Shop", FromAddr: "offers@shop.example",
				Subject: "You applied a coupon to your cart", Text: "Complete your order."},
			ok: false,
		},
		{
			name: "loan application received",
			msg: Message{FromName: "Bank", FromAddr: "alerts@bank.example",
				Subject: "Your Personal Loan application received", Text: "Thank you for your application."},
			ok: false,
		},
		{
			name: "generic application submitted is not enough",
			msg: Message{FromName: "Portal", FromAddr: "no-reply@portal.example",
				Subject: "Application submitted", Text: "Your form has been submitted."},
			ok: false,
		},
		{
			name: "otp",
			msg: Message{FromName: "Indeed", FromAddr: "no-reply@indeed.com",
				Subject: "Your OTP to verify your account", Text: "123456"},
			ok: false,
		},
		{
			name: "newsletter about interviews",
			msg: Message{FromName: "Aakash Gupta", FromAddr: "aakashgupta+getting-a-pm-job@substack.com",
				Subject: "How to Ace PM Interview Presentations and Homework in the AI Era", Text: "View this post on the web"},
			ok: false,
		},
		{
			name: "interview tool trial marketing",
			msg: Message{FromName: "Vauric", FromAddr: "hello@vauric.ai",
				Subject: "Your interview is approaching. Your trial expires tomorrow.", Text: "Everything you haven't used is still waiting."},
			ok: false,
		},
		{
			name: "company reviews digest",
			msg: Message{FromName: "Glassdoor", FromAddr: "noreply@glassdoor.com",
				Subject: "Just in at Tata Communications: This week's employee reviews and more", Text: "Check out recent updates"},
			ok: false,
		},
		{
			name: "candidate feedback survey",
			msg: Message{FromName: "SAP", FromAddr: "feedback@invites.starred.com",
				Subject: "Thank you for applying to SAP - please share your feedback!", Text: "Email not showing correctly?"},
			ok: false,
		},
		{
			name: "personal email",
			msg: Message{FromName: "Mom", FromAddr: "mom@yahoo.com",
				Subject: "Dinner on Sunday?", Text: "Call me"},
			ok: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Classify(tc.msg)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tc.ok, got)
			}
			if !ok {
				return
			}
			if got.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", got.Kind, tc.kind)
			}
			if tc.company != "" && got.Company != tc.company {
				t.Errorf("company = %q, want %q", got.Company, tc.company)
			}
			if tc.role != "" && got.Role != tc.role {
				t.Errorf("role = %q, want %q", got.Role, tc.role)
			}
		})
	}
}

func TestLooksRelevant(t *testing.T) {
	if !LooksRelevant("x@greenhouse-mail.io", "Hello") {
		t.Error("ATS sender should be relevant")
	}
	if !LooksRelevant("a@b.com", "Your application to Acme") {
		t.Error("application subject should be relevant")
	}
	if LooksRelevant("jobalerts-noreply@linkedin.com", "New jobs") {
		t.Error("job alerts should be filtered")
	}
	if LooksRelevant("friend@gmail.com", "Weekend plans") {
		t.Error("personal mail should be filtered")
	}
}

func TestSnippet(t *testing.T) {
	if got := Snippet("  a \n\n b  c ", 100); got != "a b c" {
		t.Errorf("got %q", got)
	}
	long := "word "
	for i := 0; i < 8; i++ {
		long += long
	}
	if got := Snippet(long, 50); len(got) > 54 {
		t.Errorf("snippet too long: %d", len(got))
	}
}

func TestJobURL(t *testing.T) {
	cases := map[string]string{
		"https://fitboard.in/job/8cd74219-1717-4d2a-8121-6eb37c63db55?utm_source=job_teaser&utm_medium=web": "https://fitboard.in/job/8cd74219-1717-4d2a-8121-6eb37c63db55",
		"https://boards.greenhouse.io/postman/jobs/6012345?gh_src=abc":                                      "https://boards.greenhouse.io/postman/jobs/6012345",
		"https://jobs.lever.co/acme/4f1c2d3e-aaaa-bbbb-cccc-1234567890ab":                                   "https://jobs.lever.co/acme/4f1c2d3e-aaaa-bbbb-cccc-1234567890ab",
		"https://adobe.wd5.myworkdayjobs.com/external/job/Bangalore/Principal-PM_R171043":                   "https://adobe.wd5.myworkdayjobs.com/external/job/Bangalore/Principal-PM_R171043",
		"https://www.amazon.jobs/en/jobs/2987654/senior-pm?ref=email":                                       "https://www.amazon.jobs/en/jobs/2987654/senior-pm",
	}
	for in, want := range cases {
		if got := JobURL([]string{"https://acme.example/unsubscribe?u=1", in}); got != want {
			t.Errorf("%s\n got %s\nwant %s", in, got, want)
		}
	}
	if got := JobURL([]string{"https://acme.example/blog/post", "https://click.mailer.example/ls/abc"}); got != "" {
		t.Errorf("non-job links matched: %s", got)
	}
}

// A rejection from the company's own address with a soft subject must reach the
// classifier (Netskope, 2026-10-21: it was skipped by the header filter).
func TestSoftSubjectRejectionIsRead(t *testing.T) {
	from, subject := "no-reply@netskope.com", "Thank you for your interest in Netskope"
	if !LooksRelevant(from, subject) {
		t.Fatal("header filter skipped the rejection")
	}
	r, ok := Classify(Message{FromAddr: from, Subject: subject, Text: "Dear Ayush, After a thorough review of your application and qualifications, " +
		"we regret to inform you that we will not be moving forward with your candidacy at this time."})
	if !ok || r.Kind != "rejected" || r.Company != "Netskope" {
		t.Errorf("got %+v ok=%v", r, ok)
	}
}

func TestCalendarInvite(t *testing.T) {
	hcl := Message{FromName: "Mohit Bindroo", FromAddr: "mohit.bindroo@hcl-software.com",
		Subject: "Invitation: Ayush Kumar - TP1 Discussion - HCLSoftware - Position - P... @ Thu Nov 19, 2026 11:30am - 12:30pm (IST) (ayush_kumar@live.in)",
		Text: "Ayush Kumar - TP1 Discussion - HCLSoftware - Position - Product Manager (Observability)\nJoin with Google Meet\n" +
			"Hi All, Please accept the invite for TP1 Discussion. Regards Mohit Bindroo"}
	cases := []struct {
		name                string
		msg                 Message
		ok                  bool
		kind, company, role string
	}{
		{name: "google calendar invite, title cut short in the subject", msg: hcl,
			ok: true, kind: "interview", company: "HCLSoftware", role: "Product Manager (Observability)"},
		{name: "body lines run together", msg: Message{FromAddr: hcl.FromAddr, Subject: hcl.Subject,
			Text: "Ayush Kumar - TP1 Discussion - HCLSoftware - Position - Product Manager (Observability) Join with Google Meet – Hi All"},
			ok: true, kind: "interview", company: "HCLSoftware", role: "Product Manager (Observability)"},
		{name: "outlook meeting request without the Invitation prefix", msg: Message{FromName: "Priya", FromAddr: "priya@northwind.com",
			Subject: "Northwind | Senior PM | Round 2", Text: "Microsoft Teams meeting", Invite: true},
			ok: true, kind: "interview", company: "Northwind"},
		{name: "invite that says interview", msg: Message{FromAddr: "talent@contoso.com",
			Subject: "Invitation: Interview with Contoso @ Mon Nov 23, 2026 3pm - 4pm (IST) (me@live.in)", Text: "Interview with Contoso"},
			ok: true, kind: "interview", company: "Contoso"},
		{name: "work meeting from a company is not an interview", msg: Message{FromAddr: "lead@fabrikam.com",
			Subject: "Invitation: Sprint planning @ Tue Nov 24, 2026 10am - 11am (IST) (me@live.in)", Text: "Sprint planning"}},
		{name: "personal invite needs a strong word", msg: Message{FromAddr: "friend@gmail.com",
			Subject: "Invitation: TP1 Discussion @ Wed Nov 25, 2026 7pm - 8pm (IST) (me@live.in)", Text: "TP1 Discussion"}},
		{name: "recruiter on gmail with a round", msg: Message{FromName: "Asha from Kestrel", FromAddr: "asha.recruits@gmail.com",
			Subject: "Invitation: Kestrel - Round 1 - Product Manager @ Wed Nov 25, 2026 7pm - 8pm (IST) (me@live.in)", Text: "Kestrel - Round 1 - Product Manager"},
			ok: true, kind: "interview", company: "Kestrel"},
		{name: "declined reply is not an invitation", msg: Message{FromAddr: "mohit.bindroo@hcl-software.com",
			Subject: "Declined: Ayush Kumar - TP1 Discussion @ Thu Nov 19, 2026", Text: "Mohit has declined this invitation."}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ok := Classify(c.msg)
			if ok != c.ok || r.Kind != c.kind || (c.company != "" && r.Company != c.company) || (c.role != "" && r.Role != c.role) {
				t.Errorf("got %v %+v, want %v %s/%s/%s", ok, r, c.ok, c.kind, c.company, c.role)
			}
		})
	}
	if !LooksRelevant("x@y.com", "Invitation: Ayush <> Acme @ Thu Nov 19, 2026") {
		t.Error("calendar invitations should pass the header filter")
	}
}

// Emails from the 18 Nov refresh that came out wrong.
func TestATSAndAgencyEmails(t *testing.T) {
	cases := []struct {
		name                string
		msg                 Message
		ok                  bool
		kind, company, role string
	}{
		{name: "workday tenant in the sender address, role in capitals", msg: Message{FromAddr: "walmart@myworkday.com",
			Subject: "Thank you for Applying!",
			Text: "Hi Ayush, Thank you for applying to our STAFF, PRODUCT MANAGER position. Your application will be reviewed and we will " +
				"contact you with next steps.\nThanks again!\nYour Walmart Hiring Team"},
			ok: true, kind: "applied", company: "Walmart", role: "STAFF, PRODUCT MANAGER"},
		{name: "emoji after the company name", msg: Message{FromAddr: "no-reply@ashbyhq.com",
			Subject: "Thanks for applying to Supabase 🚀", Text: "Hi Ayush, Thanks for applying to Supabase. We review every application carefully."},
			ok: true, kind: "applied", company: "Supabase"},
		{name: "lever: role after 'application for', ending at ', and we'", msg: Message{FromAddr: "no-reply@hire.lever.co",
			Subject: "Thank you for your application to Tala",
			Text: "Hi Ayush, Thank you for your interest in Tala! We wanted to let you know we received your application for " +
				"Senior Product Manager, Fraud & Risk, and we are delighted that you would consider joining our team."},
			ok: true, kind: "applied", company: "Tala", role: "Senior Product Manager, Fraud & Risk"},
		{name: "recruitment agency outreach is not an application", msg: Message{FromName: "Yogesh K", FromAddr: "yogesh.k@persolapac.com",
			Subject: "Job Opportunity || Magicbricks || Bangalore ||",
			Text: "Dear Candidate Greetings from Persol! PERSOL is one of the largest recruitment companies in Asia Pacific. " +
				"We are hiring a Group Product Manager for our client Magicbricks. Shortlisted profiles will go through an online assessment."}},
		{name: "agency email about the user's own application still counts", msg: Message{FromAddr: "yogesh.k@persolapac.com",
			Subject: "Interview invitation - Magicbricks - Group Product Manager",
			Text:    "Greetings from Persol! Our client would like to invite you to an interview for the Group Product Manager role."},
			ok: true, kind: "interview"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ok := Classify(c.msg)
			if ok != c.ok || r.Kind != c.kind || (c.company != "" && r.Company != c.company) || (c.role != "" && r.Role != c.role) {
				t.Errorf("got %v %+v, want %v %s/%s/%s", ok, r, c.ok, c.kind, c.company, c.role)
			}
		})
	}
}
