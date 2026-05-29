package api

import (
	"html/template"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
)

// defaultWebClientURL is the documented public convenience web client used when
// the operator has not configured their own. Self-hosters may override it via
// the HUSH_WEB_CLIENT_URL env var so the instructions point at any instance-
// agnostic Hush web client they prefer.
const defaultWebClientURL = "https://app.gethush.live"

// Invite codes and instance hostnames are whitelisted to safe character sets
// before being placed into a deep-link URL. This lets us mark the resulting
// hush:// link as template.URL (html/template otherwise strips the unknown
// scheme) without trusting arbitrary user input.
var (
	safeInviteCodePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	safeInstanceHostPattern = regexp.MustCompile(`^[A-Za-z0-9.-]{1,255}$`)
)

// InviteLandingHandler renders a human-readable join page for browser visits to
// /invite/{code} and /join/{host}/{code} on a backend-only (self-hosted)
// instance, where no web client is served at the instance origin.
//
// The page is rendered purely from the request path and serving origin; it
// performs no database lookup, embeds no credentials, and never overrides the
// instance origin. See CORE-INVARIANTS "Federation, Instance Routing, and
// Credential Boundaries".
type InviteLandingHandler struct {
	webClientURL string
	tmpl         *template.Template
}

// NewInviteLandingHandler builds the handler. An empty webClientURL falls back
// to the documented public convenience client.
func NewInviteLandingHandler(webClientURL string) *InviteLandingHandler {
	if webClientURL == "" {
		webClientURL = defaultWebClientURL
	}
	return &InviteLandingHandler{
		webClientURL: webClientURL,
		tmpl:         template.Must(template.New("invite-landing").Parse(inviteLandingTemplate)),
	}
}

// ServeInvite handles GET /invite/{code}, a same-instance invite whose
// instance is the serving origin.
func (h *InviteLandingHandler) ServeInvite(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	h.render(w, r, inviteLandingView{
		InstanceHost: r.Host,
		Code:         code,
		DeepLink:     buildInviteDeepLink(code),
	})
}

// ServeJoin handles GET /join/{host}/{code}, an invite that names an explicit
// instance host distinct from the serving origin.
func (h *InviteLandingHandler) ServeJoin(w http.ResponseWriter, r *http.Request) {
	host := chi.URLParam(r, "host")
	code := chi.URLParam(r, "code")
	h.render(w, r, inviteLandingView{
		InstanceHost: host,
		Code:         code,
		DeepLink:     buildJoinDeepLink(host, code),
	})
}

// inviteLandingView is the data passed to the landing template.
type inviteLandingView struct {
	InstanceHost string
	Code         string
	WebClientURL string
	DeepLink     template.URL
}

func (h *InviteLandingHandler) render(w http.ResponseWriter, r *http.Request, view inviteLandingView) {
	view.WebClientURL = h.webClientURL
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.Execute(w, view); err != nil {
		slog.Error("invite landing render", "err", err, "path", r.URL.Path)
	}
}

// buildInviteDeepLink returns the desktop deep-link for a same-instance invite,
// or an empty value when the code is not a safe, whitelisted token.
func buildInviteDeepLink(code string) template.URL {
	if !safeInviteCodePattern.MatchString(code) {
		return ""
	}
	return template.URL("hush://invite/" + code)
}

// buildJoinDeepLink returns the desktop deep-link for a cross-instance invite,
// or an empty value when either host or code fails whitelist validation.
func buildJoinDeepLink(host, code string) template.URL {
	if !safeInstanceHostPattern.MatchString(host) || !safeInviteCodePattern.MatchString(code) {
		return ""
	}
	return template.URL("hush://join/" + host + "/" + code)
}

const inviteLandingTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Join a Hush server</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 16px/1.5 system-ui, sans-serif; max-width: 34rem; margin: 4rem auto; padding: 0 1.25rem; }
  h1 { font-size: 1.4rem; margin: 0 0 .5rem; }
  .host { font-weight: 600; }
  ol { padding-left: 1.25rem; }
  li { margin: .35rem 0; }
  .code { display: inline-block; font: 600 1.1rem ui-monospace, monospace; padding: .4rem .7rem; border: 1px solid currentColor; border-radius: .5rem; letter-spacing: .04em; }
  .btn { display: inline-block; margin: 1rem .5rem 0 0; padding: .6rem 1rem; border: 1px solid currentColor; border-radius: .5rem; text-decoration: none; font-weight: 600; }
  .muted { opacity: .7; font-size: .9rem; }
</style>
</head>
<body>
<h1>You've been invited to a Hush server</h1>
<p>This invite is for the instance <span class="host">{{.InstanceHost}}</span>. The instance address itself has no web page. Join from a Hush client instead.</p>
<p>Your invite code: <span class="code">{{.Code}}</span></p>
<ol>
  <li>Open the Hush desktop app, or the web client at <a href="{{.WebClientURL}}">{{.WebClientURL}}</a>.</li>
  <li>Add or select the instance <span class="host">{{.InstanceHost}}</span>.</li>
  <li>Enter your invite code <span class="code">{{.Code}}</span> when prompted.</li>
</ol>
{{if .DeepLink}}<a class="btn" href="{{.DeepLink}}">Open in Hush Desktop</a>{{end}}
<a class="btn" href="{{.WebClientURL}}">Open web client</a>
<p class="muted">If a link does not open the app, copy the invite code above and enter it manually after connecting to {{.InstanceHost}}.</p>
</body>
</html>`
