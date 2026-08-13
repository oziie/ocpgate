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
	"github.com/oziie/ocpgate/internal/retry"
)

// ErrNamespacesForbidden means the token is valid but not allowed to list
// namespaces cluster-wide. This is the common case for an ordinary LDAP
// user on OCP, where namespace listing is a cluster-scoped read most users
// do not have — so callers should treat it as "ask the user to type one"
// rather than as a failure.
var ErrNamespacesForbidden = fmt.Errorf("not allowed to list namespaces on this cluster")

// ListNamespaces returns the namespaces visible to the given token, sorted
// by name. Transient API failures are retried; a refusal is not, since the
// token's permissions will not change between attempts.
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

	var names []string
	err = retry.Do(ctx, retry.DefaultPolicy(), func(ctx context.Context) error {
		list, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return classifyAPIError(err)
		}

		names = make([]string, 0, len(list.Items))
		for _, ns := range list.Items {
			names = append(names, ns.Name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(names)
	return names, nil
}

// classifyAPIError separates "the cluster is briefly unwell" from "this
// token is not allowed to do that".
func classifyAPIError(err error) error {
	switch {
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return retry.Permanent(ErrNamespacesForbidden)
	case apierrors.IsServerTimeout(err), apierrors.IsTimeout(err),
		apierrors.IsTooManyRequests(err), apierrors.IsInternalError(err),
		apierrors.IsServiceUnavailable(err):
		return fmt.Errorf("list namespaces: %w", err)
	case apierrors.IsNotFound(err), apierrors.IsBadRequest(err), apierrors.IsMethodNotSupported(err):
		return retry.Permanent(fmt.Errorf("list namespaces: %w", err))
	default:
		// Network-level failures land here and are worth another try.
		return fmt.Errorf("list namespaces: %w", err)
	}
}
