package config

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const oidcSignIn = `"oidc":{"issuer":"https://issuer.example","clientId":"fleet-cli"}`

func environmentConfig(origin, signIn string) string {
	return `{"environments":{"test":{"adminApiOrigin":"` + origin + `","imageRepository":"registry.example/gateway",` + signIn + `}}}`
}

func TestLoadRefusesInvalidConfiguration(t *testing.T) {
	cases := map[string]string{
		"plain http to a remote host":     environmentConfig("http://admin.example", oidcSignIn),
		"an origin with a path":           environmentConfig("https://admin.example/api", oidcSignIn),
		"another scheme":                  environmentConfig("ftp://admin.example", oidcSignIn),
		"no way to sign in":               `{"environments":{"test":{"adminApiOrigin":"https://a.example","imageRepository":"r"}}}`,
		"two ways to sign in":             environmentConfig("https://admin.example", oidcSignIn+`,"tokenCommand":["printer"]`),
		"a plain http remote issuer":      environmentConfig("https://admin.example", `"oidc":{"issuer":"http://issuer.example","clientId":"c"}`),
		"an issuer without a client id":   environmentConfig("https://admin.example", `"oidc":{"issuer":"https://issuer.example"}`),
		"a static operator token":         environmentConfig("https://admin.example", `"operatorToken":{"env":"T"}`),
		"an unknown field":                environmentConfig("https://admin.example", oidcSignIn+`,"extra":1`),
		"no environments":                 `{"environments":{}}`,
		"no image repository":             `{"environments":{"test":{"adminApiOrigin":"https://a.example",` + oidcSignIn + `}}}`,
		"an environment name with a path": `{"environments":{"../test":{"adminApiOrigin":"https://a.example","imageRepository":"r",` + oidcSignIn + `}}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, content)); err == nil {
				t.Fatal("expected a refusal")
			}
		})
	}
}

func TestLoadAcceptsPlainHTTPToLoopbackOnly(t *testing.T) {
	for _, origin := range []string{"http://127.0.0.1:3001", "http://localhost:3001", "https://admin.example"} {
		if _, err := Load(writeConfig(t, environmentConfig(origin, oidcSignIn))); err != nil {
			t.Fatalf("%s: %v", origin, err)
		}
	}
}

func TestUnknownEnvironmentNamesTheKnownOnes(t *testing.T) {
	file, err := Load(writeConfig(t, environmentConfig("https://admin.example", oidcSignIn)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Environment("elsewhere")
	if err == nil || !strings.Contains(err.Error(), "test") {
		t.Fatalf("error %v", err)
	}
}

func tokenCommandEnvironment(t *testing.T, argv string) Environment {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	file, err := Load(writeConfig(t, environmentConfig("https://admin.example", `"tokenCommand":`+argv)))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := file.Environment("test")
	if err != nil {
		t.Fatal(err)
	}
	return environment
}

func TestTokenCommandSuppliesTheToken(t *testing.T) {
	environment := tokenCommandEnvironment(t, `["sh","-c","echo id-token"]`)
	token, err := environment.RunTokenCommand(context.Background())
	if err != nil || token != "id-token" {
		t.Fatalf("token %q, error %v", token, err)
	}
}

func TestTokenCommandRunsBesideTheConfiguration(t *testing.T) {
	environment := tokenCommandEnvironment(t, `["sh","-c","cat token.txt"]`)
	if err := os.WriteFile(filepath.Join(environment.dir, "token.txt"), []byte("beside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := environment.RunTokenCommand(context.Background())
	if err != nil || token != "beside" {
		t.Fatalf("token %q, error %v", token, err)
	}
}

func TestAFailedTokenCommandReportsItsError(t *testing.T) {
	environment := tokenCommandEnvironment(t, `["sh","-c","echo nope >&2; exit 3"]`)
	_, err := environment.RunTokenCommand(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error %v", err)
	}
}

func TestATokenCommandPrintingNothingIsRefused(t *testing.T) {
	environment := tokenCommandEnvironment(t, `["sh","-c","true"]`)
	if _, err := environment.RunTokenCommand(context.Background()); err == nil {
		t.Fatal("expected a refusal")
	}
}

func TestPathPrefersTheExplicitConfiguration(t *testing.T) {
	path, err := Path(func(name string) string {
		if name == "FLEET_CONFIG" {
			return "/explicit/config.json"
		}
		return "/xdg"
	})
	if err != nil || path != "/explicit/config.json" {
		t.Fatalf("path %q, error %v", path, err)
	}
}

func TestPathFallsBackToTheXDGDirectory(t *testing.T) {
	path, err := Path(func(name string) string {
		if name == "XDG_CONFIG_HOME" {
			return "/xdg"
		}
		return ""
	})
	if err != nil || path != filepath.Join("/xdg", "fleet", "config.json") {
		t.Fatalf("path %q, error %v", path, err)
	}
}
