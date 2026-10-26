package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// fakeAPI answers like the Messages API and records each request body and headers.
func fakeAPI(t *testing.T, replies ...string) (*Claude, *[]map[string]any, *[]http.Header) {
	t.Helper()
	var bodies []map[string]any
	var headers []http.Header
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		bodies = append(bodies, m)
		headers = append(headers, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(replies[min(i, len(replies)-1)]))
		i++
	}))
	t.Cleanup(srv.Close)
	c := &Claude{client: anthropic.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0))}
	return c, &bodies, &headers
}

func reply(stop, text string) string {
	b, _ := json.Marshal(map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-opus-5-5", "stop_reason": stop,
		"content": []any{map[string]any{"type": "text", "text": text}},
		"usage":   map[string]any{"input_tokens": 100, "output_tokens": 20, "server_tool_use": map[string]any{"web_search_requests": 1}},
	})
	return string(b)
}

func TestStructuredRequestShape(t *testing.T) {
	c, bodies, headers := fakeAPI(t, reply("end_turn", `{"ok":true}`))
	schema := map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
		"required": []string{"ok"}, "additionalProperties": false}
	out, u, err := c.Structured(context.Background(), opus, "sys", "hello", schema)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"ok":true}` || u.InputTokens != 100 || u.Model != "claude-opus-5-5" {
		t.Errorf("out=%s usage=%+v", out, u)
	}
	b := (*bodies)[0]
	if b["model"] != "claude-opus-5-5" || b["fallbacks"] != "default" {
		t.Errorf("model/fallbacks: %v %v", b["model"], b["fallbacks"])
	}
	oc := b["output_config"].(map[string]any)
	if oc["effort"] != "high" || oc["format"].(map[string]any)["type"] != "json_schema" {
		t.Errorf("output_config: %v", oc)
	}
	if _, ok := b["thinking"]; ok {
		t.Error("thinking must be left unset on Claude Opus 5.5")
	}
	if !strings.Contains((*headers)[0].Get("Anthropic-Beta"), "server-side-fallback-2026-07-01") {
		t.Errorf("beta header: %q", (*headers)[0].Get("Anthropic-Beta"))
	}
}

var (
	opus     = Spec{Model: "claude-opus-5-5", Effort: "high", Fallbacks: true}
	sonnet46 = Spec{Model: "claude-sonnet-4-6", Effort: "medium", AdaptiveThinking: true}
)

// Sonnet 4.6 gets thinking: adaptive and no fallbacks; Opus 5.5 gets fallbacks and no thinking field.
func TestSettingsPerModel(t *testing.T) {
	c, bodies, headers := fakeAPI(t, reply("end_turn", "notes"))
	if _, _, err := c.Research(context.Background(), sonnet46, "s", "p"); err != nil {
		t.Fatal(err)
	}
	b := (*bodies)[0]
	if b["model"] != "claude-sonnet-4-6" || b["thinking"].(map[string]any)["type"] != "adaptive" {
		t.Errorf("sonnet 4.6 body: model=%v thinking=%v", b["model"], b["thinking"])
	}
	if _, ok := b["fallbacks"]; ok {
		t.Error("fallbacks must not be sent to claude-sonnet-4-6")
	}
	if strings.Contains((*headers)[0].Get("Anthropic-Beta"), "server-side-fallback") {
		t.Error("fallback beta header sent to claude-sonnet-4-6")
	}
}

func TestResearchToolsAndPauseTurn(t *testing.T) {
	c, bodies, _ := fakeAPI(t, reply("pause_turn", "Searching. "), reply("end_turn", "POSTING URL: https://x"))
	text, u, err := c.Research(context.Background(), sonnet46, "sys", "find it")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Searching. POSTING URL: https://x" || u.WebSearches != 2 || u.InputTokens != 200 {
		t.Errorf("text=%q usage=%+v", text, u)
	}
	if len(*bodies) != 2 {
		t.Fatalf("want a resumed second request, got %d", len(*bodies))
	}
	tools := (*bodies)[0]["tools"].([]any)
	types := []string{tools[0].(map[string]any)["type"].(string), tools[1].(map[string]any)["type"].(string)}
	if types[0] != "web_search_20260209" || types[1] != "web_fetch_20260209" {
		t.Errorf("tools: %v", types)
	}
	// Resume = the original user turn plus the paused assistant turn; no extra "continue" message.
	msgs := (*bodies)[1]["messages"].([]any)
	if len(msgs) != 2 || msgs[1].(map[string]any)["role"] != "assistant" {
		t.Errorf("resume messages: %v", msgs)
	}
}

func TestRefusalIsAnError(t *testing.T) {
	c, _, _ := fakeAPI(t, reply("refusal", ""))
	if _, _, err := c.Structured(context.Background(), opus, "s", "p", map[string]any{"type": "object"}); err != ErrRefused {
		t.Errorf("want ErrRefused, got %v", err)
	}
}

func TestBadKeyIsExplained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	defer srv.Close()
	c := &Claude{client: anthropic.NewClient(option.WithAPIKey("bad"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0))}
	_, _, err := c.Structured(context.Background(), opus, "s", "p", map[string]any{"type": "object"})
	if err == nil || !strings.Contains(err.Error(), "API key was rejected") {
		t.Errorf("got %v", err)
	}
}

func TestNoCreditIsExplained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API."}}`))
	}))
	defer srv.Close()
	c := &Claude{client: anthropic.NewClient(option.WithAPIKey("k"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0))}
	_, _, err := c.Structured(context.Background(), opus, "s", "p", map[string]any{"type": "object"})
	if err == nil || !strings.Contains(err.Error(), "no API credit") {
		t.Errorf("got %v", err)
	}
}
