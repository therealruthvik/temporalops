package activities

import (
	"context"
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Namespace is where the sample app and all canary operations live.
const Namespace = "temporalops"

// containerName is the container patched to roll a new image; it must match the
// container name in deploy/k8s/sample-app.yaml.
const containerName = "app"

// The Kubernetes client is built once and cached. Activities are stateless
// functions, so a package-level lazily-initialised client avoids rebuilding a
// REST config on every activity invocation. It is read-only after init, so the
// sync.Once is the only synchronisation needed.
var (
	clientOnce   sync.Once
	cachedClient kubernetes.Interface
	clientErr    error
)

func k8sClient() (kubernetes.Interface, error) {
	clientOnce.Do(func() {
		// Prefer in-cluster config (when the worker runs as a pod); fall back to
		// the local kubeconfig / current context (the kind cluster) for local
		// development and the demo.
		cfg, err := rest.InClusterConfig()
		if err != nil {
			rules := clientcmd.NewDefaultClientConfigLoadingRules()
			cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
				rules, &clientcmd.ConfigOverrides{},
			).ClientConfig()
		}
		if err != nil {
			clientErr = fmt.Errorf("load kube config: %w", err)
			return
		}
		cachedClient, clientErr = kubernetes.NewForConfig(cfg)
	})
	return cachedClient, clientErr
}

// Deployment naming convention: a service "web" maps to Deployments
// "web-stable" and "web-canary" behind Service "web".
func stableName(service string) string { return service + "-stable" }
func canaryName(service string) string { return service + "-canary" }

// scaleDeployment sets a Deployment's replica count via the scale subresource.
// It is idempotent in the strongest sense: if the desired count already matches
// observed spec it performs no write at all, so a retry or a post-crash re-run
// produces no duplicate side effect — the core of the durability/idempotency
// proof in Stage 7.
func scaleDeployment(ctx context.Context, name string, replicas int32) error {
	c, err := k8sClient()
	if err != nil {
		return err
	}
	scale, err := c.AppsV1().Deployments(Namespace).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get scale %s: %w", name, err)
	}
	if scale.Spec.Replicas == replicas {
		return nil // already at desired state
	}
	scale.Spec.Replicas = replicas
	if _, err := c.AppsV1().Deployments(Namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update scale %s -> %d: %w", name, replicas, err)
	}
	return nil
}

// setImage patches the app container's image. A strategic-merge patch keyed by
// container name is idempotent: applying the same image twice is a no-op write.
func setImage(ctx context.Context, name, image string) error {
	c, err := k8sClient()
	if err != nil {
		return err
	}
	patch := fmt.Sprintf(
		`{"spec":{"template":{"spec":{"containers":[{"name":%q,"image":%q}]}}}}`,
		containerName, image,
	)
	if _, err := c.AppsV1().Deployments(Namespace).Patch(
		ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{},
	); err != nil {
		return fmt.Errorf("patch image %s -> %s: %w", name, image, err)
	}
	return nil
}

// deploymentStatus returns the ready and desired replica counts, used by the
// health check to judge the canary.
func deploymentStatus(ctx context.Context, name string) (ready, desired int32, err error) {
	c, err := k8sClient()
	if err != nil {
		return 0, 0, err
	}
	d, err := c.AppsV1().Deployments(Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("get deployment %s: %w", name, err)
	}
	desired = 1
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	return d.Status.ReadyReplicas, desired, nil
}
