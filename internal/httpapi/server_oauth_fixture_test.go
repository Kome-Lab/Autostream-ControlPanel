package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeOAuthVerifier struct {
	identity oauthlogin.Identity
	err      error
}

type fakeOAuthConnector struct {
	account   oauthlogin.ConnectedAccount
	err       error
	onConnect func(oauthlogin.ConnectRequest)
}

func (f fakeOAuthVerifier) Verify(ctx context.Context, req oauthlogin.VerifyRequest) (oauthlogin.Identity, error) {
	if f.err != nil {
		return oauthlogin.Identity{}, f.err
	}
	return f.identity, nil
}

func (f fakeOAuthConnector) Connect(ctx context.Context, req oauthlogin.ConnectRequest) (oauthlogin.ConnectedAccount, error) {
	if f.onConnect != nil {
		f.onConnect(req)
	}
	if f.err != nil {
		return oauthlogin.ConnectedAccount{}, f.err
	}
	return f.account, nil
}

func startOAuthForTestWithRedirect(t *testing.T, handler http.Handler, providerID, redirectAfter string) (string, *http.Cookie) {
	t.Helper()
	requestBody, err := json.Marshal(map[string]string{"redirect_after": redirectAfter})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/"+providerID+"/start", bytes.NewReader(requestBody))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth start status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State == "" {
		t.Fatalf("oauth start response did not include state")
	}
	return body.State, findCookieForTest(t, res.Result().Cookies(), oauthStateCookieName)
}

func startOAuthForTest(t *testing.T, handler http.Handler, providerID string) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/"+providerID+"/start", bytes.NewBufferString(`{}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth start status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State == "" {
		t.Fatalf("oauth start did not return state")
	}
	return body.State, findCookieForTest(t, res.Result().Cookies(), oauthStateCookieName)
}

func assertOAuthCallbackNoStoreHeaders(t *testing.T, header http.Header) {
	t.Helper()
	if header.Get("Cache-Control") != "no-store" {
		t.Fatalf("oauth callback Cache-Control = %q, want no-store", header.Get("Cache-Control"))
	}
	if header.Get("Pragma") != "no-cache" {
		t.Fatalf("oauth callback Pragma = %q, want no-cache", header.Get("Pragma"))
	}
	if header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("oauth callback Referrer-Policy = %q, want no-referrer", header.Get("Referrer-Policy"))
	}
}

func startOAuthAccountForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, providerID string) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/start", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q}`, providerID)))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth account start status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State == "" {
		t.Fatalf("oauth account start did not return state")
	}
	return body.State, findCookieForTest(t, res.Result().Cookies(), oauthStateCookieName)
}

func findCookieForTest(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing cookie %s in %#v", name, cookies)
	return nil
}
