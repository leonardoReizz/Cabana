package deploy

import (
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/leonardoreis/cabana/internal/domain"
)

const (
	// appPort is the port cabana expects a user's container to listen on. It is
	// also injected as $PORT, so an app can read it instead of hardcoding.
	appPort = 8080

	nameLabel      = "app.kubernetes.io/name"
	managedByLabel = "app.kubernetes.io/managed-by"
	appIDLabel     = "cabana.dev/app-id"
	deployIDLabel  = "cabana.dev/deploy-id"
	// componentLabel separates a build pod from an app pod. Without it the two
	// are indistinguishable by label, which is how build output ended up
	// labelled as application logs.
	componentLabel = "cabana.dev/component"

	resourceName = "app"
	envSecret    = "app-env"
)

func appLabels(app domain.App) map[string]string {
	return map[string]string{
		nameLabel:      app.Name,
		managedByLabel: "cabana",
		appIDLabel:     app.ID,
	}
}

// namespaceFor gives every app its own namespace, which makes Destroy a single
// namespace deletion instead of a list of resources to keep in sync.
func (d *K3s) namespaceFor(app domain.App) string {
	return "cabana-app-" + app.Name
}

// imageRef is the registry-qualified image for one deploy. Tagging by deploy ID
// rather than a moving tag is what makes rollback (v0.3) possible later — every
// build stays addressable.
func (d *K3s) ImageRef(app domain.App, deployID string) string {
	return d.imageRef(app, deployID)
}

func (d *K3s) imageRef(app domain.App, deployID string) string {
	return fmt.Sprintf("%s/%s:%s", d.registry, app.Name, deployID)
}

func namespaceObject(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
	}
}

func envSecretObject(app domain.App, env map[string]string) *corev1.Secret {
	data := make(map[string]string, len(env)+1)
	for k, v := range env {
		data[k] = v
	}
	// Injected last so a user-supplied PORT does not silently disagree with the
	// port the Service actually targets.
	data["PORT"] = fmt.Sprint(appPort)

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: envSecret, Labels: appLabels(app)},
		StringData: data,
	}
}

// buildJobObject runs rootless BuildKit as a one-shot Job. BuildKit fetches the
// git context itself, so cabana never clones a repository or handles a working
// copy — the build is entirely the cluster's problem.
func (d *K3s) buildJobObject(app domain.App, deployID string, buildArgs map[string]string) *batchv1.Job {
	// Deliberately NOT appLabels(app): those are the Deployment's selector, and
	// a build pod carrying them overlaps with it. Two things went wrong when it
	// did — the app's log follower adopted the build pod and republished the
	// whole build as application output, and the Deployment's selector matched
	// pods it does not own. The build pod is found by its deploy ID instead.
	labels := map[string]string{
		managedByLabel: "cabana",
		componentLabel: "build",
		deployIDLabel:  deployID,
	}

	gitContext := gitContextURL(app.RepoURL, app.GitRef)
	image := d.imageRef(app, deployID)

	// registry.insecure=true because the in-cluster registry speaks plain HTTP
	// on a cluster-internal address that never leaves the node.
	output := fmt.Sprintf(
		"type=image,name=%s,push=true,registry.insecure=true", image)

	command := []string{
		"buildctl-daemonless.sh", "build",
		"--frontend", "dockerfile.v0",
		"--opt", "context=" + gitContext,
		"--output", output,
		"--progress", "plain",
	}
	// Sorted so the same configuration always produces the same command, which
	// keeps job specs diffable and builds reproducible.
	for _, k := range sortedKeys(buildArgs) {
		command = append(command, "--opt",
			fmt.Sprintf("build-arg:%s=%s", k, buildArgs[k]))
	}

	unconfined := corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}
	unconfinedAppArmor := corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined}
	runAsUser := int64(1000)
	backoffLimit := int32(0) // A failed build is reported, never silently retried.
	ttl := int32(3600)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "build-" + deployID,
			Labels: labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			// Jobs clean themselves up an hour after finishing, so a long-lived
			// install does not accumulate thousands of completed build pods.
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:       &runAsUser,
						RunAsGroup:      &runAsUser,
						SeccompProfile:  &unconfined,
						AppArmorProfile: &unconfinedAppArmor,
					},
					Containers: []corev1.Container{{
						Name:    "build",
						Image:   d.buildkitImage,
						Command: command,
						Env: []corev1.EnvVar{{
							Name: "BUILDKITD_FLAGS",
							// Rootless BuildKit cannot create its own process
							// sandbox inside an unprivileged pod.
							Value: "--oci-worker-no-process-sandbox",
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
						},
					}},
				},
			},
		},
	}
}

func (d *K3s) deploymentObject(app domain.App, deployID, image string) *appsv1.Deployment {
	labels := appLabels(app)
	port := int32(appPort)

	replicas := app.Replicas
	if replicas < 1 {
		replicas = 1
	}

	podLabels := map[string]string{}
	for k, v := range labels {
		podLabels[k] = v
	}
	// Stamping the deploy ID on the pod template is what makes a redeploy of an
	// unchanged image still roll the pods.
	podLabels[deployIDLabel] = deployID
	podLabels[componentLabel] = "app"

	delay := app.Health.InitialDelaySeconds
	if delay < 0 {
		delay = 0
	}

	tcpHandler := corev1.ProbeHandler{
		TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)},
	}

	// Readiness runs the check the user configured. Failing it only withholds
	// traffic, so a wrong path costs a failed rollout — and the previous version
	// keeps serving.
	readinessHandler := tcpHandler
	if app.Health.Type == domain.HealthHTTP {
		path := app.Health.Path
		if path == "" {
			path = "/"
		}
		readinessHandler = corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(port)},
		}
	}

	// Liveness deliberately stays TCP, whatever the user configured. Liveness
	// kills containers, so pointing it at a user-supplied path turns one typo
	// into an endless restart loop that no amount of waiting recovers from —
	// observed exactly once, on an app that served 200 everywhere except "/".
	// A wedged process still stops accepting connections, which this catches.
	livenessDelay := delay + 20

	period := app.Health.PeriodSeconds
	if period < 1 {
		period = 10
	}
	timeout := app.Health.ProbeTimeout()

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
				nameLabel:  app.Name,
				appIDLabel: app.ID,
			}},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				// maxUnavailable=0 is what actually makes a deploy zero-downtime
				// at one replica: without it Kubernetes may take the only pod
				// down before its replacement is ready. Surge adds the new pod
				// first and only then retires the old one.
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: ptr(intstr.FromInt32(0)),
					MaxSurge:       ptr(intstr.FromString("100%")),
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:            resourceName,
						Image:           image,
						ImagePullPolicy: corev1.PullAlways,
						Ports: []corev1.ContainerPort{{
							Name:          "http",
							ContainerPort: port,
						}},
						EnvFrom: []corev1.EnvFromSource{{
							SecretRef: &corev1.SecretEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: envSecret,
								},
							},
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler:        readinessHandler,
							InitialDelaySeconds: delay,
							PeriodSeconds:       period,
							TimeoutSeconds:      timeout,
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler:        tcpHandler,
							InitialDelaySeconds: livenessDelay,
							PeriodSeconds:       period,
							TimeoutSeconds:      timeout,
						},
					}},
				},
			},
		},
	}
}

func serviceObject(app domain.App) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Labels: appLabels(app)},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				nameLabel:  app.Name,
				appIDLabel: app.ID,
			},
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       80,
				TargetPort: intstr.FromInt32(appPort),
			}},
		},
	}
}

// ingressObject routes the hostname to the app, and requests a certificate when
// an issuer is configured.
//
// cert-manager works by watching the annotation: it sees the tls block, issues
// a certificate for those hosts, and writes it into the named Secret. Traefik
// then picks the Secret up. Nothing here talks to cert-manager directly.
func ingressObject(app domain.App, hostname, issuer string) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix

	annotations := map[string]string{}
	var tls []networkingv1.IngressTLS
	if issuer != "" {
		annotations["cert-manager.io/cluster-issuer"] = issuer
		// Redirect plain HTTP once a certificate exists. Traefik's own
		// redirect middleware would also catch the ACME challenge, which is
		// served over HTTP — cert-manager's solver ingress carries a higher
		// priority so the challenge still gets through.
		annotations["traefik.ingress.kubernetes.io/router.entrypoints"] = "web,websecure"
		tls = []networkingv1.IngressTLS{{
			Hosts:      []string{hostname},
			SecretName: certSecretName(app),
		}}
	}

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        resourceName,
			Labels:      appLabels(app),
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{
			TLS: tls,
			Rules: []networkingv1.IngressRule{{
				Host: hostname,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: resourceName,
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
}

// gitContextURL renders a repo URL and ref as a BuildKit git context.
//
// The .git suffix is not cosmetic: BuildKit decides whether an https:// context
// is a git repository or a plain HTTP download by looking at the URL, and
// without the suffix it fetches the forge's HTML landing page and tries to
// parse it as a Dockerfile. Users paste the browser URL, so cabana normalises.
func gitContextURL(repoURL, ref string) string {
	url := strings.TrimSuffix(strings.TrimSpace(repoURL), "/")
	if !strings.HasSuffix(url, ".git") {
		url += ".git"
	}
	if ref == "" {
		return url
	}
	return url + "#" + ref
}

// certSecretName is where cert-manager writes the issued certificate. Derived
// rather than random so a redeploy reuses the existing certificate instead of
// asking Let's Encrypt for another one and burning the rate limit.
func certSecretName(app domain.App) string {
	return app.Name + "-tls"
}

func ptr[T any](v T) *T { return &v }

// sortedKeys keeps env rendering deterministic, which matters for readable
// diffs and for tests that assert on generated objects.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
