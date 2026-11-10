// Package oidc signs an operator in to an OpenID Connect issuer with the device
// authorization grant and keeps the resulting session on disk. It never checks
// a token's signature: the Admin API does that.
package oidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultPollInterval = 5 * time.Second
	slowDownStep        = 5 * time.Second
	expirySkew          = time.Minute
	requestTimeout      = 30 * time.Second
	deviceGrant         = "urn:ietf:params:oauth:grant-type:device_code"
	maxResponseBytes    = 1 << 20
	maxRedirects        = 10
)

var defaultScopes = []string{"openid", "email", "offline_access"}

// Doer sends one HTTP request.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Settings names the issuer and the client fleet signs in as.
type Settings struct {
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

// Validate reports whether the settings can be used to sign in.
func (s Settings) Validate() error {
	if err := CheckSecure(s.Issuer); err != nil {
		return fmt.Errorf("issuer: %w", err)
	}
	if s.ClientID == "" {
		return errors.New("clientId is required")
	}
	return nil
}

// CheckSecure refuses a URL that would carry credentials in plain text.
func CheckSecure(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%q is not a URL", raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopback(parsed.Hostname()) {
			return nil
		}
		return fmt.Errorf("%q would send credentials in plain text; use https", raw)
	default:
		return fmt.Errorf("%q must use https", raw)
	}
}

// NewHTTPClient returns a client that will not follow a redirect to a URL that
// would carry credentials in plain text.
func NewHTTPClient() *http.Client {
	return &http.Client{CheckRedirect: refuseInsecureRedirect}
}

func refuseInsecureRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if err := CheckSecure(request.URL.String()); err != nil {
		return fmt.Errorf("refused a redirect: %w", err)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Session is what a sign-in leaves behind.
type Session struct {
	IDToken      string  `json:"idToken"`
	RefreshToken string  `json:"refreshToken,omitempty"`
	Binding      Binding `json:"binding"`
}

// Binding names the configuration a session was signed in under.
type Binding struct {
	Issuer    string `json:"issuer"`
	ClientID  string `json:"clientId"`
	APIOrigin string `json:"apiOrigin"`
}

type claims struct {
	Expiry  int64  `json:"exp"`
	Email   string `json:"email"`
	Subject string `json:"sub"`
}

func decodeClaims(idToken string) (claims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return claims{}, errors.New("the ID token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims{}, fmt.Errorf("decode the ID token: %w", err)
	}
	var decoded claims
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return claims{}, fmt.Errorf("decode the ID token: %w", err)
	}
	return decoded, nil
}

// Valid reports whether the ID token is well formed and not about to expire.
func (s Session) Valid(now time.Time) bool {
	decoded, err := decodeClaims(s.IDToken)
	return err == nil && now.Add(expirySkew).Before(time.Unix(decoded.Expiry, 0))
}

// Identity names who the session belongs to.
func (s Session) Identity() string {
	decoded, err := decodeClaims(s.IDToken)
	if err != nil {
		return "unknown"
	}
	if decoded.Email != "" {
		return decoded.Email
	}
	return decoded.Subject
}

// DeviceCode is what the operator needs to approve a sign-in.
type DeviceCode struct {
	VerificationURI         string
	VerificationURIComplete string
	UserCode                string
}

// Client talks to one issuer.
type Client struct {
	Settings Settings
	HTTP     Doer
	Sleep    func(context.Context, time.Duration) error
}

type endpoints struct {
	Issuer string `json:"issuer"`
	Token  string `json:"token_endpoint"`
	Device string `json:"device_authorization_endpoint"`
}

func (c *Client) discover(ctx context.Context) (endpoints, error) {
	target := strings.TrimRight(c.Settings.Issuer, "/") + "/.well-known/openid-configuration"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return endpoints{}, fmt.Errorf("build the discovery request: %w", err)
	}
	var found endpoints
	if _, err := c.exchange(request, &found); err != nil {
		return endpoints{}, fmt.Errorf("discover the issuer: %w", err)
	}
	if found.Issuer != c.Settings.Issuer {
		return endpoints{}, fmt.Errorf("the issuer identifies itself as %q, not %q", found.Issuer, c.Settings.Issuer)
	}
	if err := CheckSecure(found.Token); err != nil {
		return endpoints{}, fmt.Errorf("token endpoint: %w", err)
	}
	return found, nil
}

func (c *Client) exchange(request *http.Request, out any) (int, error) {
	ctx, cancel := context.WithTimeout(request.Context(), requestTimeout)
	defer cancel()
	response, err := c.HTTP.Do(request.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return response.StatusCode, fmt.Errorf("read the response: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return response.StatusCode, fmt.Errorf("the issuer returned %d with an unreadable body", response.StatusCode)
	}
	return response.StatusCode, nil
}

func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values, out any) (int, error) {
	form.Set("client_id", c.Settings.ClientID)
	if c.Settings.ClientSecret != "" {
		form.Set("client_secret", c.Settings.ClientSecret)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, fmt.Errorf("build the issuer request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	return c.exchange(request, out)
}

func (c *Client) scopes() string {
	if len(c.Settings.Scopes) > 0 {
		return strings.Join(c.Settings.Scopes, " ")
	}
	return strings.Join(defaultScopes, " ")
}

type deviceResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURL         string `json:"verification_url"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	Error                   string `json:"error"`
	ErrorDescription        string `json:"error_description"`
}

type tokenResponse struct {
	IDToken          string `json:"id_token"`
	RefreshToken     string `json:"refresh_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Login runs the device authorization grant to completion.
func (c *Client) Login(ctx context.Context, show func(DeviceCode)) (Session, error) {
	found, err := c.discover(ctx)
	if err != nil {
		return Session{}, err
	}
	if found.Device == "" {
		return Session{}, errors.New("the issuer does not offer the device authorization grant")
	}
	if err := CheckSecure(found.Device); err != nil {
		return Session{}, fmt.Errorf("device endpoint: %w", err)
	}
	var device deviceResponse
	if _, err := c.postForm(ctx, found.Device, url.Values{"scope": {c.scopes()}}, &device); err != nil {
		return Session{}, fmt.Errorf("start the sign-in: %w", err)
	}
	if device.DeviceCode == "" {
		return Session{}, fmt.Errorf("the issuer refused the sign-in: %s %s", device.Error, device.ErrorDescription)
	}
	verification := device.VerificationURI
	if verification == "" {
		verification = device.VerificationURL
	}
	show(DeviceCode{VerificationURI: verification, VerificationURIComplete: device.VerificationURIComplete, UserCode: device.UserCode})
	return c.poll(ctx, found.Token, device)
}

func (c *Client) poll(ctx context.Context, tokenEndpoint string, device deviceResponse) (Session, error) {
	interval := defaultPollInterval
	if device.Interval > 0 {
		interval = time.Duration(device.Interval) * time.Second
	}
	lifetime := time.Duration(device.ExpiresIn) * time.Second
	var waited time.Duration
	for {
		if err := c.Sleep(ctx, interval); err != nil {
			return Session{}, err
		}
		waited += interval
		if lifetime > 0 && waited > lifetime {
			return Session{}, errors.New("the sign-in code expired before it was approved")
		}
		var token tokenResponse
		form := url.Values{"grant_type": {deviceGrant}, "device_code": {device.DeviceCode}}
		if _, err := c.postForm(ctx, tokenEndpoint, form, &token); err != nil {
			return Session{}, fmt.Errorf("finish the sign-in: %w", err)
		}
		switch token.Error {
		case "":
			return sessionFrom(token)
		case "authorization_pending":
		case "slow_down":
			interval += slowDownStep
		default:
			return Session{}, fmt.Errorf("the sign-in failed: %s %s", token.Error, token.ErrorDescription)
		}
	}
}

func sessionFrom(token tokenResponse) (Session, error) {
	if token.IDToken == "" {
		return Session{}, errors.New("the issuer returned no ID token")
	}
	return Session{IDToken: token.IDToken, RefreshToken: token.RefreshToken}, nil
}

// Refresh exchanges a session's refresh token for a new ID token.
func (c *Client) Refresh(ctx context.Context, session Session) (Session, error) {
	found, err := c.discover(ctx)
	if err != nil {
		return Session{}, err
	}
	var token tokenResponse
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {session.RefreshToken}}
	if _, err := c.postForm(ctx, found.Token, form, &token); err != nil {
		return Session{}, err
	}
	if token.Error != "" {
		return Session{}, fmt.Errorf("%s %s", token.Error, token.ErrorDescription)
	}
	next, err := sessionFrom(token)
	if err != nil {
		return Session{}, err
	}
	if next.RefreshToken == "" {
		next.RefreshToken = session.RefreshToken
	}
	next.Binding = session.Binding
	return next, nil
}
