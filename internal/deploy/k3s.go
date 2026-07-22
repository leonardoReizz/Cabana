package deploy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/leonardoreis/cabana/internal/domain"
	"github.com/leonardoreis/cabana/internal/logstream"
)

// logTailLines caps how much history a newly attached pod replays. Enough to
// show why a container is unhappy, not so much that opening an app floods the
// panel with hours of output.
const logTailLines = 100

// K3s builds and runs apps inside the cluster. Nothing is built on the host:
// a BuildKit Job fetches the git context, builds the Dockerfile and pushes to
// the in-cluster registry, then a Deployment pulls that image back out.
type K3s struct {
	client        kubernetes.Interface
	hub           *logstream.Hub
	registry      string
	buildkitImage string
	nodeIP        string
	tlsIssuer     string
	recorder      BuildLogRecorder

	buildTimeout   time.Duration
	rolloutTimeout time.Duration

	// One follower per app, shared across all its subscribers.
	followMu  sync.Mutex
	followers map[string]*follower

	// How far each pod's log has already been published, keyed by
	// "<appID>/<podName>". Held here rather than inside the follower so it
	// survives a follower stopping and restarting — otherwise every reconnect
	// re-reads the pod's recent history and republishes lines the hub's replay
	// buffer already holds, which the user sees as duplicated logs.
	cursorMu sync.Mutex
	cursors  map[string]time.Time
}

type follower struct {
	refs   int
	cancel context.CancelFunc
}

type K3sOptions struct {
	Registry      string
	BuildkitImage string
	NodeIP        string
	// TLSIssuer is the cert-manager ClusterIssuer name, or empty for plain HTTP.
	TLSIssuer string
	// Recorder may be nil, in which case build output is streamed but not kept.
	Recorder BuildLogRecorder
}

func NewK3s(client kubernetes.Interface, hub *logstream.Hub, opts K3sOptions) *K3s {
	return &K3s{
		client:         client,
		hub:            hub,
		registry:       opts.Registry,
		buildkitImage:  opts.BuildkitImage,
		nodeIP:         opts.NodeIP,
		tlsIssuer:      opts.TLSIssuer,
		recorder:       opts.Recorder,
		buildTimeout:   20 * time.Minute,
		rolloutTimeout: 5 * time.Minute,
		followers:      make(map[string]*follower),
		cursors:        make(map[string]time.Time),
	}
}

func (d *K3s) cursorFor(appID, pod string) (time.Time, bool) {
	d.cursorMu.Lock()
	defer d.cursorMu.Unlock()
	at, ok := d.cursors[appID+"/"+pod]
	return at, ok
}

func (d *K3s) setCursor(appID, pod string, at time.Time) {
	d.cursorMu.Lock()
	defer d.cursorMu.Unlock()
	d.cursors[appID+"/"+pod] = at
}

func (d *K3s) dropCursors(appID string) {
	d.cursorMu.Lock()
	defer d.cursorMu.Unlock()
	for key := range d.cursors {
		if strings.HasPrefix(key, appID+"/") {
			delete(d.cursors, key)
		}
	}
}

// publishTo writes into a hub topic by key. Apps and services are both just
// keys, which is what lets one log pipeline serve both.
func (d *K3s) publishTo(key, source, format string, args ...any) {
	d.hub.Publish(key, logstream.Line{
		Timestamp: time.Now(),
		Source:    source,
		Text:      fmt.Sprintf(format, args...),
	})
}

func (d *K3s) publish(app domain.App, source, format string, args ...any) {
	d.publishTo(app.ID, source, format, args...)
}

func (d *K3s) Deploy(ctx context.Context, req Request) error {
	app, deployID := req.App, req.Deploy.ID
	ns := d.namespaceFor(app)

	d.publish(app, "cabana", "deploy %s starting", deployID)

	if err := d.ensureNamespace(ctx, app, ns); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}
	if err := d.applyEnvSecret(ctx, app, ns, req.Env); err != nil {
		return fmt.Errorf("apply env: %w", err)
	}

	// A rollback reuses an image that already exists in the registry, so there
	// is nothing to build — which is what makes it near-instant compared with a
	// deploy, and what makes it safe when the repository has moved on.
	image := req.Deploy.Image
	if req.RollbackFrom != "" {
		d.publish(app, "cabana", "rolling back to %s", image)
	} else {
		image = d.imageRef(app, deployID)
		d.publish(app, "cabana", "building %s#%s", app.RepoURL, app.GitRef)
		if err := d.runBuild(ctx, app, ns, deployID, req.BuildArgs); err != nil {
			return fmt.Errorf("build: %w", err)
		}
	}

	d.publish(app, "cabana", "rolling out %s (%d replica(s))", image, app.Replicas)
	if err := d.applyWorkload(ctx, app, ns, deployID, image); err != nil {
		return fmt.Errorf("apply workload: %w", err)
	}
	if err := d.waitForRollout(ctx, app, ns); err != nil {
		return fmt.Errorf("rollout: %w", err)
	}

	d.publish(app, "cabana", "deploy %s succeeded — %s", deployID, d.AppURL(app))
	return nil
}

// ensureNamespace tolerates a namespace still terminating from a previous
// delete. Deleting an app and recreating it under the same name is an obvious
// thing to do, and Kubernetes rejects every write into a terminating namespace
// until the teardown finishes.
func (d *K3s) ensureNamespace(ctx context.Context, app domain.App, ns string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	namespaces := d.client.CoreV1().Namespaces()
	object := namespaceObject(ns, appLabels(app))

	warned := false
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		_, err := namespaces.Create(ctx, object, metav1.CreateOptions{})
		if err == nil {
			return nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return err
		}

		existing, err := namespaces.Get(ctx, ns, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue // Finished terminating between the two calls; retry the create.
		}
		if err != nil {
			return err
		}
		if existing.Status.Phase != corev1.NamespaceTerminating {
			return nil
		}

		if !warned {
			d.publish(app, "cabana", "waiting for the previous %s to finish terminating", ns)
			warned = true
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("namespace %s stuck terminating", ns)
		case <-ticker.C:
		}
	}
}

func (d *K3s) applyEnvSecret(ctx context.Context, app domain.App, ns string, env map[string]string) error {
	secret := envSecretObject(app, env)
	secrets := d.client.CoreV1().Secrets(ns)

	_, err := secrets.Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	}
	if err != nil {
		return err
	}

	if len(env) > 0 {
		// Values are secrets; only the key names are safe to log.
		d.publish(app, "cabana", "env: %s", strings.Join(sortedKeys(env), ", "))
	}
	return nil
}

// runBuild creates the BuildKit Job, streams its output into the app's log
// topic, and reports failure with enough context to act on.
func (d *K3s) runBuild(ctx context.Context, app domain.App, ns, deployID string, buildArgs map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, d.buildTimeout)
	defer cancel()

	if len(buildArgs) > 0 {
		// Names only. A build arg is baked into the image and readable from its
		// history, so values are not secret — but they are still not the log's
		// business.
		d.publish(app, "cabana", "build args: %s", strings.Join(sortedKeys(buildArgs), ", "))
	}

	job := d.buildJobObject(app, deployID, buildArgs)
	jobs := d.client.BatchV1().Jobs(ns)

	if _, err := jobs.Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create build job: %w", err)
	}

	pod, err := d.waitForBuildPod(ctx, ns, deployID)
	if err != nil {
		return err
	}

	// follow=true is load-bearing: a non-following read of a container that has
	// only just started returns whatever exists at that instant, which is
	// nothing. Following blocks until the build container exits, which is
	// exactly the window the build output lands in.
	//
	// Streaming itself is best-effort — a build that succeeds with unreadable
	// logs is still a successful build, so only the Job's outcome decides.
	//
	// Every line is captured as it goes past so the whole build can be stored
	// afterwards, whether it succeeds or fails. A failed build is precisely the
	// one whose log someone will want to read later.
	var captured strings.Builder
	err = d.streamPodLogs(ctx, app.ID, ns, pod, "build",
		&corev1.PodLogOptions{Follow: true}, func(line string) {
			captured.WriteString(line)
			captured.WriteByte('\n')
		})
	if err != nil {
		slog.Debug("build log stream ended early", "app", app.Name, "error", err)
		d.publish(app, "cabana", "build log stream interrupted: %v", err)
	}
	d.recordBuildLog(ctx, deployID, captured.String())

	return d.waitForJob(ctx, ns, job.Name)
}

// recordBuildLog persists build output without letting a storage failure fail
// an otherwise good deploy.
func (d *K3s) recordBuildLog(ctx context.Context, deployID, content string) {
	if d.recorder == nil || content == "" {
		return
	}
	if err := d.recorder.SaveDeployLog(ctx, deployID, content); err != nil {
		slog.Error("persist build log", "deploy", deployID, "error", err)
	}
}

// waitForBuildPod blocks until the Job's pod exists and has left Pending, since
// logs cannot be read before a container starts.
func (d *K3s) waitForBuildPod(ctx context.Context, ns, deployID string) (string, error) {
	// Both the build pod and the app pod of a deploy carry its deploy ID, so
	// the component is what disambiguates them.
	selector := deployIDLabel + "=" + deployID + "," + componentLabel + "=build"
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		pods, err := d.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
			LabelSelector: selector,
		})
		if err != nil {
			return "", fmt.Errorf("list build pods: %w", err)
		}
		for _, pod := range pods.Items {
			switch pod.Status.Phase {
			case corev1.PodRunning, corev1.PodSucceeded, corev1.PodFailed:
				return pod.Name, nil
			}
			// A pod stuck Pending on an unschedulable node or a missing image
			// would otherwise hang here until the build timeout.
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodScheduled &&
					cond.Status == corev1.ConditionFalse &&
					cond.Reason == corev1.PodReasonUnschedulable {
					return "", fmt.Errorf("build pod unschedulable: %s", cond.Message)
				}
			}
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timed out waiting for build pod: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (d *K3s) waitForJob(ctx context.Context, ns, name string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		job, err := d.client.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get build job: %w", err)
		}
		for _, cond := range job.Status.Conditions {
			if cond.Status != corev1.ConditionTrue {
				continue
			}
			switch cond.Type {
			case batchv1.JobComplete:
				return nil
			case batchv1.JobFailed:
				return fmt.Errorf("build failed: %s", firstNonEmpty(cond.Message, cond.Reason))
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("build timed out after %s", d.buildTimeout)
		case <-ticker.C:
		}
	}
}

func (d *K3s) applyWorkload(ctx context.Context, app domain.App, ns, deployID, image string) error {
	deployments := d.client.AppsV1().Deployments(ns)
	deployment := d.deploymentObject(app, deployID, image)
	if _, err := deployments.Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		if _, err := deployments.Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}

	services := d.client.CoreV1().Services(ns)
	service := serviceObject(app)
	if _, err := services.Create(ctx, service, metav1.CreateOptions{}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		// A Service's ClusterIP is immutable, so an existing one is left alone
		// rather than updated — its spec never changes between deploys.
		return err
	}

	hostname := d.Hostname(app)
	if hostname == "" {
		d.publish(app, "cabana", "no hostname available — skipping ingress")
		return nil
	}

	ingresses := d.client.NetworkingV1().Ingresses(ns)
	ingress := ingressObject(app, hostname, d.tlsIssuer)
	if d.tlsIssuer != "" {
		d.publish(app, "cabana",
			"requesting a certificate for %s from %s", hostname, d.tlsIssuer)
	}
	if _, err := ingresses.Create(ctx, ingress, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		if _, err := ingresses.Update(ctx, ingress, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	return nil
}

// waitForRollout returns once the Deployment reports its new replicas available,
// so "running" in the dashboard means the app actually answered a probe.
func (d *K3s) waitForRollout(ctx context.Context, app domain.App, ns string) error {
	ctx, cancel := context.WithTimeout(ctx, d.rolloutTimeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastReason string
	for {
		dep, err := d.client.AppsV1().Deployments(ns).Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get deployment: %w", err)
		}

		if dep.Status.ObservedGeneration >= dep.Generation &&
			dep.Status.UpdatedReplicas == *dep.Spec.Replicas &&
			dep.Status.AvailableReplicas == *dep.Spec.Replicas &&
			// Guards against declaring success while old replicas are still
			// being retired, which at >1 replica would report a rollout as
			// finished mid-flight.
			dep.Status.Replicas == *dep.Spec.Replicas {
			return nil
		}

		// Surface the reason a rollout is stuck (ImagePullBackOff,
		// CrashLoopBackOff) instead of making the user wait out the timeout
		// wondering what happened.
		if reason := d.rolloutBlockerIn(ctx, ns); reason != "" && reason != lastReason {
			d.publish(app, "cabana", "waiting: %s", reason)
			lastReason = reason
		}

		select {
		case <-ctx.Done():
			if lastReason != "" {
				return fmt.Errorf("rollout stalled: %s", lastReason)
			}
			return fmt.Errorf("rollout did not complete within %s", d.rolloutTimeout)
		case <-ticker.C:
		}
	}
}

// rolloutBlockerIn reports why nothing in a namespace is coming up, so a stuck
// rollout says ImagePullBackOff instead of just timing out. Used for both app
// and service namespaces.
func (d *K3s) rolloutBlockerIn(ctx context.Context, ns string) string {
	// Excludes build pods: one waiting on an image pull is not the app's
	// rollout being blocked. Service namespaces contain no build pods, so the
	// same query serves both.
	pods, err := d.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: componentLabel + "!=build",
	})
	if err != nil {
		return ""
	}
	for _, pod := range pods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil && w.Reason != "" &&
				w.Reason != "ContainerCreating" && w.Reason != "PodInitializing" {
				return fmt.Sprintf("%s (%s)", w.Reason, strings.TrimSpace(w.Message))
			}
		}
	}
	return ""
}

func (d *K3s) Destroy(ctx context.Context, app domain.App) error {
	// One namespace per app means teardown is a single call that cannot leave
	// stragglers behind.
	err := d.client.CoreV1().Namespaces().Delete(
		ctx, d.namespaceFor(app), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete namespace: %w", err)
	}
	d.hub.Drop(app.ID)
	d.dropCursors(app.ID)
	return nil
}

// StreamLogs bridges the hub to an SSE subscriber and, in parallel, keeps the
// running app's pod logs flowing into the hub.
func (d *K3s) StreamLogs(ctx context.Context, app domain.App, out chan<- LogLine) error {
	lines, unsubscribe := d.hub.Subscribe(app.ID)
	defer unsubscribe()

	// Exactly one follower per app, shared by every subscriber. Spawning one
	// per subscriber duplicated every line into the shared hub once per open
	// stream — two browser tabs, or React's StrictMode double-mounting the
	// effect in development, and everyone sees the log twice.
	release := d.follow(app.ID, d.namespaceFor(app),
		nameLabel+"="+app.Name+","+appIDLabel+"="+app.ID+","+componentLabel+"!=build")
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

// followAppLogs reattaches to the app's pod whenever it is replaced, so a
// crashlooping or redeployed app does not silently stop producing output.
// follow registers interest in a workload's logs, starting the shared follower
// on the first subscriber. The returned function releases that interest and
// stops the follower once the last subscriber leaves.
//
// The follower deliberately runs on its own context rather than a subscriber's:
// one browser tab closing must not tear down a stream another tab is reading.
//
// key is the hub topic — an app ID or a service ID. The follower does not care
// which, which is what lets one pipeline serve both.
func (d *K3s) follow(key, ns, selector string) func() {
	d.followMu.Lock()
	defer d.followMu.Unlock()

	f, ok := d.followers[key]
	if !ok {
		ctx, cancel := context.WithCancel(context.Background())
		f = &follower{cancel: cancel}
		d.followers[key] = f
		go d.followPods(ctx, key, ns, selector)
	}
	f.refs++

	released := false
	return func() {
		d.followMu.Lock()
		defer d.followMu.Unlock()
		if released {
			return // Guard against a double defer releasing someone else's ref.
		}
		released = true

		f.refs--
		if f.refs <= 0 {
			f.cancel()
			delete(d.followers, key)
		}
	}
}

// followPods streams every running pod matching a selector into the hub,
// reattaching as pods are replaced.
func (d *K3s) followPods(ctx context.Context, key, ns, selector string) {
	var mu sync.Mutex
	streaming := make(map[string]bool) // pods with a live stream right now

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		pods, err := d.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
			LabelSelector: selector,
		})
		if err == nil {
			for _, pod := range pods.Items {
				if pod.Status.Phase != corev1.PodRunning {
					continue
				}
				// A pod on its way out during a rollout still reports Running.
				// Attaching to it replays a backlog identical to the incoming
				// pod's, which reads as duplication for an app whose startup
				// output is the same every time.
				if pod.DeletionTimestamp != nil {
					continue
				}
				name := pod.Name

				mu.Lock()
				if streaming[name] {
					mu.Unlock()
					continue
				}
				streaming[name] = true
				mu.Unlock()

				resumeFrom, seenBefore := d.cursorFor(key, name)

				if !seenBefore {
					d.publishTo(key, "cabana", "attached to pod %s", name)
				}

				// Each pod streams in its own goroutine so that during a rollout
				// the old and new pod are both readable, instead of the new one
				// waiting for the old one to die.
				go func() {
					opts := &corev1.PodLogOptions{Follow: true}
					if seenBefore {
						// Resume where the last stream stopped. Without this a
						// reconnect re-reads the pod's recent history and
						// republishes lines the hub already replayed — the exact
						// shape of "the log looks duplicated".
						opts.SinceTime = &metav1.Time{Time: resumeFrom}
					} else {
						tail := int64(logTailLines)
						opts.TailLines = &tail
					}

					// Stamped before streaming, not after: a line arriving while
					// the stream is live must not fall before the cursor, or the
					// next reconnect would emit it a second time.
					d.setCursor(key, name, time.Now())

					if err := d.streamPodLogs(ctx, key, ns, name, "app", opts, nil); err != nil {
						slog.Debug("pod log stream ended", "pod", name, "error", err)
					}

					d.setCursor(key, name, time.Now())
					mu.Lock()
					streaming[name] = false
					mu.Unlock()
				}()
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// streamPodLogs publishes a pod's output to the hub. sink, when non-nil, sees
// every line as well — it is how the build is captured for storage without
// reading the log a second time.
func (d *K3s) streamPodLogs(
	ctx context.Context,
	key string,
	ns, pod, source string,
	opts *corev1.PodLogOptions,
	sink func(line string),
) error {
	stream, err := d.client.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	// Build output can carry very long lines; the default 64KB limit truncates them.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		d.publishTo(key, source, "%s", line)
		if sink != nil {
			sink(line)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Hostname resolves the address an app is reachable at, falling back to sslip.io
// so an install with no domain configured still gets a working URL.
// AppURL is the address to show a user, with the scheme that actually applies.
func (d *K3s) AppURL(app domain.App) string {
	host := d.Hostname(app)
	if host == "" {
		return ""
	}
	if d.tlsIssuer != "" {
		return "https://" + host
	}
	return "http://" + host
}

func (d *K3s) Hostname(app domain.App) string {
	if app.Hostname != "" {
		return app.Hostname
	}
	if d.nodeIP == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s.sslip.io", app.Name, strings.ReplaceAll(d.nodeIP, ".", "-"))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return "unknown"
}

// Compile-time check that K3s satisfies the interface the API depends on.
var _ Deployer = (*K3s)(nil)

// Instances reports the live state of an app's replicas.
//
// This is the difference between "running" and a health panel: Kubernetes knows
// which replica is crashlooping, which was OOM-killed and how often each has
// restarted, but none of that reaches a user who only sees an app-level badge.
func (d *K3s) Instances(ctx context.Context, app domain.App) ([]domain.Instance, error) {
	pods, err := d.client.CoreV1().Pods(d.namespaceFor(app)).List(ctx, metav1.ListOptions{
		LabelSelector: nameLabel + "=" + app.Name + "," +
			appIDLabel + "=" + app.ID + "," + componentLabel + "!=build",
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// The namespace does not exist until the first deploy.
			return []domain.Instance{}, nil
		}
		return nil, fmt.Errorf("list pods: %w", err)
	}

	instances := make([]domain.Instance, 0, len(pods.Items))
	for _, pod := range pods.Items {
		instances = append(instances, instanceFromPod(pod))
	}

	// Unhealthy first: the reason to open this panel is to find what is broken,
	// and it should not be buried under healthy replicas.
	sort.SliceStable(instances, func(i, j int) bool {
		return instanceSeverity(instances[i].Status) > instanceSeverity(instances[j].Status)
	})
	return instances, nil
}

func instanceFromPod(pod corev1.Pod) domain.Instance {
	inst := domain.Instance{Name: pod.Name, Status: domain.InstancePending}

	if pod.Status.StartTime != nil {
		started := pod.Status.StartTime.Time
		inst.StartedAt = &started
	}

	for _, cs := range pod.Status.ContainerStatuses {
		inst.Restarts += cs.RestartCount
		inst.Ready = inst.Ready || cs.Ready

		switch {
		case cs.State.Waiting != nil:
			inst.Reason = cs.State.Waiting.Reason
			inst.Message = strings.TrimSpace(cs.State.Waiting.Message)
		case cs.State.Terminated != nil:
			inst.Reason = cs.State.Terminated.Reason
			inst.Message = strings.TrimSpace(cs.State.Terminated.Message)
		}

		// The OOM signal lives on the *previous* termination, not the current
		// state: by the time anyone looks, the container has already restarted
		// and looks healthy. Reporting it is the whole point of this panel —
		// "restarted 7 times" without "it ran out of memory" is not actionable.
		if last := cs.LastTerminationState.Terminated; last != nil && last.Reason == "OOMKilled" {
			inst.Status = domain.InstanceOOM
			if inst.Reason == "" {
				inst.Reason = "OOMKilled"
			}
			return inst
		}
	}

	switch {
	case inst.Reason == "CrashLoopBackOff":
		inst.Status = domain.InstanceCrashLoop
	case pod.Status.Phase == corev1.PodFailed:
		inst.Status = domain.InstanceFailed
	case pod.Status.Phase == corev1.PodRunning && inst.Ready:
		inst.Status = domain.InstanceRunning
	case pod.Status.Phase == corev1.PodRunning:
		// Running but not yet passing its readiness probe.
		inst.Status = domain.InstanceStarting
	}
	return inst
}

func instanceSeverity(status domain.InstanceStatus) int {
	switch status {
	case domain.InstanceOOM, domain.InstanceCrashLoop:
		return 3
	case domain.InstanceFailed:
		return 2
	case domain.InstanceStarting, domain.InstancePending:
		return 1
	default:
		return 0
	}
}
