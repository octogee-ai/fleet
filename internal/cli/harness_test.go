package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/octogee-ai/octogee-fleet/internal/oidc"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

var (
	signedInToken  = idToken(time.Now().Add(time.Hour))
	expiredToken   = idToken(time.Now().Add(-time.Hour))
	refreshedToken = idToken(time.Now().Add(2 * time.Hour))
)

func idToken(expiry time.Time) string {
	encode := base64.RawURLEncoding.EncodeToString
	payload := fmt.Sprintf(`{"exp":%d,"email":"ada@example.test","sub":"subject-ada"}`, expiry.Unix())
	return encode([]byte(`{"alg":"RS256"}`)) + "." + encode([]byte(payload)) + ".signature"
}

type sessionState int

const (
	signedIn sessionState = iota
	notSignedIn
	expiredSession
	expiredWithoutRefresh
)

type fixtureUser struct {
	id, name, gatewayID, provisioning string
}

type fixtureGateway struct {
	id, class, desired, owner string
	mounted                   []string
}

type fleetFixture struct {
	environment string
	users       []fixtureUser
	gateways    []fixtureGateway
}

var fixture = fleetFixture{
	environment: "local",
	users: []fixtureUser{
		{id: "u-ada", name: "ada", gatewayID: "shared-a"},
		{id: "u-grace", name: "grace", gatewayID: "iso-grace"},
		{id: "u-alan", name: "alan"},
	},
	gateways: []fixtureGateway{
		{id: "shared-a", class: "shared", desired: "running", mounted: []string{"u-ada"}},
		{id: "shared-b", class: "shared", desired: "running"},
		{id: "iso-grace", class: "isolated", desired: "running", owner: "u-grace", mounted: []string{"u-grace"}},
		{id: "shared-parked", class: "shared", desired: "paused"},
	},
}

type adminRequest struct {
	Method string
	Path   string
	Query  map[string]string
	Body   map[string]any
}

type recordedRequest struct {
	adminRequest
	authorization  string
	idempotencyKey string
}

type runOptions struct {
	rejectWith    int
	session       sessionState
	refreshFails  bool
	tokenCommand  []string
	redirectTo    string
	defaultHTTP   bool
	omitPasswords bool
	alterBinding  func(*oidc.Binding)
}

type fleetRun struct {
	exit        int
	stdout      string
	stderr      string
	requests    []recordedRequest
	issuerForms []url.Values
	sessionPath string
}

func (r fleetRun) mutations() []recordedRequest {
	var sent []recordedRequest
	for _, request := range r.requests {
		if request.Method != http.MethodGet {
			sent = append(sent, request)
		}
	}
	return sent
}

func (r fleetRun) onlyMutation(t *testing.T) adminRequest {
	t.Helper()
	sent := r.mutations()
	if len(sent) != 1 {
		t.Fatalf("expected exactly one mutating request, saw %d: exit=%d stderr=%s", len(sent), r.exit, r.stderr)
	}
	return sent[0].adminRequest
}

func (r fleetRun) containsRequest(want adminRequest) bool {
	for _, request := range r.requests {
		if reflect.DeepEqual(request.adminRequest, want) {
			return true
		}
	}
	return false
}

type adminStub struct {
	fixture fleetFixture
	options runOptions

	mutex    sync.Mutex
	requests []recordedRequest
}

func (s *adminStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.record(r)
	if s.options.redirectTo != "" {
		http.Redirect(w, r, s.options.redirectTo, http.StatusTemporaryRedirect)
		return
	}
	if s.options.rejectWith != 0 {
		writeJSON(w, s.options.rejectWith, map[string]any{"error": "token rejected"})
		return
	}
	if r.Method != http.MethodGet {
		s.serveMutation(w, r)
		return
	}
	s.serveRead(w, r)
}

func (s *adminStub) serveMutation(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/one-time-passwords") {
		writeJSON(w, http.StatusAccepted, map[string]any{"operationId": "op-recorded"})
		return
	}
	if s.options.omitPasswords {
		writeJSON(w, http.StatusOK, map[string]any{"operationId": "op-passwords"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"operationId": "op-passwords",
		"chat":        map[string]any{"username": "ada", "password": "chat-secret"},
		"git":         map[string]any{"username": "ws-ada", "password": "git-secret"},
	})
}

func (s *adminStub) record(r *http.Request) {
	recorded := recordedRequest{
		adminRequest:   adminRequest{Method: r.Method, Path: r.URL.Path},
		authorization:  r.Header.Get("Authorization"),
		idempotencyKey: r.Header.Get("Idempotency-Key"),
	}
	if query := r.URL.Query(); len(query) > 0 {
		recorded.Query = map[string]string{}
		for name := range query {
			recorded.Query[name] = query.Get(name)
		}
	}
	if data, err := io.ReadAll(r.Body); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &recorded.Body); err != nil {
			panic(fmt.Sprintf("the CLI sent a body that is not a JSON object: %s", data))
		}
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.requests = append(s.requests, recorded)
}

func (s *adminStub) serveRead(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/")
	segments := strings.Split(path, "/")
	switch {
	case path == "gateways":
		writeJSON(w, http.StatusOK, map[string]any{"gateways": s.gatewaySummaries()})
	case path == "placement":
		writeJSON(w, http.StatusOK, s.placement())
	case path == "operations":
		writeJSON(w, http.StatusOK, map[string]any{"operations": []map[string]any{{"id": "op-1", "kind": "deploy", "status": "running"}}})
	case path == "operations/op-1":
		writeJSON(w, http.StatusOK, map[string]any{"operation": map[string]any{
			"id":     "op-1",
			"kind":   "migrate-customer",
			"status": "failed",
			"steps":  []map[string]any{{"name": "quiesce-source", "status": "failed", "error": "source drain timed out"}},
		}})
	case path == "builds":
		writeJSON(w, http.StatusOK, map[string]any{"builds": []map[string]any{{"tag": "v42"}}})
	case path == "release-status":
		writeJSON(w, http.StatusOK, map[string]any{"summary": map[string]any{"status": "converged", "converged": 1, "serving": 1}, "gateways": []map[string]any{{"gatewayId": "shared-a", "desiredRelease": 3, "liveRelease": 3, "observation": "converged"}}})
	case path == "logs":
		fmt.Fprintln(w, `{"timestamp":"2026-11-10T00:00:00Z","severity":"WARNING","service":"api","message":"a line from the log stream"}`)
	case len(segments) == 3 && segments[0] == "gateways" && segments[2] == "details":
		writeJSON(w, http.StatusOK, map[string]any{"recentOperations": []any{}})
	case len(segments) == 3 && segments[0] == "gateways" && segments[2] == "releases":
		writeJSON(w, http.StatusOK, map[string]any{"releases": []map[string]any{{"release": 3, "imageTag": "v42", "live": true, "desired": true}}})
	case len(segments) == 3 && segments[0] == "users" && segments[2] == "broker-diagnostics":
		writeJSON(w, http.StatusOK, map[string]any{"windows": []any{}})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no route: " + path})
	}
}

func (s *adminStub) gatewaySummaries() []map[string]any {
	summaries := make([]map[string]any, 0, len(s.fixture.gateways))
	for _, gateway := range s.fixture.gateways {
		summaries = append(summaries, map[string]any{
			"gatewayId":     gateway.id,
			"gatewayClass":  gateway.class,
			"desiredStatus": gateway.desired,
			"status":        "ready",
			"imageTag":      "v42",
			"mountedUsers":  gateway.mounted,
			"ownerId":       gateway.owner,
			"lastError":     map[string]any{"message": "ensure failed on " + gateway.id},
			"podDiagnostics": map[string]any{
				"pods": []map[string]any{{"name": "pod-" + gateway.id, "ready": true}},
			},
		})
	}
	return summaries
}

func (s *adminStub) mountedOn(userID string) string {
	for _, gateway := range s.fixture.gateways {
		for _, mounted := range gateway.mounted {
			if mounted == userID {
				return gateway.id
			}
		}
	}
	return ""
}

func driftState(assigned, mounted string) string {
	switch {
	case assigned == mounted:
		return "healthy"
	case mounted == "":
		return "assigned-not-mounted"
	case assigned == "":
		return "mounted-orphan"
	default:
		return "pointer-drift"
	}
}

func (s *adminStub) placement() map[string]any {
	users := make([]map[string]any, 0, len(s.fixture.users))
	for _, user := range s.fixture.users {
		mounted := s.mountedOn(user.id)
		users = append(users, map[string]any{
			"id":                 user.id,
			"name":               user.name,
			"assignedGatewayId":  nullable(user.gatewayID),
			"mountedGatewayId":   nullable(mounted),
			"driftState":         driftState(user.gatewayID, mounted),
			"provisioningStatus": provisioningOf(user),
		})
	}
	gateways := make([]map[string]any, 0, len(s.fixture.gateways))
	for _, gateway := range s.fixture.gateways {
		gateways = append(gateways, map[string]any{"gatewayId": gateway.id, "gatewayClass": gateway.class, "desiredStatus": gateway.desired})
	}
	return map[string]any{
		"config":   map[string]any{"defaultGatewayId": "shared-a", "sharedGatewayIds": []string{"shared-a", "shared-b"}, "sharedGatewayAutoscaling": map[string]any{"enabled": true, "usersPerGateway": 40}},
		"gateways": gateways,
		"users":    users,
	}
}

func provisioningOf(user fixtureUser) string {
	if user.provisioning == "" {
		return provisioningComplete
	}
	return user.provisioning
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(data); err != nil {
		panic(err)
	}
}

type issuerStub struct {
	url          string
	refreshFails bool

	mutex sync.Mutex
	forms []url.Values
}

func (s *issuerStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if r.URL.Path == "/.well-known/openid-configuration" {
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                        s.url,
			"token_endpoint":                s.url + "/token",
			"device_authorization_endpoint": s.url + "/device",
		})
		return
	}
	if err := r.ParseForm(); err != nil {
		panic(err)
	}
	s.forms = append(s.forms, r.PostForm)
	switch {
	case r.URL.Path == "/device":
		writeJSON(w, http.StatusOK, map[string]any{
			"device_code":      "device-1",
			"user_code":        "ABCD-EFGH",
			"verification_uri": "https://issuer.example/activate",
			"expires_in":       600,
			"interval":         1,
		})
	case r.PostForm.Get("grant_type") != "refresh_token":
		writeJSON(w, http.StatusOK, map[string]any{"id_token": signedInToken, "refresh_token": "refresh-1"})
	case s.refreshFails:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant", "error_description": "revoked"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"id_token": refreshedToken, "refresh_token": "refresh-2"})
	}
}

func seedSession(t *testing.T, store oidc.Store, environment string, state sessionState, binding oidc.Binding) {
	t.Helper()
	var session oidc.Session
	switch state {
	case notSignedIn:
		return
	case signedIn:
		session = oidc.Session{IDToken: signedInToken, RefreshToken: "refresh-1"}
	case expiredSession:
		session = oidc.Session{IDToken: expiredToken, RefreshToken: "refresh-1"}
	case expiredWithoutRefresh:
		session = oidc.Session{IDToken: expiredToken}
	}
	session.Binding = binding
	if err := store.Save(environment, session); err != nil {
		t.Fatal(err)
	}
}

func execute(t *testing.T, fixture fleetFixture, options runOptions, argv []string) fleetRun {
	t.Helper()
	stub := &adminStub{fixture: fixture, options: options}
	server := httptest.NewServer(stub)
	defer server.Close()
	issuer := &issuerStub{refreshFails: options.refreshFails}
	issuerServer := httptest.NewServer(issuer)
	defer issuerServer.Close()
	issuer.url = issuerServer.URL
	settings := map[string]any{
		"adminApiOrigin":  server.URL,
		"imageRepository": "registry.example/gateway",
	}
	if options.tokenCommand != nil {
		settings["tokenCommand"] = options.tokenCommand
	} else {
		settings["oidc"] = map[string]any{"issuer": issuerServer.URL, "clientId": "fleet-cli"}
	}
	data, err := json.Marshal(map[string]any{"environments": map[string]any{fixture.environment: settings}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	stateHome := t.TempDir()
	store := oidc.Store{Dir: filepath.Join(stateHome, "octogee-fleet")}
	binding := oidc.Binding{Issuer: issuerServer.URL, ClientID: "fleet-cli", APIOrigin: server.URL}
	if options.alterBinding != nil {
		options.alterBinding(&binding)
	}
	seedSession(t, store, fixture.environment, options.session, binding)
	environment := map[string]string{"OCTOGEE_FLEET_CONFIG": path, "XDG_STATE_HOME": stateHome}
	var stdout, stderr bytes.Buffer
	runtime := Runtime{
		Stdout:  &stdout,
		Stderr:  &stderr,
		Getenv:  func(name string) string { return environment[name] },
		HTTP:    server.Client(),
		Sleep:   func(context.Context, time.Duration) error { return nil },
		Version: "test",
	}
	if options.defaultHTTP {
		runtime.HTTP = nil
	}
	exit := Run(context.Background(), argv, runtime)
	return fleetRun{
		exit:        exit,
		stdout:      stdout.String(),
		stderr:      stderr.String(),
		requests:    stub.requests,
		issuerForms: issuer.forms,
		sessionPath: filepath.Join(store.Dir, fixture.environment+".json"),
	}
}

func fleet(t *testing.T, argv ...string) fleetRun {
	t.Helper()
	return execute(t, fixture, runOptions{}, append([]string{"--env=" + fixture.environment}, argv...))
}

func fleetWith(t *testing.T, custom fleetFixture, options runOptions, argv ...string) fleetRun {
	t.Helper()
	return execute(t, custom, options, append([]string{"--env=" + custom.environment}, argv...))
}

func fleetWithoutEnvironment(t *testing.T, argv ...string) fleetRun {
	t.Helper()
	return execute(t, fixture, runOptions{}, argv)
}

func expectApplied(t *testing.T, run fleetRun, want adminRequest) {
	t.Helper()
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	if got := run.onlyMutation(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("sent %+v, want %+v", got, want)
	}
}

func expectRefused(t *testing.T, run fleetRun) {
	t.Helper()
	if run.exit == 0 {
		t.Fatalf("expected a non-zero exit: stdout=%s", run.stdout)
	}
	if sent := run.mutations(); len(sent) != 0 {
		t.Fatalf("expected no mutation, saw %+v", sent)
	}
}

func expectNothingSent(t *testing.T, run fleetRun) {
	t.Helper()
	expectRefused(t, run)
	if len(run.requests) != 0 {
		t.Fatalf("expected no requests, saw %+v", run.requests)
	}
}

func expectRead(t *testing.T, run fleetRun) {
	t.Helper()
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	if sent := run.mutations(); len(sent) != 0 {
		t.Fatalf("expected read requests only, saw %+v", sent)
	}
}

func expectContains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("expected %q in:\n%s", want, text)
	}
}
