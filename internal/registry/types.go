package registry

import "fmt"

// Environment values allowed for ClusterEntry.Environment.
const (
	EnvProduction = "production"
	EnvTest       = "test"
)

// ClusterEntry describes a single OCP cluster as published in the
// cluster-registry GitLab repo.
type ClusterEntry struct {
	Name        string `yaml:"name" json:"name"`
	APIEndpoint string `yaml:"api_endpoint" json:"api_endpoint"`
	Environment string `yaml:"environment" json:"environment"`
	Region      string `yaml:"region" json:"region"`
	LDAPRealm   string `yaml:"ldap_realm" json:"ldap_realm"`
	Active      bool   `yaml:"active" json:"active"`
}

// Validate enforces the cluster-registry YAML schema rules documented in
// CLAUDE.md: environment must be production|test, api_endpoint must be a
// well-formed https URL, and name must be non-empty (uniqueness is
// enforced across the whole registry, not per-entry).
func (c ClusterEntry) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("cluster entry missing required field: name")
	}
	if c.APIEndpoint == "" {
		return fmt.Errorf("cluster %q missing required field: api_endpoint", c.Name)
	}
	if err := validateAPIEndpoint(c.APIEndpoint); err != nil {
		return fmt.Errorf("cluster %q: %w", c.Name, err)
	}
	switch c.Environment {
	case EnvProduction, EnvTest:
	default:
		return fmt.Errorf("cluster %q: environment must be %q or %q, got %q", c.Name, EnvProduction, EnvTest, c.Environment)
	}
	return nil
}
