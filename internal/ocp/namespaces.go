// Package ocp holds direct queries against a cluster's Kubernetes API that
// are neither authentication nor session lifecycle — currently just the
// namespace lookup backing the TUI's namespace selector.
package ocp

import (
	"context"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/oziie/ocpgate/internal/registry"
)

// ErrNamespacesForbidden means the token is valid but not allowed to list
// namespaces cluster-wide. This is the common case for an ordinary LDAP
// user on OCP, where namespace listing is a cluster-scoped read most users
// do not have — so callers should treat it as "ask the user to type one"
// rather than as a failure.
var ErrNamespacesForbidden = fmt.Errorf("not allowed to list namespaces on this cluster")

// ListNamespaces returns the namespaces visible to the given token, sorted
// by name.
func ListNamespaces(ctx context.Context, cluster registry.ClusterEntry, token string, insecureSkipTLSVerify bool) ([]string, error) {
	client, err := kubernetes.NewForConfig(&rest.Config{
		Host:        cluster.APIEndpoint,
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: insecureSkipTLSVerify,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("build cluster client: %w", err)
	}

	list, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			return nil, ErrNamespacesForbidden
		}
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	names := make([]string, 0, len(list.Items))
	for _, ns := range list.Items {
		names = append(names, ns.Name)
	}
	sort.Strings(names)

	return names, nil
}
