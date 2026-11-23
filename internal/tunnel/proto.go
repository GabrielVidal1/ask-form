// Package tunnel is the wire between a relay and the machines behind it.
//
// A machine (`ask-form serve --relay …`) keeps a few HTTP long-polls open to
// the relay's machine API; the relay hands each one a visitor's request for a
// form that machine owns, the machine renders it in-process and posts the
// response back. Plain HTTP, no extra dependency, nothing listening on the
// machine — it only dials out — and forms and answers never leave it: the
// relay holds requests in memory for the few seconds they are in flight.
//
//	POST /_relay/v1/poll           {"forms":[ids], "version"} → 200 {"request":…} | 204 (nothing yet)
//	POST /_relay/v1/respond/{rid}  Response
//	POST /_relay/v1/claim          {"id"} → 200 | 409 (another machine holds it)
//	GET  /_relay/v1/health
//
// Every call carries `Authorization: Bearer <machine token>`; the relay knows
// the machine by its token.
package tunnel

import "net/http"

// Request is a visitor's request, as the relay forwards it.
type Request struct {
	ID       string      `json:"id"`
	Method   string      `json:"method"`
	Path     string      `json:"path"` // path and raw query
	Host     string      `json:"host"`
	Header   http.Header `json:"header"`
	Body     []byte      `json:"body,omitempty"`
	RemoteIP string      `json:"remoteIp"`
}

// BridgePrefix starts the paths of the relay's own calls to a machine (a
// chat app's connector on a linked machine: Agent.Bridge), never a visitor's.
const BridgePrefix = "/_mcp/"

// LinkRequest is `ask-form link CODE` reaching the relay.
type LinkRequest struct {
	Code string `json:"code"`
}

// LinkResponse names the account the code stood for (the machine takes
// only the accounts it linked) and the app that asked.
type LinkResponse struct {
	OK      bool   `json:"ok"`
	Account string `json:"account"`
	Client  string `json:"client,omitempty"`
}

// Response is what the machine answered.
type Response struct {
	Status int         `json:"status"`
	Header http.Header `json:"header"`
	Body   []byte      `json:"body,omitempty"`
}

// PollRequest is a machine's poll: the forms it serves, re-announced each time.
type PollRequest struct {
	Forms   []string `json:"forms"`
	Version string   `json:"version"`
}

// PollResponse carries one request to handle.
type PollResponse struct {
	Request *Request `json:"request,omitempty"`
}

// ClaimRequest reserves a form id for the calling machine.
type ClaimRequest struct {
	ID string `json:"id"`
}

// Header allow-lists, both ways: only what the answer pages need crosses.
// X-Ask-Account is excluded from visitor RequestHeaders and only allowed on
// relay-initiated BridgePrefix calls (BridgeRequestHeaders).
// Strict-Transport-Security is excluded from ResponseHeaders: the relay sets it.
var (
	RequestHeaders = []string{
		"Cookie", "Content-Type", "Accept", "Accept-Language", "User-Agent",
		"Origin", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest",
	}
	BridgeRequestHeaders = []string{
		"Content-Type", "Accept", "Accept-Language", "User-Agent",
		"X-Ask-Account",
	}
	ResponseHeaders = []string{
		"Content-Type", "Location", "Set-Cookie", "Cache-Control", "Pragma",
		"Content-Security-Policy", "Referrer-Policy", "X-Robots-Tag",
		"X-Content-Type-Options", "X-Frame-Options",
		"Permissions-Policy", "Cross-Origin-Opener-Policy", "Cross-Origin-Resource-Policy",
		"X-DNS-Prefetch-Control", "Retry-After",
	}
)

// Filter copies the allowed headers of h.
func Filter(h http.Header, allowed []string) http.Header {
	out := http.Header{}
	for _, k := range allowed {
		if v := h.Values(k); len(v) > 0 {
			out[http.CanonicalHeaderKey(k)] = append([]string(nil), v...)
		}
	}
	return out
}

// MaxResponseBody caps what a machine may send back for one request (an
// image asset is the largest thing a page serves).
const MaxResponseBody = 8 << 20

// MaxPollResponse caps the answer to a poll: one visitor request, JSON. An
// upload chunk (server.MaxChunk, 1 MiB) base64-encoded inside it is the
// largest thing it carries.
const MaxPollResponse = 2 << 20
