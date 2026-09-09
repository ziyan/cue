package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// Every response carries the policy, and every response carries a different
// nonce -- a nonce reused across responses is one an injected script could
// have read off the previous page.
func TestEveryResponseCarriesAPolicyAndItsOwnNonce(t *testing.T) {
	server := newTestServer(t, config.Default())
	defer func() { _ = server.device.Linker().Close() }()
	handler := withSecurityHeaders(server.router)

	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		policy := response.Header().Get("Content-Security-Policy")
		if policy == "" {
			t.Fatal("a response carried no content security policy")
		}
		if strings.Contains(policy, "script-src 'self' 'nonce-'") {
			t.Fatal("the nonce is empty, so this device's own scripts would not run")
		}
		if !strings.Contains(policy, "frame-ancestors 'none'") {
			t.Error("the policy lets this interface be framed")
		}
		// script-src must not allow inline. That is the whole point: the five
		// pages this server renders carry their own scripts, which the nonce
		// covers, and nothing else should run.
		if inline := directiveOf(policy, "script-src"); strings.Contains(inline, "'unsafe-inline'") {
			t.Errorf("script-src allows inline scripts: %q", inline)
		}

		found := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(policy)
		if found == nil {
			t.Fatal("the policy has no nonce in it")
		}
		if seen[found[1]] {
			t.Errorf("the nonce %q was used for two responses", found[1])
		}
		seen[found[1]] = true

		if response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Error("a browser is allowed to guess at the content type")
		}
	}
}

// The pages this server renders itself carry inline scripts, and they must
// carry the nonce from the very response that delivered them -- otherwise the
// policy silently switches off the on-screen menu, the setup page and the
// player, and the only sign is that nothing on the screen responds.
func TestTheRenderedPagesCarryTheNonceOfTheirOwnResponse(t *testing.T) {
	server := newTestServer(t, config.Default())
	defer func() { _ = server.device.Linker().Close() }()
	handler := withSecurityHeaders(server.router)

	request := httptest.NewRequest(http.MethodGet, "/welcome", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Skipf("the welcome page answered %d here", response.Code)
	}

	policy := response.Header().Get("Content-Security-Policy")
	found := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(policy)
	if found == nil {
		t.Fatal("no nonce in the policy")
	}
	body := response.Body.String()
	if strings.Contains(body, "<script") && !strings.Contains(body, `nonce="`+found[1]+`"`) {
		t.Error("the page has an inline script that the policy would refuse to run")
	}
}

// directiveOf returns one directive from a policy, and nothing after it. The
// first version of this test split on the directive name and searched the rest
// of the string, which found style-src's 'unsafe-inline' and reported it as
// script-src's -- a test that failed for a reason that was not true.
func directiveOf(policy, name string) string {
	for _, directive := range strings.Split(policy, ";") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, name+" ") {
			return directive
		}
	}
	return ""
}
