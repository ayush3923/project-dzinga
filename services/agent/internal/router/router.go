// Package router picks a Claude model for each kind of agentic task, sends each
// model the request settings it accepts, and escalates to a stronger model once
// when a cheaper one fails.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ayush3923/project-dzinga/services/agent/internal/llm"
)

type Task string

const (
	FitSummary       Task = "fit_summary"       // verdict and suggestions for a computed fit score
	DiagnosisSummary Task = "diagnosis_summary" // summary of a computed diagnosis
	DigestSummary    Task = "digest_summary"    // the home page's morning digest, once per refresh
	Drafting         Task = "drafting"          // cold-email draft, on request
)

// Analysis (posting parsing, correlation, scoring, diagnosis) is plain code, and
// web research happens in the Claude app; only these short writing tasks use the API.
var Tasks = []Task{FitSummary, DiagnosisSummary, DigestSummary, Drafting}

// Caps is what a model accepts. Settings a model rejects are never sent to it.
type Caps struct {
	Structured       bool     // output_config.format (JSON schema)
	WebTools         bool     // web_search_20260209 / web_fetch_20260209
	AdaptiveExplicit bool     // must send thinking: adaptive (otherwise it runs without thinking)
	Fallbacks        bool     // server-side refusal fallbacks ("default")
	Efforts          []string // accepted output_config.effort values
}

var Models = map[string]Caps{
	"claude-opus-5-5":   {Structured: true, WebTools: true, Fallbacks: true, Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
	"claude-sonnet-5":   {Structured: true, WebTools: true, Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
	"claude-sonnet-4-6": {Structured: false, WebTools: true, AdaptiveExplicit: true, Efforts: []string{"low", "medium", "high", "max"}},
}

type Route struct {
	Model    string
	Effort   string
	Escalate string // stronger model to retry on once; "" = none
}

// Default routes (product owner, 2026-10-19: summaries on Sonnet 5 to save cost).
var Default = map[Task]Route{
	FitSummary:       {Model: "claude-sonnet-5", Effort: "low"},
	DiagnosisSummary: {Model: "claude-sonnet-5", Effort: "low"},
	DigestSummary:    {Model: "claude-sonnet-5", Effort: "low"},
	Drafting:         {Model: "claude-sonnet-5", Effort: "medium"},
}

func needs(Task) (structured, web bool) { return true, false }

// Validate checks that every route's models support what its task needs.
func Validate(routes map[Task]Route) error {
	for _, t := range Tasks {
		r, ok := routes[t]
		if !ok {
			return fmt.Errorf("no route for %s", t)
		}
		for _, m := range []string{r.Model, r.Escalate} {
			if m == "" {
				continue
			}
			c, ok := Models[m]
			if !ok {
				return fmt.Errorf("%s: unknown model %q", t, m)
			}
			structured, web := needs(t)
			if structured && !c.Structured {
				return fmt.Errorf("%s needs structured output, which %s does not support", t, m)
			}
			if web && !c.WebTools {
				return fmt.Errorf("%s needs web search, which %s does not support", t, m)
			}
		}
		okEffort := false
		for _, e := range Models[r.Model].Efforts {
			okEffort = okEffort || e == r.Effort
		}
		if !okEffort {
			return fmt.Errorf("%s: effort %q not accepted by %s", t, r.Effort, r.Model)
		}
	}
	return nil
}

// FromEnv applies overrides such as DZ_ROUTE_SUMMARY=claude-opus-5-5 to the defaults.
func FromEnv() (map[Task]Route, error) {
	routes := map[Task]Route{}
	for t, r := range Default {
		if m := os.Getenv("DZ_ROUTE_" + strings.ToUpper(string(t))); m != "" {
			r.Model = m
			if r.Escalate == m {
				r.Escalate = ""
			}
		}
		routes[t] = r
	}
	return routes, Validate(routes)
}

func spec(model, effort string) llm.Spec {
	c := Models[model]
	ok := false
	for _, e := range c.Efforts {
		ok = ok || e == effort
	}
	if !ok {
		effort = "high"
	}
	return llm.Spec{Model: model, Effort: effort, AdaptiveThinking: c.AdaptiveExplicit, Fallbacks: c.Fallbacks}
}

// Attempt is one model call made for a task, for usage records.
type Attempt struct {
	Task      Task
	Escalated bool
	Usage     llm.Usage
	Err       error
}

type Router struct {
	LLM    llm.Model
	Routes map[Task]Route
	Log    *slog.Logger
}

// escalatable: failures a stronger model may fix. Bad keys and rate limits are not.
func escalatable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errAttemptTimeout) {
		return true
	}
	var se *llm.StatusError
	if errors.As(err, &se) {
		return se.Code >= 500
	}
	return errors.Is(err, llm.ErrRefused) || errors.Is(err, llm.ErrInvalidJSON) || errors.Is(err, llm.ErrCutOff)
}

// errAttemptTimeout: one model call took too long (the whole analysis has time left).
var errAttemptTimeout = errors.New("the model took too long to answer")

// attemptLimit bounds a single model call so one slow request can't use up the
// whole analysis.
func attemptLimit(Task) time.Duration { return 90 * time.Second }

// AttemptLimit can be shortened in tests.
var AttemptLimit = attemptLimit

func (r *Router) run(ctx context.Context, task Task, call func(context.Context, llm.Spec) (llm.Usage, error)) ([]Attempt, error) {
	try := func(s llm.Spec) (llm.Usage, error) {
		actx, cancel := context.WithTimeout(ctx, AttemptLimit(task))
		defer cancel()
		u, err := call(actx, s)
		if err != nil && actx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			err = fmt.Errorf("%w (%s, %s)", errAttemptTimeout, task, s.Model)
		}
		return u, err
	}
	route := r.Routes[task]
	u, err := try(spec(route.Model, route.Effort))
	attempts := []Attempt{{Task: task, Usage: u, Err: err}}
	r.Log.Info("route", "task", task, "model", route.Model, "ok", err == nil)
	if escalatable(err) && route.Escalate != "" {
		r.Log.Warn("route_escalate", "task", task, "from", route.Model, "to", route.Escalate, "reason", err.Error())
		u, err = try(spec(route.Escalate, route.Effort))
		attempts = append(attempts, Attempt{Task: task, Escalated: true, Usage: u, Err: err})
	}
	return attempts, err
}

// Research runs a retrieval task (web tools, free-text answer).
func (r *Router) Research(ctx context.Context, task Task, system, prompt string) (string, []Attempt, error) {
	var text string
	attempts, err := r.run(ctx, task, func(actx context.Context, s llm.Spec) (llm.Usage, error) {
		var u llm.Usage
		var err error
		text, u, err = r.LLM.Research(actx, s, system, prompt)
		return u, err
	})
	return text, attempts, err
}

// Structured runs a task whose answer must match a JSON schema.
func (r *Router) Structured(ctx context.Context, task Task, system, prompt string, schema map[string]any) (json.RawMessage, []Attempt, error) {
	var raw json.RawMessage
	attempts, err := r.run(ctx, task, func(actx context.Context, s llm.Spec) (llm.Usage, error) {
		var u llm.Usage
		var err error
		raw, u, err = r.LLM.Structured(actx, s, system, prompt, schema)
		return u, err
	})
	return raw, attempts, err
}
