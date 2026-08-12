// Package auth exchanges LDAP credentials for a short-lived OCP Bearer
// token using the cluster's own OAuth server. ocpgate stores no long-lived
// secrets of its own: identity stays in LDAP, and the token OCP issues is
// the only credential the tool ever holds.
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/oziie/ocpgate/internal/registry"
)

// Authenticator exchanges credentials for a cluster-scoped Bearer token.
type Authenticator interface {
	Authenticate(ctx context.Context, cluster registry.ClusterEntry, creds Credentials) (*AuthResult, error)
}

// ErrInvalidCredentials means the OAuth server rejected the username or
// password. It is returned unwrapped of any detail from the server, so a
// caller cannot accidentally surface which half was wrong.
var ErrInvalidCredentials = errors.New("authentication failed: invalid username or password")

// ErrClusterInactive means the registry marks the cluster `active: false`.
// Such clusters stay visible in the list — engineers need to see that they
// exist — but authentication against them is refused.
type ErrClusterInactive struct {
	Name string
}

func (e *ErrClusterInactive) Error() string {
	return fmt.Sprintf("cluster %q is marked inactive in the registry; authentication is disabled", e.Name)
}
