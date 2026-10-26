// Package core is the agent's client for core-api, which owns all data. The agent
// reads through the public API and writes only through token-protected internal routes.
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var ErrNotFound = errors.New("not found")

type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func New(base, token string) *Client {
	return &Client{Base: base, Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("core-api unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("core-api %s %s: %s %s", method, path, res.Status, msg)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
	}
	return nil
}

// ---- read models (subset of core-api's JSON) ----

type Application struct {
	ID        int64     `json:"id"`
	Company   string    `json:"company"`
	Role      string    `json:"role"`
	Stage     string    `json:"stage"`
	AppliedAt time.Time `json:"applied_at"`
	JobURL    string    `json:"job_url"` // added by the user: the definitive posting
}

type Event struct {
	OccurredAt time.Time `json:"occurred_at"`
	Sender     string    `json:"sender"`
	Subject    string    `json:"subject"`
	Snippet    string    `json:"snippet"`
	Kind       string    `json:"kind"`
}

type Experience struct {
	Title      string   `json:"title"`
	Company    string   `json:"company"`
	StartMonth string   `json:"start_month"`
	EndMonth   string   `json:"end_month"`
	Location   string   `json:"location"`
	Highlights []string `json:"highlights"`
}

type Profile struct {
	FullName       string       `json:"full_name"`
	Headline       string       `json:"headline"`
	CurrentTitle   string       `json:"current_title"`
	CurrentCompany string       `json:"current_company"`
	TotalYears     float64      `json:"total_years"`
	Location       string       `json:"location"`
	Summary        string       `json:"summary"`
	Skills         []string     `json:"skills"`
	Experiences    []Experience `json:"experiences"`
	UpdatedAt      *time.Time   `json:"updated_at"`
}

type Intent struct {
	Roles        []string `json:"roles"`
	Designation  string   `json:"designation"`
	Locations    []string `json:"locations"`
	SalaryMin    int      `json:"salary_min"`
	SalaryMax    int      `json:"salary_max"`
	Currency     string   `json:"currency"`
	CompanyTypes []string `json:"company_types"`
	Situation    string   `json:"situation"`
}

type Contact struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	RoleHint string `json:"role_hint"`
}

type Posting struct {
	Status           string   `json:"status"`
	Source           string   `json:"source"`
	URL              string   `json:"url"`
	Title            string   `json:"title"`
	Location         string   `json:"location"`
	WorkMode         string   `json:"work_mode"`
	Seniority        string   `json:"seniority"`
	MinYears         *int     `json:"min_years"`
	ProductContext   string   `json:"product_context"`
	HiringManager    string   `json:"hiring_manager_name"`
	HiringManagerTtl string   `json:"hiring_manager_title"`
	Requirements     []string `json:"requirements"`
	Qualifications   []string `json:"qualifications"`
	Responsibilities []string `json:"responsibilities"`
}

type FitFactor struct {
	Factor    string `json:"factor"`
	Score     int    `json:"score"`
	Rationale string `json:"rationale"`
}

// Evidence maps one posting requirement to the profile (correlation stage).
type Evidence struct {
	Requirement string `json:"requirement"`
	Evidence    string `json:"evidence"`
	Strength    string `json:"strength"` // strong | partial | missing
}

type Fit struct {
	Total       int         `json:"total"`
	Verdict     string      `json:"verdict"`
	Model       string      `json:"model"`
	Pipeline    string      `json:"pipeline"` // task=model;... for every stage that ran
	Factors     []FitFactor `json:"factors"`
	Suggestions []string    `json:"suggestions"`
	Strengths   []string    `json:"strengths"`
	Gaps        []string    `json:"gaps"`
	Evidence    []Evidence  `json:"evidence"`
}

type Detail struct {
	Posting  *Posting  `json:"posting"`
	Fit      *Fit      `json:"fit"`
	Contacts []Contact `json:"contacts"`
	JobLinks []string  `json:"job_links"`
}

type Draft struct {
	ContactID       *int64   `json:"contact_id"`
	Subject         string   `json:"subject"`
	Body            string   `json:"body"`
	ProductAnalysis string   `json:"product_analysis"`
	Experience      []string `json:"experience_points"`
	ValueAdds       []string `json:"value_adds"`
	Improvements    []string `json:"improvement_ideas"`
	Model           string   `json:"model"`
}

type Usage struct {
	ApplicationID *int64 `json:"application_id"`
	Purpose       string `json:"purpose"`
	Model         string `json:"model"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	WebSearches   int64  `json:"web_searches"`
}

// ---- calls ----

func (c *Client) Application(ctx context.Context, id int64) (Application, []Event, error) {
	var out struct {
		Application Application `json:"application"`
		Events      []Event     `json:"events"`
	}
	err := c.do(ctx, "GET", fmt.Sprintf("/api/v1/applications/%d", id), nil, &out)
	return out.Application, out.Events, err
}

func (c *Client) Detail(ctx context.Context, id int64) (Detail, error) {
	var d Detail
	err := c.do(ctx, "GET", fmt.Sprintf("/api/v1/applications/%d/detail", id), nil, &d)
	return d, err
}

func (c *Client) Profile(ctx context.Context) (Profile, error) {
	var p Profile
	err := c.do(ctx, "GET", "/api/v1/profile", nil, &p)
	return p, err
}

func (c *Client) Intent(ctx context.Context) (Intent, error) {
	var i Intent
	err := c.do(ctx, "GET", "/api/v1/intent", nil, &i)
	return i, err
}

func (c *Client) SavePosting(ctx context.Context, id int64, p Posting) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/internal/v1/applications/%d/posting", id), p, nil)
}

func (c *Client) SaveFit(ctx context.Context, id int64, f Fit) error {
	return c.do(ctx, "POST", fmt.Sprintf("/internal/v1/applications/%d/fit", id), f, nil)
}

func (c *Client) SaveDraft(ctx context.Context, id int64, d Draft) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := c.do(ctx, "POST", fmt.Sprintf("/internal/v1/applications/%d/outreach", id), d, &out)
	return out.ID, err
}

func (c *Client) RecordUsage(ctx context.Context, u Usage) error {
	return c.do(ctx, "POST", "/internal/v1/llm-usage", u, nil)
}

// ---- organizations ----

type OrgApplication struct {
	ID       int64  `json:"id"`
	Role     string `json:"role"`
	Stage    string `json:"stage"`
	Open     bool   `json:"open"`
	Timeline []struct {
		Label string    `json:"label"`
		At    time.Time `json:"at"`
	} `json:"timeline"`
	Fit *struct {
		Total   int    `json:"total"`
		Verdict string `json:"verdict"`
	} `json:"fit"`
}

type Organization struct {
	ID              int64            `json:"id"`
	Name            string           `json:"name"`
	Applications    []OrgApplication `json:"application_history"`
	Responded       int              `json:"responded"`
	Rejected        int              `json:"rejected"`
	AvgResponseDays *float64         `json:"avg_response_days"`
	DiagnosisReason string           `json:"diagnosis_reason"`
	Profile         *struct {
		Overview string `json:"overview"`
	} `json:"profile"`
}

type NewsItem struct {
	Title       string `json:"title"`
	PublishedOn string `json:"published_on"`
	Summary     string `json:"summary"`
	URL         string `json:"url"`
}

type Product struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Leader struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

type Rating struct {
	Source      string   `json:"source"`
	Score       *float64 `json:"score"`
	Scale       float64  `json:"scale"`
	ReviewCount *int     `json:"review_count"`
	URL         string   `json:"url"`
	AsOf        string   `json:"as_of"`
}

type CompanyProfile struct {
	Overview       string     `json:"overview"`
	InterviewAngle string     `json:"interview_angle"`
	News           []NewsItem `json:"news"`
	Products       []Product  `json:"products"`
	Leaders        []Leader   `json:"leaders"`
	Ratings        []Rating   `json:"ratings"`
	Model          string     `json:"model"`
}

type DiagnosisItem struct {
	Text       string `json:"text"`
	Confidence string `json:"confidence"`
}

type Diagnosis struct {
	Summary string          `json:"summary"`
	Causes  []DiagnosisItem `json:"causes"`
	Actions []string        `json:"actions"`
	Model   string          `json:"model"`
}

func (c *Client) Organization(ctx context.Context, id int64) (Organization, error) {
	var o Organization
	err := c.do(ctx, "GET", fmt.Sprintf("/api/v1/organizations/%d", id), nil, &o)
	return o, err
}

func (c *Client) SaveCompanyProfile(ctx context.Context, id int64, p CompanyProfile) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/internal/v1/organizations/%d/profile", id), p, nil)
}

func (c *Client) SaveDiagnosis(ctx context.Context, id int64, d Diagnosis) error {
	return c.do(ctx, "POST", fmt.Sprintf("/internal/v1/organizations/%d/diagnosis", id), d, nil)
}

// ---- morning digest ----

type DigestApp struct {
	Company string `json:"company"`
	Role    string `json:"role"`
	Kind    string `json:"kind"`
}

type Digest struct {
	RefreshID       int64       `json:"refresh_id"`
	NewApplications []DigestApp `json:"new_applications"`
	Responses       []DigestApp `json:"responses"`
	Trends          struct {
		AppsThisWeek       int     `json:"apps_this_week"`
		AppsLastWeek       int     `json:"apps_last_week"`
		ResponsesThisWeek  int     `json:"responses_this_week"`
		RejectionsThisWeek int     `json:"rejections_this_week"`
		ResponseRate30d    float64 `json:"response_rate_30d"`
		Waiting            int     `json:"waiting"`
	} `json:"trends"`
	NextSteps []struct {
		Company string `json:"company"`
		Role    string `json:"role"`
		Title   string `json:"title"`
	} `json:"next_steps"`
	Suggested []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"suggested"`
	Text  string `json:"text"`
	Model string `json:"model"`
}

func (c *Client) Digest(ctx context.Context) (Digest, error) {
	var d Digest
	err := c.do(ctx, "GET", "/api/v1/digest", nil, &d)
	return d, err
}

func (c *Client) SaveDigest(ctx context.Context, refreshID int64, text, model string) error {
	return c.do(ctx, "PUT", "/internal/v1/digest", map[string]any{"refresh_id": refreshID, "text": text, "model": model}, nil)
}
