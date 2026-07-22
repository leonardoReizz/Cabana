// Package k8s builds the Kubernetes clientset cabana talks to.
package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Connect prefers the in-cluster ServiceAccount, which is how cabana runs in
// production. Falling back to a kubeconfig is what makes local development
// against k3d possible without deploying cabana into the cluster first.
func Connect(kubeconfigPath string) (*kubernetes.Clientset, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return kubernetes.NewForConfig(cfg)
	}
	if err != rest.ErrNotInCluster {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}

	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("KUBECONFIG")
	}
	if kubeconfigPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("locate kubeconfig: %w", err)
		}
		kubeconfigPath = filepath.Join(home, ".kube", "config")
	}

	cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return kubernetes.NewForConfig(cfg)
}
