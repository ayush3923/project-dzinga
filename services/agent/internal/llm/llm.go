// Package llm wraps the Claude API for the agent: a research call that may search
// and fetch the web, and structured calls that must return JSON matching a schema.
// Which model runs each call, and with which settings, is decided by the router.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

type Usage struct {
	Model        string
	InputTokens  int64
	OutputTokens int64
	WebSearches  int64
}

func (u *Usage) add(m anthropic.BetaMessage) {
	if m.Model != "" {
		u.Model = string(m.Model)
	}
	u.InputTokens += m.Usage.InputTokens + m.Usage.CacheReadInputTokens + m.Usage.CacheCreationInputTokens
	u.OutputTokens += m.Usage.OutputTokens
	u.WebSearches += m.Usage.ServerToolUse.WebSearchRequests
}

// Spec is one call's model and the settings that model accepts (chosen by the router).
type Spec struct {
	Model            string
	Effort           string
	AdaptiveThinking bool // send thinking: adaptive explicitly (models that otherwise skip thinking)
	Fallbacks        bool // server-side refusal fallbacks
}

// Model is what the analysis code needs from an LLM; tests supply a fake.
type Model interface {
	// Research may search and fetch the web; it returns the model's notes as text.
	Research(ctx context.Context, spec Spec, system, prompt string) (string, Usage, error)
	// Structured returns JSON that matches schema.
	Structured(ctx context.Context, spec Spec, system, prompt string, schema map[string]any) (json.RawMessage, Usage, error)
}

var (
	// ErrRefused means Claude's safety systems declined the request.
	ErrRefused     = errors.New("the model declined this request")
	ErrInvalidJSON = errors.New("the model returned invalid JSON")
	ErrCutOff      = errors.New("the model's answer was cut off")
)

// StatusError is an HTTP error from the Claude API, with a readable message.
type StatusError struct {
	Code int
	Msg  string
	Err  error
}

func (e *StatusError) Error() string { return e.Msg }
func (e *StatusError) Unwrap() error { return e.Err }

type Claude struct {
	client anthropic.Client
}

func NewClaude(apiKey string) *Claude {
	return &Claude{client: anthropic.NewClient(option.WithAPIKey(apiKey), option.WithMaxRetries(2))}
}

// base sets what every request shares, sending each model only what it accepts.
func (c *Claude) base(s Spec, system string, maxTokens int64) anthropic.BetaMessageNewParams {
	p := anthropic.BetaMessageNewParams{
		Model:        anthropic.Model(s.Model),
		MaxTokens:    maxTokens,
		System:       []anthropic.BetaTextBlockParam{{Text: system}},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(s.Effort)},
	}
	if s.AdaptiveThinking {
		p.Thinking = anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{}}
	}
	if s.Fallbacks {
		// A declined request is re-served by a fallback model chosen per refusal category.
		p.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
		p.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
	}
	return p
}

const maxContinuations = 4

func (c *Claude) Research(ctx context.Context, s Spec, system, prompt string) (string, Usage, error) {
	usage := Usage{Model: s.Model}
	params := c.base(s, system, 16000)
	params.Tools = []anthropic.BetaToolUnionParam{
		{OfWebSearchTool20260209: &anthropic.BetaWebSearchTool20260209Param{MaxUses: anthropic.Int(5)}},
		{OfWebFetchTool20260209: &anthropic.BetaWebFetchTool20260209Param{MaxUses: anthropic.Int(5), MaxContentTokens: anthropic.Int(20000)}},
	}
	user := anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))
	params.Messages = []anthropic.BetaMessageParam{user}

	var assistant anthropic.BetaMessageParam
	var text strings.Builder
	for i := 0; ; i++ {
		resp, err := c.client.Beta.Messages.New(ctx, params)
		if err != nil {
			return "", usage, explain(err)
		}
		usage.add(*resp)
		if resp.StopReason == anthropic.BetaStopReasonRefusal {
			return "", usage, ErrRefused
		}
		for _, b := range resp.Content {
			if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
				text.WriteString(t.Text)
			}
		}
		if resp.StopReason != anthropic.BetaStopReasonPauseTurn || i == maxContinuations {
			break
		}
		// The server paused its search loop: send the turn back so it resumes.
		p := resp.ToParam()
		if assistant.Content == nil {
			assistant = p
		} else {
			assistant.Content = append(assistant.Content, p.Content...)
		}
		params.Messages = []anthropic.BetaMessageParam{user, assistant}
	}
	return strings.TrimSpace(text.String()), usage, nil
}

func (c *Claude) Structured(ctx context.Context, s Spec, system, prompt string, schema map[string]any) (json.RawMessage, Usage, error) {
	usage := Usage{Model: s.Model}
	params := c.base(s, system, 16000)
	params.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: schema}
	params.Messages = []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))}
	resp, err := c.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return nil, usage, explain(err)
	}
	usage.add(*resp)
	switch resp.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return nil, usage, ErrRefused
	case anthropic.BetaStopReasonMaxTokens:
		return nil, usage, ErrCutOff
	}
	var out strings.Builder
	for _, b := range resp.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			out.WriteString(t.Text)
		}
	}
	raw := json.RawMessage(out.String())
	if !json.Valid(raw) {
		return nil, usage, ErrInvalidJSON
	}
	return raw, usage, nil
}

// explain turns API errors into messages a user can act on.
func explain(err error) error {
	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		return err
	}
	msg := fmt.Sprintf("the Anthropic API returned %d", apierr.StatusCode)
	switch {
	case apierr.StatusCode == 401 || apierr.StatusCode == 403:
		msg = "the Anthropic API key was rejected (check ANTHROPIC_API_KEY)"
	case apierr.StatusCode == 429:
		msg = "rate limited by the Anthropic API; try again in a minute"
	case apierr.StatusCode == 400 && strings.Contains(err.Error(), "credit balance"):
		return &StatusError{Code: 402, Msg: "your Anthropic account has no API credit: add credit at console.anthropic.com → Plans & Billing, then try again", Err: err}
	case apierr.StatusCode == 400:
		msg = "the Anthropic API rejected the request"
	case apierr.StatusCode >= 500:
		msg = "the Anthropic API is having trouble; try again shortly"
	}
	return &StatusError{Code: apierr.StatusCode, Msg: msg + ": " + err.Error(), Err: err}
}
