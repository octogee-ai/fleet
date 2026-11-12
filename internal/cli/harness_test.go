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

var fixedNow = time.Date(2026, 11, 10, 0, 5, 0, 0, time.UTC)

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
	// target is the placement target the Admin API reports; empty means the user's gateway.
	target string
}

type fixtureGateway struct {
	id, class, desired, owner string
	mounted                   []string
	vm, stopReason            string
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
		{id: "iso-grace", class: "isolated", desired: "running", owner: "u-grace", mounted: []string{"u-grace"}, vm: "SUSPENDED", stopReason: "idle"},
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
	rejectWith       int
	session          sessionState
	refreshFails     bool
	tokenCommand     []string
	redirectTo       string
	defaultHTTP      bool
	omitPasswords    bool
	alterBinding     func(*oidc.Binding)
	withoutDryRuns   bool
	refuseDryRunWith int
	appliesDryRuns   bool
	plansNothing     bool
	env              map[string]string
	errorCode        string
	refuseApplyWith  int
	detailsFailWith  int
	logsHang         bool
	logsTruncated    bool
	historyLimit     time.Duration
}

type fleetRun struct {
	exit        int
	stdout      string
	stderr      string
	requests    []recordedRequest
	issuerForms []url.Values
	sessionPath string
}

func (r recordedRequest) isDryRun() bool { return r.Query["dryRun"] == "true" }

func (r fleetRun) mutations() []recordedRequest {
	var sent []recordedRequest
	for _, request := range r.requests {
		if request.Method != http.MethodGet && !request.isDryRun() {
			sent = append(sent, request)
		}
	}
	return sent
}

func (r fleetRun) dryRuns() []recordedRequest {
	var sent []recordedRequest
	for _, request := range r.requests {
		if request.Method != http.MethodGet && request.isDryRun() {
			sent = append(sent, request)
		}
	}
	return sent
}

func (r fleetRun) onlyDryRun(t *testing.T) recordedRequest {
	t.Helper()
	sent := r.dryRuns()
	if len(sent) != 1 {
		t.Fatalf("expected exactly one dry run, saw %d: exit=%d stderr=%s", len(sent), r.exit, r.stderr)
	}
	return sent[0]
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
	if !s.options.withoutDryRuns {
		w.Header().Set("Admin-Api-Capabilities", "dry-run")
	}
	if s.options.redirectTo != "" {
		http.Redirect(w, r, s.options.redirectTo, http.StatusTemporaryRedirect)
		return
	}
	if s.options.rejectWith != 0 {
		writeJSON(w, s.options.rejectWith, s.refusal("token rejected"))
		return
	}
	if r.Method != http.MethodGet {
		s.serveMutation(w, r)
		return
	}
	s.serveRead(w, r)
}

func (s *adminStub) serveDryRun(w http.ResponseWriter, r *http.Request) {
	if s.options.refuseDryRunWith != 0 {
		writeJSON(w, s.options.refuseDryRunWith, s.refusal("gateway has a current retirement claim"))
		return
	}
	if s.options.plansNothing {
		writeJSON(w, http.StatusOK, map[string]any{"dryRun": true, "plans": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dryRun": true,
		"plans": []map[string]any{{
			"what":            "change the fleet",
			"why":             "Admin fleet request",
			"kind":            "change-fleet",
			"target":          map[string]any{"path": strings.TrimPrefix(r.URL.Path, "/api/admin")},
			"preActionStatus": map[string]any{"desiredStatus": "running", "desiredRelease": 3},
			"mutation":        map[string]any{"kind": "change-fleet"},
			"forced":          s.lastBody()["force"] == true,
		}},
	})
}

func (s *adminStub) refusal(message string) map[string]any {
	body := map[string]any{"error": message}
	if s.options.errorCode != "" {
		body["code"] = s.options.errorCode
	}
	return body
}

func (s *adminStub) recorded() []recordedRequest {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]recordedRequest{}, s.requests...)
}

func (s *adminStub) lastBody() map[string]any {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.requests[len(s.requests)-1].Body
}

func (s *adminStub) serveMutation(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("dryRun") == "true" && !s.options.appliesDryRuns {
		s.serveDryRun(w, r)
		return
	}
	if s.options.refuseApplyWith != 0 {
		writeJSON(w, s.options.refuseApplyWith, s.refusal("a migration is in flight for this gateway"))
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/one-time-passwords") {
		status, body := appliedAnswer(r)
		writeJSON(w, status, body)
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

// appliedAnswer is what the Admin API returns for an applied mutation. Not
// every route returns an operation id.
func appliedAnswer(r *http.Request) (int, map[string]any) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/")
	segments := strings.Split(path, "/")
	switch {
	case r.Method == http.MethodPatch:
		return http.StatusAccepted, map[string]any{"gatewayId": segments[1], "updated": []string{"desiredStatus"}}
	case strings.HasSuffix(path, "/assign"):
		return http.StatusAccepted, map[string]any{"uid": segments[1], "gatewayId": "shared-b", "gatewayProfile": "shared"}
	case strings.HasSuffix(path, "/unassign"):
		return http.StatusOK, map[string]any{"unassigned": true, "uid": segments[1], "from": "shared-a"}
	case strings.HasSuffix(path, "/delete"):
		return http.StatusAccepted, map[string]any{
			"status": "deletion_scheduled", "deletionId": "deletion-1",
			"suspensionOperationId": "op-suspend", "erasureOperationId": "op-erase",
		}
	case path == "users":
		return http.StatusCreated, map[string]any{"userId": "u-new", "operationId": "op-recorded"}
	}
	return http.StatusAccepted, map[string]any{"operationId": "op-recorded"}
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
		writeJSON(w, http.StatusOK, map[string]any{"operations": []map[string]any{
			{"id": "op-1", "kind": "deploy", "status": "running", "createdBy": "operator@example.test", "createdAt": minutesAgo(3)},
			{"id": "audit-1", "recordType": "audit", "what": "update gateway desired configuration", "status": "completed", "createdBy": "operator@example.test", "createdAt": minutesAgo(4)},
		}})
	case path == "operations/op-1":
		writeJSON(w, http.StatusOK, map[string]any{"operation": map[string]any{
			"id":        "op-1",
			"kind":      "migrate-customer",
			"status":    "failed",
			"createdBy": "operator@example.test",
			"createdAt": minutesAgo(3),
			"gatewayId": "shared-b",
			"error":     "migration failed at quiesce-source",
			"steps": []map[string]any{
				{"name": "reserve-target", "status": "completed"},
				{"name": "quiesce-source", "status": "failed", "error": "source drain timed out"},
			},
		}})
	case path == "builds":
		writeJSON(w, http.StatusOK, map[string]any{"service": "gateway", "generatedAt": "2026-11-10T00:05:00Z", "builds": []map[string]any{
			{"tag": "v42", "pushedAt": "2026-11-09T22:05:00Z", "digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		}})
	case path == "release-status":
		writeJSON(w, http.StatusOK, map[string]any{"summary": map[string]any{"status": "converged", "converged": 1, "serving": 1}, "gateways": []map[string]any{{"gatewayId": "shared-a", "desiredRelease": 3, "liveRelease": 3, "observation": "converged"}}})
	case path == "logs" && s.options.logsHang:
		<-r.Context().Done()
	case path == "logs":
		fmt.Fprintln(w, `{"timestamp":"2026-11-10T00:00:00Z","severity":"WARNING","service":"api","message":"a line from the log stream"}`)
		if s.options.logsTruncated {
			fmt.Fprintln(w, `{"timestamp":"2026-11-10T00:00:01Z","severity":"WARNING","service":"fleet","instance":"admin-api","message":"Older entries in this window were not read. Shorten since or add a filter.","details":{"truncated":true}}`)
		}
	case len(segments) == 3 && segments[0] == "gateways" && segments[2] == "details":
		s.serveDetails(w, segments[1])
	case len(segments) == 3 && segments[0] == "gateways" && segments[2] == "releases":
		writeJSON(w, http.StatusOK, map[string]any{"releases": []map[string]any{{"release": 3, "imageTag": "v42", "live": true, "desired": true}}})
	case len(segments) == 3 && segments[0] == "users" && segments[2] == "broker-diagnostics":
		writeJSON(w, http.StatusOK, map[string]any{"windows": []map[string]any{
			{"windowStartMs": minutesAgo(65), "totalTokens": 12000, "requestCount": 3, "costUsd": 0.42},
		}})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no route: " + path})
	}
}

func (s *adminStub) serveDetails(w http.ResponseWriter, id string) {
	if s.options.detailsFailWith != 0 {
		writeJSON(w, s.options.detailsFailWith, s.refusal("details are unavailable"))
		return
	}
	for _, gateway := range s.fixture.gateways {
		if gateway.id == id {
			writeJSON(w, http.StatusOK, s.details(gateway))
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "Gateway not found", "gatewayId": id})
}

func (s *adminStub) details(gateway fixtureGateway) map[string]any {
	mounted := make([]map[string]any, 0, len(gateway.mounted))
	for _, uid := range gateway.mounted {
		for _, user := range s.fixture.users {
			if user.id == uid {
				mounted = append(mounted, map[string]any{"uid": uid, "slug": user.name, "username": user.name, "email": user.name + "@example.test", "lastActive": "2026-11-09T23:05:00Z"})
			}
		}
	}
	var vm any
	if gateway.vm != "" {
		vm = map[string]any{
			"instanceName": "vm-" + gateway.id, "zone": "zone-a", "instanceId": "42", "internalIp": "10.0.0.9",
			"status": gateway.vm, "startedAt": minutesAgo(90), "provisioningModel": "SPOT",
			"stopReason": nullable(gateway.stopReason), "updatedAt": minutesAgo(30),
		}
	}
	return map[string]any{
		"mountedUsers": mounted,
		"recentOperations": []map[string]any{
			{"id": "op-9", "kind": "restart", "status": "completed", "createdBy": "operator@example.test", "createdAt": minutesAgo(20)},
		},
		"control": map[string]any{
			"desiredStatus": gateway.desired, "desiredRelease": 3, "liveRelease": 3,
			"gatewayClass": gateway.class, "ownerUid": nullable(gateway.owner), "vm": vm,
		},
		"generatedAt": "2026-11-10T00:05:00Z",
	}
}

func minutesAgo(minutes int) int64 {
	return fixedNow.Add(-time.Duration(minutes) * time.Minute).UnixMilli()
}

func (s *adminStub) gatewaySummaries() []map[string]any {
	summaries := make([]map[string]any, 0, len(s.fixture.gateways))
	for _, gateway := range s.fixture.gateways {
		summaries = append(summaries, map[string]any{
			"gatewayId":       gateway.id,
			"gatewayClass":    gateway.class,
			"desiredStatus":   gateway.desired,
			"status":          "ready",
			"imageTag":        "v42",
			"mountedUsers":    gateway.mounted,
			"ownerId":         gateway.owner,
			"lastHeartbeatAt": "2026-11-10T00:02:00Z",
			"lastError":       map[string]any{"message": "ensure failed on " + gateway.id},
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

// driftState mirrors the order in which the Admin API classifies placement.
func (s *adminStub) driftState(assigned, mounted, target string) string {
	missing := func(id string) bool {
		if id == "" {
			return false
		}
		for _, gateway := range s.fixture.gateways {
			if gateway.id == id {
				return gateway.desired == "retired"
			}
		}
		return true
	}
	switch {
	case missing(target) || missing(assigned):
		return "missing-gateway"
	case assigned != "" && target != "" && assigned != target:
		return "pointer-drift"
	case mounted != "" && mounted != assigned:
		return "mounted-orphan"
	case assigned != "" && mounted != assigned:
		return "assigned-not-mounted"
	}
	return "healthy"
}

func (s *adminStub) placement() map[string]any {
	users := make([]map[string]any, 0, len(s.fixture.users))
	for _, user := range s.fixture.users {
		mounted := s.mountedOn(user.id)
		target := user.target
		if target == "" {
			target = user.gatewayID
		}
		users = append(users, map[string]any{
			"id":                    user.id,
			"name":                  user.name,
			"email":                 user.name + "@example.test",
			"policyTargetGatewayId": nullable(target),
			"assignedGatewayId":     nullable(user.gatewayID),
			"mountedGatewayId":      nullable(mounted),
			"driftState":            s.driftState(user.gatewayID, mounted, target),
			"provisioningStatus":    provisioningOf(user),
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
		"adminApiOrigin": server.URL,
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
	for name, value := range options.env {
		environment[name] = value
	}
	var stdout, stderr bytes.Buffer
	runtime := Runtime{
		Stdout:  &stdout,
		Stderr:  &stderr,
		Getenv:  func(name string) string { return environment[name] },
		HTTP:    server.Client(),
		Sleep:   func(context.Context, time.Duration) error { return nil },
		Now:     func() time.Time { return fixedNow },
		Version: "test",

		historyLimit: options.historyLimit,
	}
	if options.defaultHTTP {
		runtime.HTTP = nil
	}
	exit := Run(context.Background(), argv, runtime)
	return fleetRun{
		exit:        exit,
		stdout:      stdout.String(),
		stderr:      stderr.String(),
		requests:    stub.recorded(),
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
