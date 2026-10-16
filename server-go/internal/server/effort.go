package server

import (
	"errors"
	"strings"
	"sync"

	"github.com/satomic/model-router/server-go/internal/omap"
	"github.com/satomic/model-router/server-go/internal/upstream"
)

// Some reasoning models (gpt-6-luna on Azure, for one) reject function tools on
// /chat/completions unless reasoning_effort is "none":
//
//	400: Function tools with reasoning_effort are not supported for this model in
//	/v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'.
//
// The router never forwards a reasoning_effort of its own, so the model's default effort applies
// and every tool-carrying request -- which is every GitHub Copilot request -- fails. Other
// reasoning models (gpt-5.4) accept tools at their default effort, so "none" is not forced on all
// of them: it is applied to a model only after that model has refused, and remembered from then
// on so the refusal costs one extra round trip per model and process, not one per request.

const toolsNeedNoEffort = "with reasoning_effort are not supported"

// noEffortWithTools holds provider + "\x00" + upstream model for the models that refused.
var noEffortWithTools sync.Map

func effortKey(provider, model string) string { return provider + "\x00" + model }

// hasTools reports whether the request carries at least one tool definition.
func hasTools(body *omap.Map) bool { return len(body.Slice("tools")) > 0 }

// isNoEffortRefusal reports whether err is the upstream's tools-with-reasoning-effort 400.
func isNoEffortRefusal(err error) bool {
	var ue *upstream.Error
	return errors.As(err, &ue) && ue.Status == 400 && strings.Contains(ue.Message, toolsNeedNoEffort)
}

// withEffortRetry calls send with body, and when a reasoning model refuses tools at its default
// effort, sends it again once with reasoning_effort "none" and remembers the model. A body that
// already names an effort is left alone: that is the caller's (or a previous call's) choice.
// note records what was changed, for the trace; it is called only when the body was changed.
func withEffortRetry[T any](reasoning bool, provider, model string, body *omap.Map,
	note func(string), send func(*omap.Map) (T, error)) (T, error) {
	key := effortKey(provider, model)
	if reasoning && hasTools(body) && !body.Has("reasoning_effort") {
		if _, known := noEffortWithTools.Load(key); known {
			body.Set("reasoning_effort", "none")
			note("reasoning_effort set to none: this model rejects function tools at its default effort")
		}
	}
	out, err := send(body)
	if err == nil || !reasoning || !hasTools(body) || body.Has("reasoning_effort") || !isNoEffortRefusal(err) {
		return out, err
	}
	noEffortWithTools.Store(key, true)
	body.Set("reasoning_effort", "none")
	note("retried with reasoning_effort none after: " + err.Error())
	return send(body)
}
