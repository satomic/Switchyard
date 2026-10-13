package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/satomic/model-router/server-go/internal/omap"
)

// OpenAIDecisions posts one request to `<baseURL>/decisions` (OpenAI's Decisions API) and returns
// the decoded body.
//
// Like SystemOne, not a pooled Client: one bearer-token POST whose request and response are JSON
// maps, on the shared transport so the decision call reuses a warm connection. Errors come back
// in OpenAI's usual {"error": {"message", "code"}} envelope, which readError already unwraps.
func OpenAIDecisions(ctx context.Context, baseURL, apiKey string, body any) (*omap.Map, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/decisions", bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := systemOneHTTP.Do(req)
	if err != nil {
		return nil, explainDNS(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, readError(resp.StatusCode, payload)
	}
	value, err := omap.FromJSON(payload)
	if err != nil {
		return nil, err
	}
	data, ok := value.(*omap.Map)
	if !ok {
		return nil, &Error{Status: resp.StatusCode, Message: "the Decisions API returned a non-object body"}
	}
	return data, nil
}
