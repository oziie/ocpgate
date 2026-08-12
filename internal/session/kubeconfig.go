package session

import (
	"fmt"
	"os"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/oziie/ocpgate/internal/auth"
	"github.com/oziie/ocpgate/internal/registry"
)

// kubeconfigName is the file written inside each session directory.
const kubeconfigName = "kubeconfig"

// kubeconfigOptions carries the parts of a kubeconfig that come from how
// ocpgate was invoked rather than from the cluster or the token.
type kubeconfigOptions struct {
	namespace             string
	insecureSkipTLSVerify bool
}

// buildKubeconfig produces a single-context kubeconfig holding only the
// Bearer token for this session. No client certificates, no refresh token,
// nothing that outlives the token's expiry.
func buildKubeconfig(cluster registry.ClusterEntry, result auth.AuthResult, opts kubeconfigOptions) *clientcmdapi.Config {
	// Context, cluster, and user keys are all named after the cluster so
	// `kubectl config current-context` reads as the cluster the engineer
	// picked, rather than an opaque generated id.
	name := cluster.Name

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[name] = &clientcmdapi.Cluster{
		Server:                cluster.APIEndpoint,
		InsecureSkipTLSVerify: opts.insecureSkipTLSVerify,
	}
	cfg.AuthInfos[result.Username] = &clientcmdapi.AuthInfo{
		Token: result.Token,
	}
	cfg.Contexts[name] = &clientcmdapi.Context{
		Cluster:   name,
		AuthInfo:  result.Username,
		Namespace: opts.namespace,
	}
	cfg.CurrentContext = name

	return cfg
}

// writeKubeconfig serializes cfg to path with owner-only permissions. The
// file is created with O_EXCL: a session directory is freshly generated
// per session, so an existing file there means something is wrong and
// should not be silently overwritten.
func writeKubeconfig(path string, cfg *clientcmdapi.Config) error {
	content, err := clientcmd.Write(*cfg)
	if err != nil {
		return fmt.Errorf("serialize kubeconfig: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create kubeconfig %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Write(content); err != nil {
		return fmt.Errorf("write kubeconfig %s: %w", path, err)
	}
	return nil
}
