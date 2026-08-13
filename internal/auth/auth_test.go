package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/oziie/ocpgate/internal/registry"
	"github.com/oziie/ocpgate/internal/retry"
)

const (
	testUser  = "john.doe"
	testPass  = "s3cret"
	testToken = "sha256~xf9Kq2LmN0pQrStUvWxYz"
)

// fakeOCP stands in for a cluster's API and OAuth server. It serves
// discovery and the challenging-client authorize endpoint, enforcing the
// same preconditions a real OCP OAuth server does.
type fakeOCP struct {
	t *testing.T

	// baseURL is filled in once the test server is listening, so the
	// discovery document can advertise the server's own address.
	baseURL        string
	serveDiscovery bool
	discoveryCalls int
	authorizeCalls int
	lastCSRFHeader string
	lastQuery      map[string]string

	// transientFailures makes the next N authorize calls return 503, to
	// simulate an OAuth server that is briefly unwell.
	transientFailures int
	// discoveryFailures does the same for the discovery endpoint.
	discoveryFailures int
}

func newFakeOCP(t *testing.T, serveDiscovery bool) (*fakeOCP, *httptest.Server) {
	t.Helper()

	f := &fakeOCP{t: t, serveDiscovery: serveDiscovery}
	server := httptest.NewTLSServer(f)
	t.Cleanup(server.Close)
	f.baseURL = server.URL
	return f, server
}

func (f *fakeOCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case discoveryPath:
		f.discoveryCalls++
		if f.discoveryFailures > 0 {
			f.discoveryFailures--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if !f.serveDiscovery {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q}`,
			f.baseURL, f.baseURL+"/oauth/authorize", f.baseURL+"/oauth/token")

	case "/oauth/authorize":
		f.authorizeCalls++
		if f.transientFailures > 0 {
			f.transientFailures--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f.lastCSRFHeader = r.Header.Get("X-CSRF-Token")
		f.lastQuery = map[string]string{
			"client_id":     r.URL.Query().Get("client_id"),
			"response_type": r.URL.Query().Get("response_type"),
		}

		user, pass, ok := r.BasicAuth()
		if !ok || user != testUser || pass != testPass {
			w.Header().Set("WWW-Authenticate", "Basic realm=\"openshift\"")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Location", fmt.Sprintf(
			"https://oauth-openshift.apps.example.com/oauth/token/implicit#access_token=%s&expires_in=86400&scope=user%%3Afull&token_type=Bearer",
			testToken))
		w.WriteHeader(http.StatusFound)

	default:
		http.NotFound(w, r)
	}
}

func testCluster(server *httptest.Server) registry.ClusterEntry {
	return registry.ClusterEntry{
		Name:        "prod-cluster-1",
		APIEndpoint: server.URL,
		Environment: registry.EnvProduction,
		Region:      "eu-west",
		LDAPRealm:   "PROD",
		Active:      true,
	}
}

func newTestAuthenticator(server *httptest.Server) *OCPAuthenticator {
	return NewOCPAuthenticator(
		WithHTTPClient(server.Client()),
		WithTimeout(5*time.Second),
		// Same attempt count as production, without the real backoff.
		WithRetryPolicy(retry.Policy{Attempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}),
	)
}

func TestAuthenticateSuccessViaDiscovery(t *testing.T) {
	fake, server := newFakeOCP(t, true)
	auth := newTestAuthenticator(server)

	before := time.Now()
	result, err := auth.Authenticate(context.Background(), testCluster(server), Credentials{
		Username: testUser,
		Password: testPass,
	})
	require.NoError(t, err)

	assert.Equal(t, testToken, result.Token)
	assert.Equal(t, testUser, result.Username)
	assert.WithinDuration(t, before.Add(86400*time.Second), result.ExpiresAt, 5*time.Second)

	assert.Equal(t, 1, fake.discoveryCalls, "discovery should be consulted")
	assert.Equal(t, 1, fake.authorizeCalls)
	assert.Equal(t, "1", fake.lastCSRFHeader, "OCP needs X-CSRF-Token to issue a Basic challenge")
	assert.Equal(t, challengingClientID, fake.lastQuery["client_id"])
	assert.Equal(t, "token", fake.lastQuery["response_type"])
}

func TestAuthenticateFallsBackWhenDiscoveryMissing(t *testing.T) {
	fake, server := newFakeOCP(t, false)
	auth := newTestAuthenticator(server)

	result, err := auth.Authenticate(context.Background(), testCluster(server), Credentials{
		Username: testUser,
		Password: testPass,
	})
	require.NoError(t, err)
	assert.Equal(t, testToken, result.Token)
	assert.Equal(t, 1, fake.authorizeCalls, "should fall back to <api>/oauth/authorize")
}

func TestAuthenticateRetriesTransientOAuthFailure(t *testing.T) {
	fake, server := newFakeOCP(t, true)
	fake.transientFailures = 2
	auth := newTestAuthenticator(server)

	result, err := auth.Authenticate(context.Background(), testCluster(server), Credentials{
		Username: testUser,
		Password: testPass,
	})
	require.NoError(t, err, "a brief 503 should not fail the login")

	assert.Equal(t, testToken, result.Token)
	assert.Equal(t, 3, fake.authorizeCalls, "two failures then success")
}

func TestAuthenticateGivesUpAfterRepeatedTransientFailures(t *testing.T) {
	fake, server := newFakeOCP(t, true)
	fake.transientFailures = 99
	auth := newTestAuthenticator(server)

	_, err := auth.Authenticate(context.Background(), testCluster(server), Credentials{
		Username: testUser,
		Password: testPass,
	})
	require.Error(t, err)

	assert.Equal(t, 3, fake.authorizeCalls, "should stop at the policy's attempt limit")
	assert.Contains(t, err.Error(), "after 3 attempts")
	assert.NotErrorIs(t, err, ErrInvalidCredentials,
		"an unavailable server must not be reported as a bad password")
}

func TestAuthenticateNeverRetriesRejectedCredentials(t *testing.T) {
	fake, server := newFakeOCP(t, true)
	auth := newTestAuthenticator(server)

	_, err := auth.Authenticate(context.Background(), testCluster(server), Credentials{
		Username: testUser,
		Password: "wrong-password",
	})
	require.ErrorIs(t, err, ErrInvalidCredentials)

	// Repeating a bad password is useless and risks an LDAP lockout.
	assert.Equal(t, 1, fake.authorizeCalls)
}

func TestDiscoveryRetriesBeforeFallingBack(t *testing.T) {
	fake, server := newFakeOCP(t, true)
	fake.discoveryFailures = 1
	auth := newTestAuthenticator(server)

	result, err := auth.Authenticate(context.Background(), testCluster(server), Credentials{
		Username: testUser,
		Password: testPass,
	})
	require.NoError(t, err)

	assert.Equal(t, testToken, result.Token)
	assert.Equal(t, 2, fake.discoveryCalls,
		"a transient discovery failure should be retried, not silently fall back")
}

func TestDiscoveryDoesNotRetryWhenUnpublished(t *testing.T) {
	fake, server := newFakeOCP(t, false)
	auth := newTestAuthenticator(server)

	_, err := auth.Authenticate(context.Background(), testCluster(server), Credentials{
		Username: testUser,
		Password: testPass,
	})
	require.NoError(t, err)

	// A 404 means the cluster does not publish discovery; retrying it
	// would just delay every login on such a cluster.
	assert.Equal(t, 1, fake.discoveryCalls)
}

func TestAuthenticateInvalidCredentials(t *testing.T) {
	_, server := newFakeOCP(t, true)
	auth := newTestAuthenticator(server)

	result, err := auth.Authenticate(context.Background(), testCluster(server), Credentials{
		Username: testUser,
		Password: "wrong-password",
	})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrInvalidCredentials)
	// The error must not disclose which half of the credential was wrong.
	assert.NotContains(t, err.Error(), "wrong-password")
}

func TestAuthenticateRejectsInactiveCluster(t *testing.T) {
	_, server := newFakeOCP(t, true)
	auth := newTestAuthenticator(server)

	cluster := testCluster(server)
	cluster.Active = false

	_, err := auth.Authenticate(context.Background(), cluster, Credentials{
		Username: testUser,
		Password: testPass,
	})
	require.Error(t, err)

	var inactive *ErrClusterInactive
	require.ErrorAs(t, err, &inactive)
	assert.Equal(t, "prod-cluster-1", inactive.Name)
}

func TestAuthenticateRejectsEmptyCredentials(t *testing.T) {
	_, server := newFakeOCP(t, true)
	auth := newTestAuthenticator(server)

	_, err := auth.Authenticate(context.Background(), testCluster(server), Credentials{Username: testUser})
	assert.ErrorContains(t, err, "password is required")

	_, err = auth.Authenticate(context.Background(), testCluster(server), Credentials{Password: testPass})
	assert.ErrorContains(t, err, "username is required")
}

func TestAuthenticateRespectsContextCancellation(t *testing.T) {
	_, server := newFakeOCP(t, true)
	auth := newTestAuthenticator(server)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := auth.Authenticate(ctx, testCluster(server), Credentials{Username: testUser, Password: testPass})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "got %v", err)
}

func TestParseTokenFromLocation(t *testing.T) {
	now := time.Date(2026, 5, 2, 14, 32, 1, 0, time.UTC)

	t.Run("fragment with expiry", func(t *testing.T) {
		result, err := parseTokenFromLocation(
			"https://oauth.example.com/implicit#access_token=abc123&expires_in=3600&token_type=Bearer", now)
		require.NoError(t, err)
		assert.Equal(t, "abc123", result.Token)
		assert.Equal(t, now.Add(time.Hour), result.ExpiresAt)
	})

	t.Run("no expiry reported", func(t *testing.T) {
		result, err := parseTokenFromLocation("https://oauth.example.com/implicit#access_token=abc123", now)
		require.NoError(t, err)
		assert.True(t, result.ExpiresAt.IsZero())
		assert.False(t, result.IsExpired(now.Add(100*time.Hour)))
	})

	t.Run("access_denied maps to invalid credentials", func(t *testing.T) {
		_, err := parseTokenFromLocation("https://oauth.example.com/implicit#error=access_denied", now)
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("other oauth error surfaces description", func(t *testing.T) {
		_, err := parseTokenFromLocation(
			"https://oauth.example.com/implicit#error=server_error&error_description=backend+unavailable", now)
		assert.ErrorContains(t, err, "backend unavailable")
	})

	t.Run("missing token", func(t *testing.T) {
		_, err := parseTokenFromLocation("https://oauth.example.com/implicit#token_type=Bearer", now)
		assert.ErrorContains(t, err, "no access_token")
	})

	t.Run("unparseable expiry", func(t *testing.T) {
		_, err := parseTokenFromLocation("https://oauth.example.com/implicit#access_token=a&expires_in=soon", now)
		assert.ErrorContains(t, err, "expires_in")
	})
}

func TestCredentialsNeverFormatPassword(t *testing.T) {
	creds := Credentials{Username: testUser, Password: "hunter2"}

	for _, formatted := range []string{
		fmt.Sprintf("%v", creds),
		fmt.Sprintf("%s", creds),
		fmt.Sprintf("%#v", creds),
		fmt.Sprint(creds),
	} {
		assert.NotContains(t, formatted, "hunter2")
		assert.Contains(t, formatted, "REDACTED")
	}
}

func TestAuthResultNeverFormatsToken(t *testing.T) {
	result := AuthResult{Token: testToken, Username: testUser, ExpiresAt: time.Now()}

	for _, formatted := range []string{
		fmt.Sprintf("%v", result),
		fmt.Sprintf("%#v", result),
	} {
		assert.NotContains(t, formatted, testToken)
		assert.Contains(t, formatted, "REDACTED")
	}
}

func TestAuthResultIsExpired(t *testing.T) {
	now := time.Now()

	assert.False(t, AuthResult{ExpiresAt: now.Add(time.Minute)}.IsExpired(now))
	assert.True(t, AuthResult{ExpiresAt: now.Add(-time.Minute)}.IsExpired(now))
	assert.True(t, AuthResult{ExpiresAt: now}.IsExpired(now), "expiry boundary counts as expired")
	assert.False(t, AuthResult{}.IsExpired(now), "zero expiry means the cluster decides")
}
