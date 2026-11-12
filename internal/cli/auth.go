package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/octogee-ai/octogee-fleet/internal/config"
	"github.com/octogee-ai/octogee-fleet/internal/oidc"
)

var authCommands = []*command{
	{path: words("login"), summary: "Sign in to the environment's OIDC issuer.", run: login},
	{path: words("logout"), summary: "Forget this environment's sign-in.", run: logout},
}

func sessionStore(runtime Runtime) (oidc.Store, error) {
	dir, err := oidc.StateDir(runtime.Getenv)
	if err != nil {
		return oidc.Store{}, err
	}
	return oidc.Store{Dir: dir}, nil
}

func oidcClient(environment config.Environment, runtime Runtime) *oidc.Client {
	return &oidc.Client{Settings: *environment.OIDC, HTTP: runtime.HTTP, Sleep: runtime.Sleep}
}

func sessionBinding(environment config.Environment) oidc.Binding {
	return oidc.Binding{Issuer: environment.OIDC.Issuer, ClientID: environment.OIDC.ClientID, APIOrigin: environment.AdminAPIOrigin}
}

func login(i *invocation) error {
	if i.environment.OIDC == nil {
		return fmt.Errorf("environment %q takes its token from a command; there is nothing to sign in to", i.name)
	}
	store, err := sessionStore(i.runtime)
	if err != nil {
		return err
	}
	session, err := oidcClient(i.environment, i.runtime).Login(i.ctx, func(code oidc.DeviceCode) {
		i.line(fmt.Sprintf("Open %s and enter the code %s", i.style.cyan(code.VerificationURI), i.style.bold(code.UserCode)))
		if code.VerificationURIComplete != "" {
			i.line("Or open " + i.style.cyan(code.VerificationURIComplete))
		}
		i.waiting.start("Waiting for you to approve the sign-in in a browser…")
	})
	i.waiting.end()
	if err != nil {
		return err
	}
	session.Binding = sessionBinding(i.environment)
	if err := store.Save(i.name, session); err != nil {
		return err
	}
	i.ok(fmt.Sprintf("signed in to %s as %s", i.name, i.style.bold(session.Identity())))
	return nil
}

func logout(i *invocation) error {
	store, err := sessionStore(i.runtime)
	if err != nil {
		return err
	}
	if err := store.Delete(i.name); err != nil {
		return err
	}
	i.ok("signed out of " + i.name)
	return nil
}

func bearerToken(name string, environment config.Environment, runtime Runtime) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if environment.OIDC == nil {
			return environment.RunTokenCommand(ctx)
		}
		return sessionToken(ctx, name, environment, runtime)
	}
}

func sessionToken(ctx context.Context, name string, environment config.Environment, runtime Runtime) (string, error) {
	store, err := sessionStore(runtime)
	if err != nil {
		return "", err
	}
	session, found, err := store.Load(name)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("not signed in to %s; run `octogee-fleet --env=%s login`", name, name)
	}
	if session.Binding != sessionBinding(environment) {
		return "", fmt.Errorf("the saved sign-in for %s was made under a different configuration; run `octogee-fleet --env=%s login`", name, name)
	}
	if session.Valid(time.Now()) {
		return session.IDToken, nil
	}
	if session.RefreshToken == "" {
		return "", fmt.Errorf("the sign-in to %s has expired; run `octogee-fleet --env=%s login`", name, name)
	}
	refreshed, err := oidcClient(environment, runtime).Refresh(ctx, session)
	if err != nil {
		return "", fmt.Errorf("the sign-in to %s could not be renewed (%w); run `octogee-fleet --env=%s login`", name, err, name)
	}
	if err := store.Save(name, refreshed); err != nil {
		return "", err
	}
	return refreshed.IDToken, nil
}
