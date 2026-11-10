package oidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func idToken(claims map[string]any) string {
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	return encode([]byte(`{"alg":"RS256"}`)) + "." + encode(payload) + ".signature"
}

func tokenExpiring(at time.Time) string {
	return idToken(map[string]any{"exp": at.Unix(), "email": "ada@example.test", "sub": "subject-ada"})
}

var freshToken = tokenExpiring(time.Now().Add(time.Hour))

type issuer struct {
	server    *httptest.Server
	discovery map[string]any
	device    map[string]any
	tokens    []map[string]any

	mutex       sync.Mutex
	deviceForms []url.Values
	tokenForms  []url.Values
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	stub := &issuer{}
	stub.server = httptest.NewServer(stub)
	t.Cleanup(stub.server.Close)
	stub.discovery = map[string]any{
		"issuer":                        stub.server.URL,
		"token_endpoint":                stub.server.URL + "/token",
		"device_authorization_endpoint": stub.server.URL + "/device",
	}
	stub.device = map[string]any{
		"device_code":      "device-1",
		"user_code":        "ABCD-EFGH",
		"verification_uri": "https://issuer.example/activate",
		"expires_in":       600,
		"interval":         1,
	}
	stub.tokens = []map[string]any{{"id_token": freshToken, "refresh_token": "refresh-1"}}
	return stub
}

func (s *issuer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		respond(w, s.discovery)
	case "/device":
		if err := r.ParseForm(); err != nil {
			panic(err)
		}
		s.deviceForms = append(s.deviceForms, r.PostForm)
		respond(w, s.device)
	case "/token":
		if err := r.ParseForm(); err != nil {
			panic(err)
		}
		s.tokenForms = append(s.tokenForms, r.PostForm)
		next := s.tokens[0]
		if len(s.tokens) > 1 {
			s.tokens = s.tokens[1:]
		}
		respond(w, next)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func respond(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(err)
	}
}

type recorder struct{ sleeps []time.Duration }

func (r *recorder) sleep(_ context.Context, duration time.Duration) error {
	r.sleeps = append(r.sleeps, duration)
	return nil
}

func (s *issuer) client(sleeper *recorder) *Client {
	return &Client{
		Settings: Settings{Issuer: s.server.URL, ClientID: "fleet-cli"},
		HTTP:     s.server.Client(),
		Sleep:    sleeper.sleep,
	}
}

func ignoreCode(DeviceCode) {}

func expectFailure(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v, want one mentioning %q", err, want)
	}
}

func TestLoginWaitsForApprovalAndBacksOffWhenAsked(t *testing.T) {
	stub := newIssuer(t)
	stub.tokens = []map[string]any{
		{"error": "authorization_pending"},
		{"error": "slow_down"},
		{"id_token": freshToken, "refresh_token": "refresh-1"},
	}
	sleeper := &recorder{}
	var shown []DeviceCode
	session, err := stub.client(sleeper).Login(context.Background(), func(code DeviceCode) { shown = append(shown, code) })
	if err != nil {
		t.Fatal(err)
	}
	if session.IDToken != freshToken || session.RefreshToken != "refresh-1" {
		t.Fatalf("session %+v", session)
	}
	want := []time.Duration{time.Second, time.Second, 6 * time.Second}
	if !reflect.DeepEqual(sleeper.sleeps, want) {
		t.Fatalf("slept %v, want %v", sleeper.sleeps, want)
	}
	if len(shown) != 1 || shown[0].UserCode != "ABCD-EFGH" || shown[0].VerificationURI != "https://issuer.example/activate" {
		t.Fatalf("showed %+v", shown)
	}
	last := stub.tokenForms[len(stub.tokenForms)-1]
	if last.Get("grant_type") != deviceGrant || last.Get("device_code") != "device-1" || last.Get("client_id") != "fleet-cli" {
		t.Fatalf("token form %v", last)
	}
}

func TestLoginAsksForTheDefaultScopes(t *testing.T) {
	stub := newIssuer(t)
	if _, err := stub.client(&recorder{}).Login(context.Background(), ignoreCode); err != nil {
		t.Fatal(err)
	}
	if got := stub.deviceForms[0].Get("scope"); got != "openid email offline_access" {
		t.Fatalf("scope %q", got)
	}
}

func TestLoginSendsConfiguredScopesAndClientSecret(t *testing.T) {
	stub := newIssuer(t)
	client := stub.client(&recorder{})
	client.Settings.Scopes = []string{"openid", "email"}
	client.Settings.ClientSecret = "installed-app-secret"
	if _, err := client.Login(context.Background(), ignoreCode); err != nil {
		t.Fatal(err)
	}
	form := stub.deviceForms[0]
	if form.Get("scope") != "openid email" || form.Get("client_secret") != "installed-app-secret" {
		t.Fatalf("device form %v", form)
	}
	if got := stub.tokenForms[0].Get("client_secret"); got != "installed-app-secret" {
		t.Fatalf("token form secret %q", got)
	}
}

func TestLoginAcceptsTheAlternateVerificationField(t *testing.T) {
	stub := newIssuer(t)
	delete(stub.device, "verification_uri")
	stub.device["verification_url"] = "https://issuer.example/device"
	var shown DeviceCode
	if _, err := stub.client(&recorder{}).Login(context.Background(), func(code DeviceCode) { shown = code }); err != nil {
		t.Fatal(err)
	}
	if shown.VerificationURI != "https://issuer.example/device" {
		t.Fatalf("showed %+v", shown)
	}
}

func TestLoginGivesUpWhenTheCodeExpires(t *testing.T) {
	stub := newIssuer(t)
	stub.device["expires_in"] = 2
	stub.tokens = []map[string]any{{"error": "authorization_pending"}}
	_, err := stub.client(&recorder{}).Login(context.Background(), ignoreCode)
	expectFailure(t, err, "expired")
}

func TestLoginStopsWhenApprovalIsDenied(t *testing.T) {
	stub := newIssuer(t)
	stub.tokens = []map[string]any{{"error": "access_denied", "error_description": "the operator said no"}}
	_, err := stub.client(&recorder{}).Login(context.Background(), ignoreCode)
	expectFailure(t, err, "the operator said no")
}

func TestLoginStopsWhenTheContextEnds(t *testing.T) {
	stub := newIssuer(t)
	client := stub.client(&recorder{})
	client.Sleep = func(context.Context, time.Duration) error { return context.Canceled }
	if _, err := client.Login(context.Background(), ignoreCode); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v", err)
	}
	if len(stub.tokenForms) != 0 {
		t.Fatalf("polled %d times after cancellation", len(stub.tokenForms))
	}
}

func TestLoginRefusesAnIssuerNamingAnotherIssuer(t *testing.T) {
	stub := newIssuer(t)
	stub.discovery["issuer"] = "https://elsewhere.example"
	_, err := stub.client(&recorder{}).Login(context.Background(), ignoreCode)
	expectFailure(t, err, "identifies itself")
	if len(stub.deviceForms) != 0 {
		t.Fatal("started a sign-in with the wrong issuer")
	}
}

func TestLoginRefusesPlainTextEndpoints(t *testing.T) {
	for _, endpoint := range []string{"token_endpoint", "device_authorization_endpoint"} {
		t.Run(endpoint, func(t *testing.T) {
			stub := newIssuer(t)
			stub.discovery[endpoint] = "http://remote.example/path"
			_, err := stub.client(&recorder{}).Login(context.Background(), ignoreCode)
			expectFailure(t, err, "plain text")
			if len(stub.deviceForms) != 0 {
				t.Fatal("sent credentials to a plain text endpoint")
			}
		})
	}
}

func TestLoginExplainsAnIssuerWithoutTheDeviceGrant(t *testing.T) {
	stub := newIssuer(t)
	delete(stub.discovery, "device_authorization_endpoint")
	_, err := stub.client(&recorder{}).Login(context.Background(), ignoreCode)
	expectFailure(t, err, "device authorization")
}

func TestLoginReportsARefusedStart(t *testing.T) {
	stub := newIssuer(t)
	stub.device = map[string]any{"error": "invalid_scope", "error_description": "offline_access is unknown"}
	_, err := stub.client(&recorder{}).Login(context.Background(), ignoreCode)
	expectFailure(t, err, "offline_access is unknown")
}

func TestLoginRefusesATokenResponseWithoutAnIDToken(t *testing.T) {
	stub := newIssuer(t)
	stub.tokens = []map[string]any{{"access_token": "not-an-id-token"}}
	_, err := stub.client(&recorder{}).Login(context.Background(), ignoreCode)
	expectFailure(t, err, "no ID token")
}

func TestRefreshKeepsTheRefreshTokenWhenTheIssuerDoesNotRotateIt(t *testing.T) {
	stub := newIssuer(t)
	stub.tokens = []map[string]any{{"id_token": freshToken}}
	next, err := stub.client(&recorder{}).Refresh(context.Background(), Session{IDToken: "old", RefreshToken: "refresh-1"})
	if err != nil {
		t.Fatal(err)
	}
	if next.IDToken != freshToken || next.RefreshToken != "refresh-1" {
		t.Fatalf("session %+v", next)
	}
	form := stub.tokenForms[0]
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "refresh-1" || form.Get("client_id") != "fleet-cli" {
		t.Fatalf("form %v", form)
	}
}

func TestRefreshKeepsTheSessionsBinding(t *testing.T) {
	stub := newIssuer(t)
	stub.tokens = []map[string]any{{"id_token": freshToken}}
	binding := Binding{Issuer: "https://issuer.example", ClientID: "fleet-cli", APIOrigin: "https://api.example"}
	next, err := stub.client(&recorder{}).Refresh(context.Background(), Session{IDToken: "old", RefreshToken: "refresh-1", Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	if next.Binding != binding {
		t.Fatalf("binding %+v", next.Binding)
	}
}

func TestRefreshAdoptsARotatedRefreshToken(t *testing.T) {
	stub := newIssuer(t)
	stub.tokens = []map[string]any{{"id_token": freshToken, "refresh_token": "refresh-2"}}
	next, err := stub.client(&recorder{}).Refresh(context.Background(), Session{IDToken: "old", RefreshToken: "refresh-1"})
	if err != nil || next.RefreshToken != "refresh-2" {
		t.Fatalf("session %+v, error %v", next, err)
	}
}

func TestRefreshReportsTheIssuersRefusal(t *testing.T) {
	stub := newIssuer(t)
	stub.tokens = []map[string]any{{"error": "invalid_grant", "error_description": "revoked"}}
	_, err := stub.client(&recorder{}).Refresh(context.Background(), Session{IDToken: "old", RefreshToken: "refresh-1"})
	expectFailure(t, err, "revoked")
}

func TestSessionValidity(t *testing.T) {
	now := time.Now()
	cases := map[string]struct {
		token string
		want  bool
	}{
		"well inside its lifetime":  {tokenExpiring(now.Add(time.Hour)), true},
		"inside the expiry margin":  {tokenExpiring(now.Add(30 * time.Second)), false},
		"already expired":           {tokenExpiring(now.Add(-time.Hour)), false},
		"no expiry claim":           {idToken(map[string]any{"sub": "ada"}), false},
		"not a JWT":                 {"opaque-token", false},
		"an undecodable payload":    {"a.!!!.c", false},
		"a payload that isn't JSON": {"a." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".c", false},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if got := (Session{IDToken: test.token}).Valid(now); got != test.want {
				t.Fatalf("valid %v, want %v", got, test.want)
			}
		})
	}
}

func TestIdentityPrefersEmailOverSubject(t *testing.T) {
	cases := map[string]struct{ token, want string }{
		"email":   {idToken(map[string]any{"email": "ada@example.test", "sub": "s"}), "ada@example.test"},
		"subject": {idToken(map[string]any{"sub": "s"}), "s"},
		"neither": {"opaque-token", "unknown"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if got := (Session{IDToken: test.token}).Identity(); got != test.want {
				t.Fatalf("identity %q, want %q", got, test.want)
			}
		})
	}
}

func redirectingServer(t *testing.T, location string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, location, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestTheClientRefusesARedirectToPlainText(t *testing.T) {
	server := redirectingServer(t, "http://insecure.example/token")
	response, err := NewHTTPClient().Post(server.URL, "text/plain", strings.NewReader("refresh_token=secret"))
	if response != nil {
		response.Body.Close()
	}
	expectFailure(t, err, "plain text")
}

func TestTheClientFollowsASecureRedirect(t *testing.T) {
	server := redirectingServer(t, "/final")
	response, err := NewHTTPClient().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d", response.StatusCode)
	}
}

func TestTheClientStopsARedirectLoop(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+"/again", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	response, err := NewHTTPClient().Get(server.URL)
	if response != nil {
		response.Body.Close()
	}
	expectFailure(t, err, "redirects")
}

func TestSettingsValidation(t *testing.T) {
	cases := map[string]struct {
		settings Settings
		ok       bool
	}{
		"https issuer":            {Settings{Issuer: "https://issuer.example", ClientID: "c"}, true},
		"loopback issuer":         {Settings{Issuer: "http://127.0.0.1:5556", ClientID: "c"}, true},
		"localhost issuer":        {Settings{Issuer: "http://localhost:5556", ClientID: "c"}, true},
		"plain http remote":       {Settings{Issuer: "http://issuer.example", ClientID: "c"}, false},
		"not a URL":               {Settings{Issuer: "issuer", ClientID: "c"}, false},
		"another scheme":          {Settings{Issuer: "ftp://issuer.example", ClientID: "c"}, false},
		"no client id":            {Settings{Issuer: "https://issuer.example"}, false},
		"a loopback-looking host": {Settings{Issuer: "http://localhost.example", ClientID: "c"}, false},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			err := test.settings.Validate()
			if (err == nil) != test.ok {
				t.Fatalf("error %v, ok %v", err, test.ok)
			}
		})
	}
}
