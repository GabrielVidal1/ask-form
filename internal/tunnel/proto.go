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
var (
	RequestHeaders = []string{
		"Cookie", "Content-Type", "Accept", "Accept-Language", "User-Agent",
		"Origin", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest",
	}
	ResponseHeaders = []string{
		"Content-Type", "Location", "Set-Cookie", "Cache-Control", "Pragma",
		"Content-Security-Policy", "Referrer-Policy", "X-Robots-Tag",
		"Strict-Transport-Security", "X-Content-Type-Options", "X-Frame-Options",
		"Permissions-Policy", "Cross-Origin-Opener-Policy", "Cross-Origin-Resource-Policy",
		"Retry-After",
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
