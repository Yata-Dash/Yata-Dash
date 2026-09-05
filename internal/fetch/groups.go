package fetch

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/ident"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/netguard"
)

// applyCustomAuth attaches a custom def's credentials to a request, in the
// style its auth_method names. Shared by the stats fetch and the group-ladder
// fetch so a platform that changes how it authenticates is one edit, not two —
// and so a second endpoint can never drift into using a weaker style than the
// first.
func applyCustomAuth(req *http.Request, api *defs.CustomAPI, t models.Tracker) *Error {
	switch api.AuthMethod {
	case "session_cookie":
		if strings.TrimSpace(t.SessionCookie) == "" {
			return errf("no_key", nil)
		}
		req.Header.Set("Cookie", api.CookieName+"="+strings.TrimSpace(t.SessionCookie))
	case "api_key_query":
		if strings.TrimSpace(t.APIKey) == "" {
			return errf("no_key", nil)
		}
		q := req.URL.Query()
		param := api.APIKeyParam
		if param == "" {
			param = "api_token"
		}
		q.Set(param, t.APIKey)
		req.URL.RawQuery = q.Encode()
	case "api_key_header":
		if strings.TrimSpace(t.APIKey) == "" {
			return errf("no_key", nil)
		}
		req.Header.Set("Authorization", "Bearer "+t.APIKey)
	case "api_key_json_rpc":
		// The key is embedded as the first positional request param instead.
	}
	return nil
}

// FetchGroups retrieves the tracker's own group ladder and returns the raw
// response body with per-user progress stripped — ready to hash and store.
//
// It returns bytes rather than []defs.GroupDef because what gets stored is
// what the TRACKER said, not what this version of Yata understood: a better
// mapping later (when the platform starts serving colours, say) re-derives
// from the response, with nothing to refetch.
func (c *Client) FetchGroups(t models.Tracker) ([]byte, *Error) {
	spec := c.Registry.GroupAPI(t.URL, t.Type)
	if spec == nil || spec.Path == "" {
		return nil, errf("no_def", fmt.Errorf("no group API for %s", t.URL))
	}
	api := c.Registry.ResolveCustomAPI(t.URL, t.Type)
	if api == nil {
		return nil, errf("no_def", fmt.Errorf("no custom API def for %s", t.URL))
	}

	// Same split as fetchCustom: a def-chosen host is held to the stricter
	// policy, since a def is data and this one picked the address.
	baseURL, client := t.URL, c.HTTP
	if strings.TrimSpace(api.BaseURL) != "" {
		baseURL, client = api.BaseURL, c.defBaseClient()
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+spec.Path, nil)
	if err != nil {
		return nil, errf("request_error", err)
	}
	// Without this the platform answers a browser redirect to its login page
	// instead of a status, so an auth failure would read as a missing endpoint.
	req.Header.Set("Accept", "application/json")
	ident.Apply(req, c.identify(t))
	if aErr := applyCustomAuth(req, api, t); aErr != nil {
		return nil, aErr
	}

	resp, err := client.Do(req)
	if err != nil {
		if netguard.IsBlocked(err) {
			return nil, errf("blocked_destination", err)
		}
		return nil, errf(classifyNetErr(err), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errf(fmt.Sprintf("http_%d", resp.StatusCode), fmt.Errorf("http %d", resp.StatusCode))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errf("read_error", err)
	}
	// Reject a response with no usable ladder HERE rather than storing it:
	// an empty revision would overwrite a good one and read downstream as a
	// tracker that abolished its ranks.
	if len(defs.LadderFromAPI(body, *spec)) == 0 {
		return nil, errf("parse_error", fmt.Errorf("no %q ladder in response", spec.Ladder))
	}
	stripped, err := defs.StripGroupProgress(body)
	if err != nil {
		return nil, errf("parse_error", err)
	}
	return stripped, nil
}
