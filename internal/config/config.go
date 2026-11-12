// Package config reads the operator's configuration: which environments exist,
// where each one's Admin API lives, and how the operator signs in to it.
package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/octogee-ai/octogee-fleet/internal/oidc"
)

const tokenCommandTimeout = time.Minute

var environmentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Environment is one named deployment of the platform. An operator signs in
// through OIDC; automation names a command that prints an OIDC ID token.
type Environment struct {
	AdminAPIOrigin string         `json:"adminApiOrigin"`
	OIDC           *oidc.Settings `json:"oidc,omitempty"`
	TokenCommand   []string       `json:"tokenCommand,omitempty"`
	dir            string
}

// File is a parsed operator configuration.
type File struct {
	Environments map[string]Environment `json:"environments"`
}

// Path returns the configuration file the operator selected.
func Path(getenv func(string) string) (string, error) {
	if path := getenv("OCTOGEE_FLEET_CONFIG"); path != "" {
		return path, nil
	}
	if dir := getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "octogee-fleet", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the operator configuration: %w", err)
	}
	return filepath.Join(home, ".config", "octogee-fleet", "config.json"), nil
}

// Load reads and validates a configuration file.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the operator configuration: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file File
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(file.Environments) == 0 {
		return nil, fmt.Errorf("%s names no environments", path)
	}
	dir := filepath.Dir(path)
	for name, environment := range file.Environments {
		if !environmentName.MatchString(name) {
			return nil, fmt.Errorf("environment name %q may use only letters, digits, dots, dashes and underscores", name)
		}
		if err := environment.validate(); err != nil {
			return nil, fmt.Errorf("environment %q: %w", name, err)
		}
		environment.dir = dir
		file.Environments[name] = environment
	}
	return &file, nil
}

// Environment returns the named environment.
func (f *File) Environment(name string) (Environment, error) {
	environment, ok := f.Environments[name]
	if !ok {
		names := make([]string, 0, len(f.Environments))
		for known := range f.Environments {
			names = append(names, known)
		}
		sort.Strings(names)
		return Environment{}, fmt.Errorf("unknown environment %q; the operator configuration names: %s", name, strings.Join(names, ", "))
	}
	return environment, nil
}

func (e Environment) validate() error {
	if err := validateOrigin(e.AdminAPIOrigin); err != nil {
		return err
	}
	if (e.OIDC == nil) == (len(e.TokenCommand) == 0) {
		return errors.New("name exactly one of oidc or tokenCommand")
	}
	if e.OIDC != nil {
		return e.OIDC.Validate()
	}
	return nil
}

func validateOrigin(origin string) error {
	if err := oidc.CheckSecure(origin); err != nil {
		return fmt.Errorf("adminApiOrigin: %w", err)
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("adminApiOrigin %q is not an origin", origin)
	}
	if strings.TrimRight(parsed.Path, "/") != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("adminApiOrigin %q must be an origin with no path", origin)
	}
	return nil
}

// RunTokenCommand prints an OIDC ID token from the environment's command.
func (e Environment) RunTokenCommand(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tokenCommandTimeout)
	defer cancel()
	argv := e.TokenCommand
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = e.dir
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("token command %q failed: %w: %s", argv[0], err, strings.TrimSpace(stderr.String()))
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", fmt.Errorf("token command %q printed nothing", argv[0])
	}
	return token, nil
}
