package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Graph reads a Microsoft personal mailbox (outlook.com, live.in, hotmail…) through
// Microsoft Graph. Microsoft no longer allows app passwords for these accounts, so
// it signs in with the OAuth device-code flow: the user approves read-only access
// (Mail.Read) on Microsoft's own page. The token lives in memory for this run only.
type Graph struct {
	ClientID string   // Application (client) ID of the user's own app registration
	Account  string   // expected mailbox address; the run stops if a different one signs in
	Folders  []string // well-known folder names, e.g. inbox, archive
	Log      *slog.Logger
	Prompt   func(verificationURI, userCode string)

	// RefreshToken, when set, signs in without a prompt (saved from an earlier
	// sign-in). OnRefresh receives every new refresh token so it can be stored.
	RefreshToken string
	OnRefresh    func(token string)

	HTTP      *http.Client
	AuthBase  string // default https://login.microsoftonline.com/consumers
	GraphBase string // default https://graph.microsoft.com/v1.0
}

func (g *Graph) defaults() {
	if g.HTTP == nil {
		g.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if g.AuthBase == "" {
		g.AuthBase = "https://login.microsoftonline.com/consumers"
	}
	if g.GraphBase == "" {
		g.GraphBase = "https://graph.microsoft.com/v1.0"
	}
}

func (g *Graph) Fetch(ctx context.Context, since time.Time, prefilter func(string, string) bool, fn func(Message) error) error {
	g.defaults()
	var token string
	var err error
	if g.RefreshToken != "" {
		token, err = g.refresh(ctx)
	} else {
		token, err = g.deviceLogin(ctx)
	}
	if err != nil {
		return err
	}
	if err := g.checkAccount(ctx, token); err != nil {
		return err
	}
	for _, f := range g.Folders {
		if err := g.fetchFolder(ctx, token, strings.ToLower(strings.TrimSpace(f)), since, prefilter, fn); err != nil {
			return fmt.Errorf("folder %q: %w", f, err)
		}
	}
	return nil
}

type oauthResp struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	AccessToken     string `json:"access_token"`
	RefreshToken    string `json:"refresh_token"`
	Error           string `json:"error"`
	ErrorDesc       string `json:"error_description"`
}

func (g *Graph) postForm(ctx context.Context, u string, v url.Values) (oauthResp, error) {
	var out oauthResp
	req, _ := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := g.HTTP.Do(req)
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	return out, err
}

const scopes = "offline_access Mail.Read User.Read"

// DeviceCode is a pending browser sign-in.
type DeviceCode struct {
	VerificationURI string
	UserCode        string
	ExpiresIn       time.Duration
	deviceCode      string
	interval        time.Duration
}

// StartDevice begins a device-code sign-in; the user approves it in a browser.
func (g *Graph) StartDevice(ctx context.Context) (DeviceCode, error) {
	g.defaults()
	dc, err := g.postForm(ctx, g.AuthBase+"/oauth2/v2.0/devicecode", url.Values{"client_id": {g.ClientID}, "scope": {scopes}})
	if err != nil {
		return DeviceCode{}, fmt.Errorf("start sign-in: %w", err)
	}
	if dc.Error != "" {
		return DeviceCode{}, fmt.Errorf("start sign-in: %s (check the client ID and that public client flows are allowed)", dc.Error)
	}
	return DeviceCode{VerificationURI: dc.VerificationURI, UserCode: dc.UserCode,
		ExpiresIn: time.Duration(max(dc.ExpiresIn, 60)) * time.Second, deviceCode: dc.DeviceCode,
		interval: time.Duration(max(dc.Interval, 1)) * time.Second}, nil
}

// WaitDevice polls until the user approves (or declines, or the code expires). It
// returns the access token and stores the refresh token through OnRefresh.
func (g *Graph) WaitDevice(ctx context.Context, d DeviceCode) (string, error) {
	g.defaults()
	interval := d.interval
	deadline := time.Now().Add(d.ExpiresIn)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		tok, err := g.postForm(ctx, g.AuthBase+"/oauth2/v2.0/token", url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"client_id":   {g.ClientID},
			"device_code": {d.deviceCode},
		})
		if err != nil {
			return "", fmt.Errorf("sign-in: %w", err)
		}
		switch tok.Error {
		case "":
			if tok.RefreshToken != "" && g.OnRefresh != nil {
				g.OnRefresh(tok.RefreshToken)
			}
			return tok.AccessToken, nil
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "authorization_declined":
			return "", errors.New("sign-in was declined")
		case "expired_token":
			return "", errors.New("sign-in code expired; run again")
		default:
			return "", fmt.Errorf("sign-in failed: %s", tok.Error)
		}
	}
	return "", errors.New("sign-in timed out; run again")
}

func (g *Graph) deviceLogin(ctx context.Context) (string, error) {
	d, err := g.StartDevice(ctx)
	if err != nil {
		return "", err
	}
	g.Prompt(d.VerificationURI, d.UserCode)
	return g.WaitDevice(ctx, d)
}

// ErrReconnect means the saved sign-in no longer works (revoked or expired).
var ErrReconnect = errors.New("the saved Microsoft sign-in has expired or was revoked; reconnect this mailbox")

func (g *Graph) refresh(ctx context.Context) (string, error) {
	tok, err := g.postForm(ctx, g.AuthBase+"/oauth2/v2.0/token", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {g.ClientID},
		"refresh_token": {g.RefreshToken},
		"scope":         {scopes},
	})
	if err != nil {
		return "", fmt.Errorf("sign-in: %w", err)
	}
	if tok.Error != "" || tok.AccessToken == "" {
		return "", ErrReconnect
	}
	if tok.RefreshToken != "" && g.OnRefresh != nil {
		g.OnRefresh(tok.RefreshToken) // Microsoft rotates refresh tokens
	}
	return tok.AccessToken, nil
}

// Verify checks a token belongs to the expected account (used after connecting).
func (g *Graph) Verify(ctx context.Context, token string) error {
	g.defaults()
	return g.checkAccount(ctx, token)
}

// get calls Graph, retrying politely when throttled (HTTP 429/503).
func (g *Graph) get(ctx context.Context, token, u string, extra map[string]string, v any) error {
	for attempt := 0; ; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		for k, val := range extra {
			req.Header.Set(k, val)
		}
		res, err := g.HTTP.Do(req)
		if err != nil {
			return err
		}
		if (res.StatusCode == 429 || res.StatusCode == 503) && attempt < 4 {
			wait, _ := strconv.Atoi(res.Header.Get("Retry-After"))
			res.Body.Close()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(max(wait, 2)) * time.Second):
			}
			continue
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusNotFound {
			return errNotFound
		}
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("graph returned %s", res.Status) // body may echo request details; not logged
		}
		return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(v)
	}
}

var errNotFound = errors.New("not found")

func (g *Graph) checkAccount(ctx context.Context, token string) error {
	var me struct {
		UserPrincipalName string `json:"userPrincipalName"`
		Mail              string `json:"mail"`
	}
	if err := g.get(ctx, token, g.GraphBase+"/me?$select=userPrincipalName,mail", nil, &me); err != nil {
		return fmt.Errorf("read profile: %w", err)
	}
	if g.Account != "" && !strings.EqualFold(me.UserPrincipalName, g.Account) && !strings.EqualFold(me.Mail, g.Account) {
		return fmt.Errorf("signed in as %s, but --user is %s; sign in with the right account", me.UserPrincipalName, g.Account)
	}
	if g.Account == "" {
		g.Account = me.UserPrincipalName
	}
	return nil
}

type graphAddr struct {
	EmailAddress struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

type graphMsg struct {
	ID                string    `json:"id"`
	InternetMessageID string    `json:"internetMessageId"`
	Subject           string    `json:"subject"`
	From              graphAddr `json:"from"`
	ReceivedDateTime  time.Time `json:"receivedDateTime"`
	Body              struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
}

func (g *Graph) fetchFolder(ctx context.Context, token, folder string, since time.Time,
	prefilter func(string, string) bool, fn func(Message) error) error {
	q := url.Values{
		"$filter":  {"receivedDateTime ge " + since.UTC().Format(time.RFC3339)},
		"$orderby": {"receivedDateTime desc"},
		"$select":  {"id,internetMessageId,subject,from,receivedDateTime"},
		"$top":     {"100"},
	}
	next := g.GraphBase + "/me/mailFolders/" + url.PathEscape(folder) + "/messages?" + q.Encode()

	// Pass 1: headers only, to choose which bodies to download.
	var candidates []graphMsg
	total := 0
	for next != "" {
		var page struct {
			Value    []graphMsg `json:"value"`
			NextLink string     `json:"@odata.nextLink"`
		}
		if err := g.get(ctx, token, next, nil, &page); errors.Is(err, errNotFound) {
			g.Log.Info("graph_folder_missing", "folder", folder)
			return nil
		} else if err != nil {
			return err
		}
		for _, m := range page.Value {
			total++
			if prefilter == nil || prefilter(strings.ToLower(m.From.EmailAddress.Address), m.Subject) {
				candidates = append(candidates, m)
			}
		}
		next = page.NextLink
		if next != "" && !strings.HasPrefix(next, g.GraphBase) {
			return errors.New("unexpected pagination link") // never send the token elsewhere
		}
	}
	g.Log.Info("graph_scan", "folder", folder, "messages", total, "candidates", len(candidates))

	// Pass 2: plain-text bodies for candidates.
	textBody := map[string]string{"Prefer": `outlook.body-content-type="text"`}
	for _, c := range candidates {
		var full graphMsg
		if err := g.get(ctx, token, g.GraphBase+"/me/messages/"+url.PathEscape(c.ID)+"?$select=body", textBody, &full); err != nil {
			g.Log.Warn("graph_body_failed", "folder", folder)
			continue
		}
		text := full.Body.Content
		var htmlBody string
		if strings.EqualFold(full.Body.ContentType, "html") {
			htmlBody, text = text, htmlToText(text)
		}
		id := c.InternetMessageID
		if id == "" {
			id = "graph:" + c.ID
		}
		m := Message{ID: strings.Trim(id, "<>"), Account: g.Account, Date: c.ReceivedDateTime}
		m.FromName, m.FromAddr = c.From.EmailAddress.Name, strings.ToLower(c.From.EmailAddress.Address)
		m.Subject, m.Text = c.Subject, strings.TrimSpace(text)
		m.Links = links(htmlBody, text)
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}
