package views

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/oziie/ocpgate/internal/registry"
	"github.com/oziie/ocpgate/internal/tui/keys"
)

func TestStatusBarShowsDashesBeforeAnythingIsChosen(t *testing.T) {
	bar := StatusBar{}.View(time.Now())

	assert.Contains(t, bar, "user:")
	assert.Contains(t, bar, "cluster:")
	assert.Contains(t, bar, "namespace:")
	assert.Contains(t, bar, "token:")
	assert.Contains(t, bar, "—")
}

func TestStatusBarCountsDownToken(t *testing.T) {
	now := time.Now()

	bar := StatusBar{
		Username:  "john.doe",
		Cluster:   "prod-cluster-1",
		Namespace: "team-a",
		ExpiresAt: now.Add(90 * time.Minute),
	}.View(now)

	assert.Contains(t, bar, "john.doe")
	assert.Contains(t, bar, "prod-cluster-1")
	assert.Contains(t, bar, "team-a")
	assert.Contains(t, bar, "1h30m")

	expired := StatusBar{ExpiresAt: now.Add(-time.Second)}.View(now)
	assert.Contains(t, expired, "expired")
}

func TestEnvironmentBadge(t *testing.T) {
	production := EnvironmentBadge(registry.ClusterEntry{Environment: registry.EnvProduction, Active: true})
	assert.Contains(t, production, "PRODUCTION")

	test := EnvironmentBadge(registry.ClusterEntry{Environment: registry.EnvTest, Active: true})
	assert.Contains(t, test, "TEST")

	inactive := EnvironmentBadge(registry.ClusterEntry{Environment: registry.EnvProduction, Active: false})
	assert.Contains(t, inactive, "INACTIVE")
}

func TestClusterItemFilterMatchesNameEnvironmentAndRegion(t *testing.T) {
	item := ClusterItem{Entry: registry.ClusterEntry{
		Name:        "prod-cluster-1",
		Environment: registry.EnvProduction,
		Region:      "eu-west",
	}}

	filter := item.FilterValue()
	for _, term := range []string{"prod-cluster-1", "production", "eu-west"} {
		assert.Contains(t, filter, term)
	}
}

func TestCredentialsMasksPasswordAndKeepsUsernameOnError(t *testing.T) {
	cluster := registry.ClusterEntry{
		Name:        "prod-cluster-1",
		APIEndpoint: "https://api.prod-1.example.com:6443",
		Environment: registry.EnvProduction,
		Active:      true,
	}

	view := NewCredentials(cluster, "john.doe", keys.Default())
	assert.False(t, view.Submitted(), "password is still empty")

	view.inputs[fieldPassword].SetValue("hunter2")
	assert.True(t, view.Submitted())
	assert.Equal(t, "hunter2", view.Credentials().Password)

	rendered := view.View()
	assert.NotContains(t, rendered, "hunter2")
	assert.Contains(t, rendered, strings.Repeat("•", len("hunter2")))

	// A retry clears the password but keeps the username, since a wrong
	// password is the far more likely cause.
	view.SetError(assert.AnError)
	assert.Equal(t, "john.doe", view.Credentials().Username)
	assert.Empty(t, view.Credentials().Password)
}

func TestNamespaceManualFallbackPrefillsDefault(t *testing.T) {
	view := NewNamespaceManual("default", "cannot list namespaces", keys.Default())

	assert.True(t, view.Manual())
	assert.Equal(t, "default", view.Selected())
	assert.Contains(t, view.View(), "cannot list namespaces")
}

func TestNamespaceListSelectsDefault(t *testing.T) {
	view := NewNamespaceList([]string{"default", "team-a", "team-b"}, "team-a", keys.Default())

	assert.False(t, view.Manual())
	assert.Equal(t, "team-a", view.Selected())
}

// The zero value gets resized before it is ever built, because Bubble Tea
// reports the window size at startup.
func TestZeroNamespaceViewToleratesResize(t *testing.T) {
	var view Namespace
	assert.NotPanics(t, func() { view.SetSize(80, 24) })
}
