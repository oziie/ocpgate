package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/oziie/ocpgate/internal/registry"
)

const (
	// challengingClientID is OCP's built-in OAuth client that answers a
	// Basic-auth challenge with a Bearer token instead of redirecting to
	// the browser login page. This is the same client `oc login` uses.
	challengingClientID = "openshift-challenging-client"

	// discoveryPath is the unauthenticated endpoint on the cluster API
	// that advertises where the OAuth server actually lives.
	discoveryPath = "/.well-known/oauth-authorization-server"

	defaultTimeout = 30 * time.Second
)

// OCPAuthenticator implements Authenticator against a cluster's OAuth
// server, which is configured to validate credentials against LDAP.
type OCPAuthenticator struct {
	client *http.Client
}

type options struct {
	timeout               time.Duration
	insecureSkipTLSVerify bool
	httpClient            *http.Client
}

// Option customizes an OCPAuthenticator.
type Option func(*options)

// WithTimeout bounds each HTTP request made during authentication.
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout = d }
}

// WithInsecureSkipTLSVerify disables verification of the cluster's
// certificate chain. It exists because OCP clusters are routinely fronted
// by an internal CA, and mirrors `oc login --insecure-skip-tls-verify`.
func WithInsecureSkipTLSVerify(skip bool) Option {
	return func(o *options) { o.insecureSkipTLSVerify = skip }
}

// WithHTTPClient supplies a preconfigured client — used by tests, and by
// callers that need a custom CA bundle or proxy. Redirect handling is
// overridden on a copy, since the whole flow depends on reading the
// redirect rather than following it.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient = c }
}

// NewOCPAuthenticator builds an Authenticator for OCP's challenging-client
// OAuth flow.
func NewOCPAuthenticator(opts ...Option) *OCPAuthenticator {
	o := options{timeout: defaultTimeout}
	for _, apply := range opts {
		apply(&o)
	}

	var client http.Client
	if o.httpClient != nil {
		client = *o.httpClient
	}
	if client.Transport == nil || o.insecureSkipTLSVerify {
		transport, _ := client.Transport.(*http.Transport)
		if transport == nil {
			transport = http.DefaultTransport.(*http.Transport).Clone()
		} else {
			transport = transport.Clone()
		}
		if o.insecureSkipTLSVerify {
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = &tls.Config{}
			}
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
		client.Transport = transport
	}
	if o.timeout > 0 {
		client.Timeout = o.timeout
	}

	// The token arrives in the Location header of a redirect that points
	// at a URL we must never actually fetch, so stop at the redirect.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &OCPAuthenticator{client: &client}
}

// Authenticate exchanges LDAP credentials for a short-lived Bearer token
// scoped to cluster.
func (a *OCPAuthenticator) Authenticate(ctx context.Context, cluster registry.ClusterEntry, creds Credentials) (*AuthResult, error) {
	if err := cluster.Validate(); err != nil {
		return nil, err
	}
	if !cluster.Active {
		return nil, &ErrClusterInactive{Name: cluster.Name}
	}
	if err := creds.Validate(); err != nil {
		return nil, err
	}

	authorizeURL := a.discoverAuthorizeEndpoint(ctx, cluster.APIEndpoint)
	return a.requestToken(ctx, authorizeURL, creds)
}

// discoverAuthorizeEndpoint asks the cluster API where its OAuth server
// lives. On a real cluster the OAuth route (oauth-openshift.apps.<domain>)
// is a different host from the API, so discovery is the only reliable way
// to find it. When discovery is unavailable, fall back to the API host's
// own /oauth/authorize rather than failing — some clusters serve it there,
// and a wrong guess surfaces as a clear auth error one request later.
func (a *OCPAuthenticator) discoverAuthorizeEndpoint(ctx context.Context, apiEndpoint string) string {
	fallback := strings.TrimSuffix(apiEndpoint, "/") + "/oauth/authorize"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(apiEndpoint, "/")+discoveryPath, nil)
	if err != nil {
		return fallback
	}
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fallback
	}

	var metadata struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil || metadata.AuthorizationEndpoint == "" {
		return fallback
	}
	return metadata.AuthorizationEndpoint
}

// requestToken performs the challenge request and reads the token out of
// the resulting redirect.
func (a *OCPAuthenticator) requestToken(ctx context.Context, authorizeURL string, creds Credentials) (*AuthResult, error) {
	u, err := url.Parse(authorizeURL)
	if err != nil {
		return nil, fmt.Errorf("invalid authorize endpoint %q: %w", authorizeURL, err)
	}

	q := u.Query()
	q.Set("client_id", challengingClientID)
	q.Set("response_type", "token")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build authorize request: %w", err)
	}
	req.SetBasicAuth(creds.Username, creds.Password)
	// Without this header OCP treats the call as a browser session and
	// serves the HTML login page instead of issuing a Basic challenge.
	req.Header.Set("X-CSRF-Token", "1")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contact oauth server: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, ErrInvalidCredentials
	case isRedirect(resp.StatusCode):
		location := resp.Header.Get("Location")
		if location == "" {
			return nil, fmt.Errorf("oauth server returned %d without a Location header", resp.StatusCode)
		}
		result, err := parseTokenFromLocation(location, time.Now())
		if err != nil {
			return nil, err
		}
		result.Username = creds.Username
		return result, nil
	default:
		return nil, fmt.Errorf("unexpected response from oauth server: %s", resp.Status)
	}
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// parseTokenFromLocation extracts the token from the redirect target. OCP
// returns it in the URL fragment (#access_token=...&expires_in=...), which
// keeps it out of server access logs.
func parseTokenFromLocation(location string, now time.Time) (*AuthResult, error) {
	u, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("parse oauth redirect: %w", err)
	}

	raw := u.Fragment
	if raw == "" {
		raw = u.RawQuery
	}

	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, fmt.Errorf("parse oauth redirect parameters: %w", err)
	}

	if oauthErr := values.Get("error"); oauthErr != "" {
		if oauthErr == "access_denied" {
			return nil, ErrInvalidCredentials
		}
		if desc := values.Get("error_description"); desc != "" {
			return nil, fmt.Errorf("oauth error %s: %s", oauthErr, desc)
		}
		return nil, fmt.Errorf("oauth error: %s", oauthErr)
	}

	token := values.Get("access_token")
	if token == "" {
		return nil, fmt.Errorf("oauth redirect contained no access_token")
	}

	result := &AuthResult{Token: token}
	if expiresIn := values.Get("expires_in"); expiresIn != "" {
		seconds, err := strconv.Atoi(expiresIn)
		if err != nil {
			return nil, fmt.Errorf("parse expires_in %q: %w", expiresIn, err)
		}
		result.ExpiresAt = now.Add(time.Duration(seconds) * time.Second).UTC()
	}
	return result, nil
}
