package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	celtypes "github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// NewGateWatcher starts a new goroutine to watch a specific gate
func NewGateWatcher(
	ctx context.Context,
	client client.Client,
	dynamic dynamic.Interface,
	discovery discovery.DiscoveryInterface,
	remove func(*GateWatcher),
	pod *corev1.Pod,
	gate corev1.PodSchedulingGate,
	condition *GateCondition,
) *GateWatcher {
	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	watcherCtx, cancel := context.WithCancel(ctx)

	namespace := pod.Namespace

	if condition.Namespace != "" {
		namespace = condition.Namespace
	}

	watcher := &GateWatcher{
		Cancel:          cancel,
		Client:          client,
		Discovery:       discovery,
		Dynamic:         dynamic,
		condition:       condition,
		ctx:             watcherCtx,
		gateName:        gate.Name,
		logger:          log.FromContext(watcherCtx),
		namespace:       namespace,
		podKey:          podKey,
		podNamespace:    pod.Namespace,
		podName:         pod.Name,
		remove:          remove,
		requeueAttempts: 0,
	}

	return watcher
}

func (w *GateWatcher) GateKey() string {
	return fmt.Sprintf("%s/%s", w.podKey, w.gateName)
}

func (w *GateWatcher) PodKey() string {
	return w.podKey
}

func (w *GateWatcher) Start() {
	w.logger.Info("Starting gate watcher",
		"gate", w.gateName, "pod", w.podKey)

	go w.watch()
}

// evaluateCondition evaluates the gate condition
func (w *GateWatcher) evaluateCondition(eventObject *unstructured.Unstructured) (bool, time.Duration) {
	if w.condition.Expression == "" {
		// Existence check only
		return true, 0
	}

	// Get current pod state
	var pod corev1.Pod
	if err := w.Get(w.ctx, types.NamespacedName{Namespace: w.podNamespace, Name: w.podName}, &pod); err != nil {
		w.logger.Error(err, "Failed to get pod for condition evaluation", "pod", w.podKey)
		// If pod is not found, the watcher can be cleaned up
		if apierrors.IsNotFound(err) {
			return true, 0
		}
		return false, 0
	}

	// Check if gate still exists
	gateExists := false
	for _, gate := range pod.Spec.SchedulingGates {
		if gate.Name == w.gateName {
			gateExists = true
			break
		}
	}

	if !gateExists {
		// Gate was already removed, stop watching
		return true, 0
	}

	satisfied, requeueAfter, err := w.evaluateExpression(eventObject, &pod)
	if err != nil {
		w.logger.Error(err, "Failed to evaluate gate condition",
			"gate", w.gateName, "pod", w.podKey)
		return false, 0
	}

	return satisfied, requeueAfter
}

func (w *GateWatcher) evaluateExpression(eventObject *unstructured.Unstructured, pod *corev1.Pod) (bool, time.Duration, error) {
	if w.condition.Expression == "" {
		return false, 0, fmt.Errorf("expression is required")
	}

	// Convert pod to JSON
	podJSON, _ := json.Marshal(pod)
	var podData map[string]interface{}
	_ = json.Unmarshal(podJSON, &podData)

	// Convert unstructured to JSON
	resourceJSON, _ := eventObject.MarshalJSON()
	var resourceData map[string]interface{}
	_ = json.Unmarshal(resourceJSON, &resourceData)

	// Create CEL environment with declarations for our types
	env, err := cel.NewEnv(
		cel.Function("now",
			cel.Overload("now",
				[]*cel.Type{},
				cel.TimestampType,
				cel.FunctionBinding(func(args ...ref.Val) ref.Val {
					return celtypes.Timestamp{Time: time.Now()}
				}),
			),
		),
		cel.StdLib(),
		cel.Variable("resource", cel.DynType),
		cel.Variable("this", cel.DynType),
	)

	if err != nil {
		return false, 0, fmt.Errorf("failed to create CEL environment: %v", err)
	}

	// Parse and check the expression first
	parsed, issues := env.Parse(w.condition.Expression)
	if issues.Err() != nil {
		return false, 0, fmt.Errorf("failed to parse expression: %v", issues.Err())
	}
	checked, _ := env.Check(parsed)

	// Create program from checked expression
	prg, _ := env.Program(checked)

	// Evaluate expression with both object and pod
	out, _, err := prg.Eval(map[string]interface{}{
		"resource": resourceData,
		"this":     podData,
	})
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			w.logger.V(1).Info(
				"Expression checked missing field, waiting for update",
				"pod", podData, "resource", resourceData,
				"expression", w.condition.Expression)
			return false, 0, nil
		} else {
			return false, 0, fmt.Errorf("failed to evaluate expression: %v", err)
		}
	}

	// Convert result to bool
	result, ok := out.Value().(bool)
	if !ok {
		return false, 0, fmt.Errorf("expression did not evaluate to boolean")
	}

	var requeueAfter time.Duration
	if !result && strings.Contains(w.condition.Expression, "now()") {
		requeueAfter = 2 * time.Second
	}

	return result, requeueAfter, nil
}

func (w *GateWatcher) backoff() time.Duration {
	delay := time.Second * 2 * (1 << w.requeueAttempts)
	const maxDelay = time.Minute * 5
	if delay > maxDelay {
		delay = maxDelay
	}
	w.requeueAttempts++
	return delay
}

// removeGate removes the gate from the pod
func (w *GateWatcher) removeGate() error {
	// Get current pod state
	var pod corev1.Pod
	podNamespace, podName := strings.Split(w.podKey, "/")[0], strings.Split(w.podKey, "/")[1]

	if err := w.Get(w.ctx, types.NamespacedName{Namespace: podNamespace, Name: podName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			w.logger.Info("Pod not found, assuming it was deleted. Gate does not need to be removed.", "pod", w.podKey)
			return nil
		}
		w.logger.Error(err, "Failed to get pod for gate removal", "pod", w.podKey)
		return err
	}

	// Remove the specific gate
	var newGates []corev1.PodSchedulingGate
	found := false
	for _, gate := range pod.Spec.SchedulingGates {
		if gate.Name != w.gateName {
			newGates = append(newGates, gate)
		} else {
			found = true
		}
	}

	if !found {
		w.logger.Info("Gate not present on pod, assuming already removed", "gate", w.gateName, "pod", w.podKey)
		return nil
	}

	pod.Spec.SchedulingGates = newGates

	// Update the pod
	if err := w.Update(w.ctx, &pod); err != nil {
		w.logger.Info("Failed to update pod scheduling gates, requeue and try again",
			"gate", w.gateName, "pod", w.podKey)
		return err
	}

	w.logger.Info("Successfully removed gate", "gate", w.gateName, "pod", w.podKey)
	w.requeueAttempts = 0

	return nil
}

// getResourceName uses the discovery client to get the correct resource name
func (w *GateWatcher) getResourceName() string {
	// Use discovery client to get the correct resource name
	resources, err := w.Discovery.ServerResourcesForGroupVersion(w.condition.APIVersion)
	if err != nil {
		// Fallback to simple pluralization if discovery fails
		w.logger.V(1).Info("Discovery failed, using fallback pluralization",
			"apiVersion", w.condition.APIVersion, "error", err)
		return strings.ToLower(w.condition.Kind) + "s"
	}

	// Find the resource that matches our kind
	for _, resource := range resources.APIResources {
		if resource.Kind == w.condition.Kind {
			return resource.Name
		}
	}

	// If not found, fallback to simple pluralization
	w.logger.V(1).Info("Resource not found in discovery, using fallback pluralization",
		"apiVersion", w.condition.APIVersion, "kind", w.condition.Kind)
	return strings.ToLower(w.condition.Kind) + "s"
}

// watch is the main goroutine function that watches a gate condition
func (w *GateWatcher) watch() {
	// Get the correct resource name
	resourceName := w.getResourceName()

	// Parse API version properly
	gv, err := schema.ParseGroupVersion(w.condition.APIVersion)
	if err != nil {
		w.logger.Error(err, "Failed to parse API version", "apiVersion", w.condition.APIVersion)
		return
	}

	resource := schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resourceName,
	}

	var watchBuilder dynamic.ResourceInterface = w.Dynamic.Resource(resource)

	if w.condition.Namespaced {
		watchBuilder = watchBuilder.(dynamic.NamespaceableResourceInterface).Namespace(w.namespace)
	}

	// Set up watcher using dynamic client
	k8sWatcher, err := watchBuilder.Watch(
		w.ctx, v1.ListOptions{
			FieldSelector: fmt.Sprintf("metadata.name=%s", w.condition.Name),
		},
	)

	if err != nil {
		if w.condition.Namespaced {
			w.logger.Error(err, "Failed to create namespaced watcher for resource",
				"apiVersion", w.condition.APIVersion, "kind", w.condition.Kind,
				"name", w.condition.Name, "namespace", w.namespace)
		} else {
			w.logger.Error(err, "Failed to create cluster watcher for resource",
				"apiVersion", w.condition.APIVersion, "kind", w.condition.Kind,
				"name", w.condition.Name)
		}

		// Ensure we clean up this watcher from the controller's map
		w.remove(w)
		return
	}
	defer k8sWatcher.Stop()

	w.logger.Info("Watching resource for gate condition",
		"gate", w.gateName, "pod", w.podKey,
		"resource", fmt.Sprintf("%s/%s/%s", w.condition.APIVersion, w.condition.Kind, w.condition.Name))

	var timer *time.Timer
	var timerCh <-chan time.Time
	var lastKnownObject *unstructured.Unstructured

	evaluateAndAct := func() bool {
		if lastKnownObject == nil {
			w.logger.Info("Cannot evaluate, no resource object seen yet")
			return false // Don't stop watcher
		}

		satisfied, requeueAfter := w.evaluateCondition(lastKnownObject)

		if satisfied {
			w.logger.Info("Gate condition satisfied, removing gate",
				"gate", w.gateName, "pod", w.podKey)

			if err := w.removeGate(); err != nil {
				// Failed to remove gate, use exponential backoff
				requeueAfter = w.backoff()
				w.logger.Info("Failed to remove gate, scheduling re-evaluation", "after", requeueAfter)
			} else {
				w.remove(w) // Self-destruct
				return true // Stop watcher
			}
		}

		if requeueAfter > 0 {
			w.logger.Info("Condition not met or failed, scheduling re-evaluation", "after", requeueAfter)
			timer = time.NewTimer(requeueAfter)
		}
		return false // Don't stop watcher
	}

	// Watch for events
	for {
		if timer != nil {
			timerCh = timer.C
		}

		select {
		case <-w.ctx.Done():
			w.logger.Info("Gate watcher context cancelled", "gate", w.gateName, "pod", w.podKey)
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-k8sWatcher.ResultChan():
			if !ok {
				w.logger.Info("Watcher channel closed, terminating.")
				return
			}
			if timer != nil {
				timer.Stop()
			}
			if event.Type == "ERROR" {
				w.logger.Error(fmt.Errorf("watcher error"), "Error from resource watcher",
					"gate", w.gateName, "pod", w.podKey)
				continue
			}

			lastKnownObject = event.Object.(*unstructured.Unstructured)
			if stop := evaluateAndAct(); stop {
				return
			}
		case <-timerCh:
			w.logger.Info("Timer fired, re-evaluating condition")
			if stop := evaluateAndAct(); stop {
				return
			}
		}
	}
}
