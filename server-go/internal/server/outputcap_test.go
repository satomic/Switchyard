package server

import (
	"testing"

	"github.com/satomic/model-router/server-go/internal/omap"
	"github.com/satomic/model-router/server-go/internal/upstream"
)

// fakeMini behaves like gpt-4.1-mini on Azure: anything over 32768 output tokens is refused.
func fakeMini(calls *[]*omap.Map) func(*omap.Map) (string, error) {
	return func(b *omap.Map) (string, error) {
		*calls = append(*calls, b.Clone())
		if limit, _ := omap.AsFloat(b.Value("max_tokens")); limit > 32768 {
			return "", &upstream.Error{Status: 400, Message: "400: max_tokens is too large: 128000. This model supports at most 32768 completion tokens, whereas you provided 128000."}
		}
		return "ok", nil
	}
}

func TestOutputCapRetryThenRemembered(t *testing.T) {
	outputCaps.Delete(effortKey("p1", "gpt-4.1-mini"))
	var calls []*omap.Map
	var notes []string
	note := func(s string) { notes = append(notes, s) }
	body := func() *omap.Map { return mapOf("max_tokens", omap.Int(128000)) }

	out, err := withOutputCapRetry("p1", "gpt-4.1-mini", body(), note, fakeMini(&calls))
	if err != nil || out != "ok" || len(calls) != 2 || calls[1].Int("max_tokens", 0) != 32768 {
		t.Fatalf("the refusal should be retried once at 32768: out=%q err=%v calls=%d", out, err, len(calls))
	}
	calls = nil
	if _, err := withOutputCapRetry("p1", "gpt-4.1-mini", body(), note, fakeMini(&calls)); err != nil || len(calls) != 1 {
		t.Fatalf("the remembered limit should be applied up front, calls=%d err=%v", len(calls), err)
	}
	calls = nil
	small := mapOf("max_tokens", omap.Int(512))
	if _, err := withOutputCapRetry("p1", "gpt-4.1-mini", small, note, fakeMini(&calls)); err != nil || calls[0].Int("max_tokens", 0) != 512 {
		t.Error("a limit already under the cap must be left alone")
	}
}

func TestOutputCapRecognisesAnthropic(t *testing.T) {
	err := &upstream.Error{Status: 400, Message: "max_tokens: 128000 > 64000, which is the maximum allowed number of output tokens for claude-sonnet"}
	if got := outputCapFrom(err); got != 64000 {
		t.Errorf("anthropic wording: got %d", got)
	}
	if got := outputCapFrom(&upstream.Error{Status: 400, Message: "something else"}); got != 0 {
		t.Errorf("an unrelated 400 must not be read as a limit: %d", got)
	}
}
