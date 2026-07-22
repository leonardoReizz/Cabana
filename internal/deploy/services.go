package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/leonardoreis/cabana/internal/catalog"
	"github.com/leonardoreis/cabana/internal/domain"
)

const serviceSecretName = "service-secrets"

func serviceNamespace(svc domain.Service) string {
	return "cabana-svc-" + svc.Name
}

func serviceLabels(svc domain.Service) map[string]string {
	return map[string]string{
		nameLabel:                     svc.Name,
		managedByLabel:                "cabana",
		componentLabel:                "service",
		"cabana.dev/service-id":       svc.ID,
		"cabana.dev/service-template": svc.Template,
	}
}

// ServiceHost is the in-cluster address linked apps connect to. It is fully
// qualified because the app lives in a different namespace, where a short name
// would not resolve.
func ServiceHost(svc domain.Service) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, serviceNamespace(svc))
}

// headlessServiceName is the Service that gives a StatefulSet's pods stable
// per-pod DNS. It exists only to back that identity, not for client access.
func headlessServiceName(name string) string { return name + "-headless" }

// ServicePodHost is the stable DNS name of a StatefulSet service's first pod.
// A StatefulSet names pods <sts>-0, <sts>-1, …; with one replica the address is
// fixed, which is exactly what a service that advertises its own location needs.
func ServicePodHost(svc domain.Service) string {
	return fmt.Sprintf("%s-0.%s.%s.svc.cluster.local",
		svc.Name, headlessServiceName(svc.Name), serviceNamespace(svc))
}

// ProvisionService installs a catalogue template as a running workload and
// blocks until it is ready to accept connections.
func (d *K3s) ProvisionService(
	ctx context.Context,
	svc domain.Service,
	tmpl catalog.Template,
	secrets map[string]string,
	deps []ServiceDependency,
) error {
	ns := serviceNamespace(svc)
	labels := serviceLabels(svc)

	image := svc.Image
	if image == "" {
		image = tmpl.Image
	}
	// Narrated into the same hub apps use, so the dashboard can show a service
	// coming up instead of a spinner with nothing behind it.
	d.publishTo(svc.ID, "cabana", "installing %s as %s", image, svc.Name)

	if err := d.ensureServiceNamespace(ctx, ns, labels); err != nil {
		return fmt.Errorf("namespace: %w", err)
	}

	// Logs are followed from here — before dependencies and the wait — so a
	// user watching sees the database come up, then the service connect to it.
	release := d.follow(svc.ID, ns, componentLabel+"=service")
	defer release()

	// Dependencies come first and fully: the main service's connection string
	// for them is only correct once they exist, and waiting here turns "metabase
	// failed" into the more useful "its database failed".
	injectedEnv := map[string]string{}
	for _, dep := range deps {
		env, err := d.provisionDependency(ctx, svc, ns, dep)
		if err != nil {
			d.publishTo(svc.ID, "cabana", "dependency %s failed: %v", dep.Name, err)
			return fmt.Errorf("dependency %s: %w", dep.Name, err)
		}
		for k, v := range env {
			injectedEnv[k] = v
		}
	}

	// Injected connection strings carry passwords, so they live in the service's
	// Secret alongside its own generated credentials.
	secretData := make(map[string]string, len(secrets)+len(injectedEnv))
	for k, v := range secrets {
		secretData[k] = v
	}
	injectedRefs := make(map[string]string, len(injectedEnv))
	for envName, value := range injectedEnv {
		key := "inject-" + strings.ToLower(envName)
		secretData[key] = value
		injectedRefs[envName] = key
	}
	if err := d.applyServiceSecret(ctx, ns, serviceSecretName, labels, secretData); err != nil {
		return fmt.Errorf("secrets: %w", err)
	}

	if tmpl.Persistent() {
		d.publishTo(svc.ID, "cabana", "requesting %s of storage", tmpl.Storage)
	} else {
		d.publishTo(svc.ID, "cabana", "no storage — this service keeps nothing across restarts")
	}
	if err := d.applyServiceWorkload(ctx, ns, svc.Name, serviceSecretName, image, tmpl, labels, injectedRefs); err != nil {
		return fmt.Errorf("workload: %w", err)
	}

	d.publishTo(svc.ID, "cabana", "waiting for it to start")
	if err := d.waitForWorkload(ctx, svc, ns, svc.Name); err != nil {
		d.publishTo(svc.ID, "cabana", "install failed: %v", err)
		return err
	}

	d.publishTo(svc.ID, "cabana", "%s is ready at %s:%d", svc.Name, svc.Host, svc.Port)
	return nil
}

// provisionDependency installs one bundled dependency and returns the env vars
// to set on the depending service, keyed by env name with the connection value.
func (d *K3s) provisionDependency(
	ctx context.Context, main domain.Service, ns string, dep ServiceDependency,
) (map[string]string, error) {
	name := main.Name + "-" + dep.Name
	d.publishTo(main.ID, "cabana", "provisioning dependency %s (%s)", name, dep.Template.Name)

	// Dependency pods carry the main service's id so namespace cleanup and log
	// following both pick them up, plus their own name label for their Service.
	labels := map[string]string{
		nameLabel:                 name,
		managedByLabel:            "cabana",
		componentLabel:            "service",
		"cabana.dev/service-id":   main.ID,
		"cabana.dev/service-role": "dependency",
	}

	depSecret := name + "-secrets"
	if err := d.applyServiceSecret(ctx, ns, depSecret, labels, dep.Secrets); err != nil {
		return nil, fmt.Errorf("secret: %w", err)
	}

	if err := d.applyServiceWorkload(ctx, ns, name, depSecret, dep.Template.Image,
		dep.Template, labels, nil); err != nil {
		return nil, fmt.Errorf("workload: %w", err)
	}

	if err := d.waitForWorkload(ctx, main, ns, name); err != nil {
		return nil, err
	}

	// The connection the depending service uses to reach this dependency.
	host := fmt.Sprintf("%s.%s.svc.cluster.local", name, ns)
	vars := catalog.ConnectionVars{
		Host: host, PodHost: host, Port: dep.Template.Port, Password: dep.Secrets["password"],
	}
	conn, err := dep.Template.RenderConnection(vars)
	if err != nil {
		return nil, fmt.Errorf("render connection: %w", err)
	}

	out := make(map[string]string, len(dep.Inject))
	for _, in := range dep.Inject {
		out[in.Env] = in.InjectValue(vars, conn)
	}
	return out, nil
}

// StreamServiceLogs bridges the hub to an SSE subscriber and keeps the running
// container's output flowing into it.
func (d *K3s) StreamServiceLogs(ctx context.Context, svc domain.Service, out chan<- LogLine) error {
	lines, unsubscribe := d.hub.Subscribe(svc.ID)
	defer unsubscribe()

	release := d.follow(svc.ID, serviceNamespace(svc), componentLabel+"=service")
	defer release()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			select {
			case out <- LogLine(line):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (d *K3s) ensureServiceNamespace(ctx context.Context, ns string, labels map[string]string) error {
	_, err := d.client.CoreV1().Namespaces().Create(
		ctx, namespaceObject(ns, labels), metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (d *K3s) applyServiceSecret(ctx context.Context, ns, name string, labels, secrets map[string]string) error {
	object := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		StringData: secrets,
	}
	secretsAPI := d.client.CoreV1().Secrets(ns)

	_, err := secretsAPI.Create(ctx, object, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// Left as-is on purpose: the running container was initialised with the
		// original credentials, and overwriting them here would produce a
		// service nobody can authenticate against.
		return nil
	}
	return err
}

// serviceSelector identifies one workload's pods. Name plus the owning
// service's id: within a namespace the main service and its dependencies share
// the id but differ by name, so this stays unique per workload.
func serviceSelector(name string, labels map[string]string) map[string]string {
	return map[string]string{
		nameLabel:               name,
		"cabana.dev/service-id": labels["cabana.dev/service-id"],
	}
}

// applyServiceWorkload creates a workload and its Service. injectedRefs, when
// present, are extra env vars sourced from keys in secretName — the connection
// strings of bundled dependencies.
func (d *K3s) applyServiceWorkload(
	ctx context.Context,
	ns, name, secretName, image string,
	tmpl catalog.Template,
	labels, injectedRefs map[string]string,
) error {
	env := make([]corev1.EnvVar, 0, len(tmpl.Env)+len(injectedRefs)+1)

	// A StatefulSet service can reach its own stable address through
	// $(CABANA_POD_HOST) — defined first so a template's own env may reference
	// it, which is how Kafka advertises where clients should reconnect. k8s
	// expands $(VAR) only against env defined earlier in the same container.
	if tmpl.IsStatefulSet() {
		env = append(env, corev1.EnvVar{
			Name: "CABANA_POD_HOST",
			Value: fmt.Sprintf("%s-0.%s.%s.svc.cluster.local",
				name, headlessServiceName(name), ns),
		})
	}

	for _, e := range tmpl.Env {
		if e.FromSecret != "" {
			env = append(env, secretEnv(e.Name, secretName, e.FromSecret))
			continue
		}
		env = append(env, corev1.EnvVar{Name: e.Name, Value: e.Value})
	}
	for envName, key := range injectedRefs {
		env = append(env, secretEnv(envName, secretName, key))
	}

	ports := []corev1.ContainerPort{{Name: "service", ContainerPort: tmpl.Port}}
	servicePorts := []corev1.ServicePort{{
		Name: "service", Port: tmpl.Port, TargetPort: intstr.FromInt32(tmpl.Port),
	}}
	for _, p := range tmpl.ExtraPorts {
		ports = append(ports, corev1.ContainerPort{Name: p.Name, ContainerPort: p.Port})
		servicePorts = append(servicePorts, corev1.ServicePort{
			Name: p.Name, Port: p.Port, TargetPort: intstr.FromInt32(p.Port),
		})
	}

	var volumeMounts []corev1.VolumeMount
	if tmpl.Persistent() {
		volumeMounts = []corev1.VolumeMount{{Name: "data", MountPath: tmpl.MountPath}}
	}

	container := corev1.Container{
		Name:         "service",
		Image:        image,
		Command:      tmpl.Command,
		Env:          env,
		Ports:        ports,
		VolumeMounts: volumeMounts,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		ReadinessProbe: serviceProbe(tmpl),
	}

	selector := serviceSelector(name, labels)
	podSpec := corev1.PodSpec{Containers: []corev1.Container{container}}

	if tmpl.IsStatefulSet() {
		if err := d.applyStatefulSet(ctx, ns, name, tmpl, labels, selector, podSpec); err != nil {
			return err
		}
	} else {
		if err := d.applyDeployment(ctx, ns, name, tmpl, labels, selector, podSpec); err != nil {
			return err
		}
	}

	// ClusterIP only: a database reachable from the internet by default would be
	// a serious footgun. Apps reach it over cluster DNS.
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.ServiceSpec{Selector: selector, Ports: servicePorts},
	}
	if _, err := d.client.CoreV1().Services(ns).Create(ctx, service, metav1.CreateOptions{}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (d *K3s) applyDeployment(
	ctx context.Context, ns, name string, tmpl catalog.Template,
	labels, selector map[string]string, podSpec corev1.PodSpec,
) error {
	replicas := int32(1)

	if tmpl.Persistent() {
		if err := d.applyPVC(ctx, ns, name, tmpl.Storage, labels); err != nil {
			return err
		}
		podSpec.Volumes = []corev1.Volume{{
			Name: "data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: name},
			},
		}}
	}

	// Recreate for anything holding a ReadWriteOnce volume — a second pod could
	// not mount it anyway. Stateless services roll normally.
	strategy := appsv1.RollingUpdateDeploymentStrategyType
	if tmpl.Persistent() {
		strategy = appsv1.RecreateDeploymentStrategyType
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Strategy: appsv1.DeploymentStrategy{Type: strategy},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
	deployments := d.client.AppsV1().Deployments(ns)
	if _, err := deployments.Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		if _, err := deployments.Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func (d *K3s) applyStatefulSet(
	ctx context.Context, ns, name string, tmpl catalog.Template,
	labels, selector map[string]string, podSpec corev1.PodSpec,
) error {
	replicas := int32(1)

	// The headless Service is what gives the pod its stable DNS name — the
	// whole point of running as a StatefulSet. It carries no cluster IP and is
	// not for client traffic; the regular Service handles that.
	headless := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: headlessServiceName(name), Labels: labels},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  selector,
			Ports:     []corev1.ServicePort{{Name: "service", Port: tmpl.Port}},
		},
	}
	if _, err := d.client.CoreV1().Services(ns).Create(ctx, headless, metav1.CreateOptions{}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return err
	}

	var volumeClaims []corev1.PersistentVolumeClaim
	if tmpl.Persistent() {
		size, err := resource.ParseQuantity(tmpl.Storage)
		if err != nil {
			return fmt.Errorf("invalid storage %q: %w", tmpl.Storage, err)
		}
		// The StatefulSet owns the claim through this template, which is what
		// ties a specific volume to a specific pod ordinal across restarts.
		volumeClaims = []corev1.PersistentVolumeClaim{{
			ObjectMeta: metav1.ObjectMeta{Name: "data"},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: size},
				},
			},
		}}
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: headlessServiceName(name),
			Replicas:    &replicas,
			Selector:    &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
			VolumeClaimTemplates: volumeClaims,
		},
	}
	sets := d.client.AppsV1().StatefulSets(ns)
	if _, err := sets.Create(ctx, sts, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		// A StatefulSet's volumeClaimTemplates are immutable, so an update that
		// touched them would be rejected; the pod template is what a redeploy
		// changes, and Update carries it.
		if _, err := sets.Update(ctx, sts, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func (d *K3s) applyPVC(ctx context.Context, ns, name, storage string, labels map[string]string) error {
	size, err := resource.ParseQuantity(storage)
	if err != nil {
		return fmt.Errorf("invalid storage %q: %w", storage, err)
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: size},
			},
		},
	}
	_, err = d.client.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// A PVC's size is the only mutable field and shrinking is illegal, so an
		// existing claim is kept rather than reconciled.
		return nil
	}
	return err
}

func secretEnv(name, secretName, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  key,
			},
		},
	}
}

// serviceProbe prefers whatever the template declares, falling back to a TCP
// check — cabana cannot know a given image's health endpoint.
func serviceProbe(tmpl catalog.Template) *corev1.Probe {
	probe := &corev1.Probe{InitialDelaySeconds: 5, PeriodSeconds: 5, FailureThreshold: 30}

	switch {
	case len(tmpl.Readiness.Exec) > 0:
		probe.Exec = &corev1.ExecAction{Command: tmpl.Readiness.Exec}
	case tmpl.Readiness.HTTPPath != "":
		probe.HTTPGet = &corev1.HTTPGetAction{
			Path: tmpl.Readiness.HTTPPath,
			Port: intstr.FromInt32(tmpl.ReadinessPort()),
		}
	default:
		probe.TCPSocket = &corev1.TCPSocketAction{Port: intstr.FromInt32(tmpl.Port)}
	}
	return probe
}

// waitForWorkload blocks until a specific deployment or statefulset reports a
// ready replica. Named rather than "any workload in the namespace" because a
// service now has more than one — its dependency and itself — and each must be
// waited on distinctly for its own error to be reported.
func (d *K3s) waitForWorkload(ctx context.Context, svc domain.Service, ns, name string) error {
	ctx, cancel := context.WithTimeout(ctx, d.rolloutTimeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastReason string
	for {
		ready, err := d.workloadReady(ctx, ns, name)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}

		if reason := d.rolloutBlockerIn(ctx, ns); reason != "" && reason != lastReason {
			lastReason = reason
			// Surfaced as it happens rather than only in the final error, so a
			// slow pull looks like progress instead of a hang.
			d.publishTo(svc.ID, "cabana", "waiting: %s", reason)
			// A missing image never resolves itself. Waiting out the full
			// timeout to report a typed version would be five minutes of
			// silence for something knowable in seconds.
			if isImagePullFailure(reason) {
				return fmt.Errorf("could not pull the image — check the version exists: %s", reason)
			}
		}

		select {
		case <-ctx.Done():
			if lastReason != "" {
				return fmt.Errorf("%s did not become ready: %s", name, lastReason)
			}
			return fmt.Errorf("%s did not become ready within %s", name, d.rolloutTimeout)
		case <-ticker.C:
		}
	}
}

// workloadReady reports whether the named workload has a ready replica, looking
// at a Deployment first and a StatefulSet if there is no Deployment by that name.
func (d *K3s) workloadReady(ctx context.Context, ns, name string) (bool, error) {
	dep, err := d.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return dep.Status.AvailableReplicas >= 1, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("get deployment %s: %w", name, err)
	}

	sts, err := d.client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("get statefulset %s: %w", name, err)
	}
	return sts.Status.ReadyReplicas >= 1, nil
}

// isImagePullFailure detects the terminal pull states. ErrImagePull is the first
// failure and ImagePullBackOff is the retry state; either means the reference
// does not resolve.
func isImagePullFailure(reason string) bool {
	return strings.HasPrefix(reason, "ErrImagePull") ||
		strings.HasPrefix(reason, "ImagePullBackOff") ||
		strings.HasPrefix(reason, "InvalidImageName")
}

// DestroyService removes everything belonging to a service, including its data.
func (d *K3s) DestroyService(ctx context.Context, svc domain.Service) error {
	err := d.client.CoreV1().Namespaces().Delete(
		ctx, serviceNamespace(svc), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete namespace: %w", err)
	}
	d.hub.Drop(svc.ID)
	d.dropCursors(svc.ID)
	return nil
}
