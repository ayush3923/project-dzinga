package router

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/agent/internal/llm"
)

// scripted returns one result per call, recording the spec each call received.
type scripted struct {
	specs []llm.Spec
	errs  []error
}

func (s *scripted) next(spec llm.Spec) error {
	s.specs = append(s.specs, spec)
	if len(s.errs) == 0 {
		return nil
	}
	err := s.errs[0]
	s.errs = s.errs[1:]
	return err
}
func (s *scripted) Research(_ context.Context, spec llm.Spec, _, _ string) (string, llm.Usage, error) {
	return "notes", llm.Usage{Model: spec.Model}, s.next(spec)
}
func (s *scripted) Structured(_ context.Context, spec llm.Spec, _, _ string, _ map[string]any) (json.RawMessage, llm.Usage, error) {
	return json.RawMessage(`{}`), llm.Usage{Model: spec.Model}, s.next(spec)
}

func newRouter(m llm.Model) *Router {
	return &Router{LLM: m, Routes: Default, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestDefaultRoutesAreValid(t *testing.T) {
	if err := Validate(Default); err != nil {
		t.Fatal(err)
	}
	// Only short writing tasks use the API, all on Sonnet 5.
	for _, task := range Tasks {
		if Default[task].Model != "claude-sonnet-5" {
			t.Errorf("%s routes to %s, want claude-sonnet-5", task, Default[task].Model)
		}
	}
}

func TestEachTaskGetsItsModelSettings(t *testing.T) {
	m := &scripted{}
	r := newRouter(m)
	r.Structured(context.Background(), FitSummary, "s", "p", nil)
	if m.specs[0] != (llm.Spec{Model: "claude-sonnet-5", Effort: "low"}) {
		t.Errorf("fit summary spec: %+v", m.specs[0])
	}
	routes := map[Task]Route{FitSummary: {Model: "claude-sonnet-4-6", Effort: "medium"}}
	r.Routes = routes
	r.Structured(context.Background(), FitSummary, "s", "p", nil)
	if m.specs[1] != (llm.Spec{Model: "claude-sonnet-4-6", Effort: "medium", AdaptiveThinking: true}) {
		t.Errorf("sonnet 4.6 spec: %+v", m.specs[1])
	}
}

func escalating() map[Task]Route {
	return map[Task]Route{FitSummary: {Model: "claude-sonnet-5", Effort: "low", Escalate: "claude-opus-5-5"}}
}

func TestEscalatesOnceOnRecoverableFailure(t *testing.T) {
	for _, cause := range []error{llm.ErrInvalidJSON, llm.ErrCutOff, llm.ErrRefused, &llm.StatusError{Code: 529, Msg: "overloaded"}} {
		m := &scripted{errs: []error{cause}}
		r := newRouter(m)
		r.Routes = escalating()
		_, attempts, err := r.Structured(context.Background(), FitSummary, "s", "p", nil)
		if err != nil || len(attempts) != 2 || !attempts[1].Escalated || m.specs[1].Model != "claude-opus-5-5" {
			t.Errorf("%v: err=%v attempts=%+v specs=%+v", cause, err, attempts, m.specs)
		}
	}
}

func TestDoesNotEscalateUnrecoverableOrUnconfigured(t *testing.T) {
	// A bad key, no credit or a rate limit won't be fixed by a bigger model.
	m := &scripted{errs: []error{&llm.StatusError{Code: 400, Msg: "credit balance is too low"}}}
	r := newRouter(m)
	r.Routes = escalating()
	if _, attempts, err := r.Structured(context.Background(), FitSummary, "s", "p", nil); err == nil || len(attempts) != 1 {
		t.Errorf("400 escalated: %+v", attempts)
	}
	// The defaults have no escalation: a failed summary falls back to a template.
	m = &scripted{errs: []error{llm.ErrInvalidJSON}}
	if _, attempts, err := newRouter(m).Structured(context.Background(), FitSummary, "s", "p", nil); err == nil || len(attempts) != 1 {
		t.Errorf("default escalated: %+v", attempts)
	}
}

func TestEnvOverridesAreValidated(t *testing.T) {
	t.Setenv("DZ_ROUTE_FIT_SUMMARY", "claude-opus-5-5")
	routes, err := FromEnv()
	if err != nil || routes[FitSummary].Model != "claude-opus-5-5" {
		t.Errorf("override: %+v %v", routes[FitSummary], err)
	}
	// Sonnet 4.6 has no structured outputs, so it can't run a JSON task.
	t.Setenv("DZ_ROUTE_FIT_SUMMARY", "claude-sonnet-4-6")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "structured output") {
		t.Errorf("want capability error, got %v", err)
	}
	t.Setenv("DZ_ROUTE_FIT_SUMMARY", "gpt-4")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("want unknown model error, got %v", err)
	}
}

// slow blocks until its context ends, like a request that never answers.
type slow struct{ scripted }

func (s *slow) Structured(ctx context.Context, spec llm.Spec, _, _ string, _ map[string]any) (json.RawMessage, llm.Usage, error) {
	s.specs = append(s.specs, spec)
	if spec.Model == "claude-opus-5-5" {
		return json.RawMessage(`{}`), llm.Usage{Model: spec.Model}, nil
	}
	<-ctx.Done()
	return nil, llm.Usage{Model: spec.Model}, ctx.Err()
}

func TestSlowCallTimesOutAndEscalates(t *testing.T) {
	old := AttemptLimit
	AttemptLimit = func(Task) time.Duration { return 50 * time.Millisecond }
	defer func() { AttemptLimit = old }()
	m := &slow{}
	r := newRouter(m)
	r.Routes = escalating()
	_, attempts, err := r.Structured(context.Background(), FitSummary, "s", "p", nil)
	if err != nil || len(attempts) != 2 || !strings.Contains(attempts[0].Err.Error(), "took too long") || m.specs[1].Model != "claude-opus-5-5" {
		t.Errorf("err=%v attempts=%+v", err, attempts)
	}
}
