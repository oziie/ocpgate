package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClusterEntryValidate(t *testing.T) {
	cases := []struct {
		name    string
		entry   ClusterEntry
		wantErr bool
	}{
		{
			name: "valid",
			entry: ClusterEntry{
				Name:        "prod-cluster-1",
				APIEndpoint: "https://api.prod-cluster-1.example.com:6443",
				Environment: EnvProduction,
			},
			wantErr: false,
		},
		{
			name:    "missing name",
			entry:   ClusterEntry{APIEndpoint: "https://x:6443", Environment: EnvTest},
			wantErr: true,
		},
		{
			name:    "bad scheme",
			entry:   ClusterEntry{Name: "a", APIEndpoint: "http://x:6443", Environment: EnvTest},
			wantErr: true,
		},
		{
			name:    "missing port",
			entry:   ClusterEntry{Name: "a", APIEndpoint: "https://x", Environment: EnvTest},
			wantErr: true,
		},
		{
			name:    "bad environment",
			entry:   ClusterEntry{Name: "a", APIEndpoint: "https://x:6443", Environment: "staging"},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.entry.Validate()
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateUnique(t *testing.T) {
	unique := []ClusterEntry{{Name: "a"}, {Name: "b"}}
	assert.NoError(t, validateUnique(unique))

	dup := []ClusterEntry{{Name: "a"}, {Name: "A"}}
	assert.Error(t, validateUnique(dup))
}

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(filepath.Join(dir, "clusters.json"))

	entries, syncedAt, err := c.Load()
	require.NoError(t, err)
	assert.Nil(t, entries)
	assert.True(t, syncedAt.IsZero())

	want := []ClusterEntry{
		{Name: "prod-cluster-1", APIEndpoint: "https://api.prod:6443", Environment: EnvProduction, Active: true},
	}
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, c.Save(want, now))

	got, gotSyncedAt, err := c.Load()
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, now, gotSyncedAt)

	// Ensure temp file was cleaned up by the rename.
	_, err = os.Stat(c.Path + ".tmp")
	assert.True(t, os.IsNotExist(err))
}
