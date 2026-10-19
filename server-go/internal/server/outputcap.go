package server

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"

	"github.com/satomic/model-router/server-go/internal/omap"
	"github.com/satomic/model-router/server-go/internal/upstream"
)

// A client sizes its output limit for the model it *thinks* it is talking to. Behind a router that
// is often not the model that answers: Copilot CLI configured for gpt-5.4 asks for 128000 output
// tokens, the router picks gpt-4.1-mini, and Azure refuses the whole request:
//
//	400: max_tokens is too large: 128000. This model supports at most 32768 completion tokens,
//	whereas you provided 128000.
//
// The caller cannot fix this -- it never chose the model -- so the router does: the request is sent
// again once with the limit the upstream named, and that limit is remembered for the model, so
// later requests are clamped before they are sent and the refusal costs one round trip per model
// and process. Anthropic words the same refusal differently, and both are recognised.

var outputCapPatterns = []*regexp.Regexp{
	regexp.MustCompile(`supports at most (\d+) (?:completion|output) tokens`),
	regexp.MustCompile(`> (\d+), which is the maximum allowed number of output tokens`),
}

// outputCapFields are the names the output limit goes by: chat, reasoning chat, Responses.
var outputCapFields = []string{"max_tokens", "max_completion_tokens", "max_output_tokens"}

// outputCaps holds provider + "\x00" + upstream model -> the largest output limit it accepts.
var outputCaps sync.Map

// outputCapFrom returns the limit an upstream refusal names, or 0 when err is not that refusal.
func outputCapFrom(err error) int {
	var ue *upstream.Error
	if !errors.As(err, &ue) || ue.Status != 400 {
		return 0
	}
	for _, pattern := range outputCapPatterns {
		if match := pattern.FindStringSubmatch(ue.Message); match != nil {
			if limit, convErr := strconv.Atoi(match[1]); convErr == nil && limit > 0 {
				return limit
			}
		}
	}
	return 0
}

// clampOutput lowers every output-limit field above `limit` to it, and reports whether it did.
func clampOutput(body *omap.Map, limit int) bool {
	changed := false
	for _, field := range outputCapFields {
		if !body.Has(field) {
			continue
		}
		if value, ok := omap.AsFloat(body.Value(field)); ok && int(value) > limit {
			body.Set(field, omap.Int(limit))
			changed = true
		}
	}
	return changed
}

// withOutputCapRetry calls send with body, clamping the output limit to what this model is known
// to accept, and when the upstream refuses a limit as too large, sends it once more at the limit
// it named. note records what was changed, for the trace.
func withOutputCapRetry[T any](provider, model string, body *omap.Map,
	note func(string), send func(*omap.Map) (T, error)) (T, error) {
	key := effortKey(provider, model)
	if known, ok := outputCaps.Load(key); ok {
		if clampOutput(body, known.(int)) {
			note(fmt.Sprintf("output limit lowered to %d, the most this model accepts", known.(int)))
		}
	}
	out, err := send(body)
	limit := outputCapFrom(err)
	if limit == 0 || !clampOutput(body, limit) {
		return out, err
	}
	outputCaps.Store(key, limit)
	note(fmt.Sprintf("retried with the output limit lowered to %d after: %s", limit, err.Error()))
	return send(body)
}
