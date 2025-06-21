package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// startGateWatcher starts a new goroutine to watch a specific gate
func NewGateWatcher(
	ctx context.Context,
	client client.Client,
	dynamicClient *dynamic.DynamicClient,
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
		Cancel:       cancel,
		Client:       client,
		Dynamic:      dynamicClient,
		condition:    condition,
		ctx:          watcherCtx,
		gateName:     gate.Name,
		logger:       log.FromContext(watcherCtx),
		namespace:    namespace,
		podKey:       podKey,
		podNamespace: pod.Namespace,
		podName:      pod.Name,
		remove:       remove,
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
func (w *GateWatcher) evaluateCondition(eventObject *unstructured.Unstructured) bool {
	if w.condition.Expression == "" {
		// Existence check only
		return true
	}

	// Get current pod state
	var pod corev1.Pod
	if err := w.Get(w.ctx, types.NamespacedName{Namespace: w.podNamespace, Name: w.podName}, &pod); err != nil {
		w.logger.Error(err, "Failed to get pod for condition evaluation", "pod", w.podKey)
		// If pod is not found, clean up this watcher
		if apierrors.IsNotFound(err) {
			w.remove(w)
		}
		return false
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
		return true
	}

	satisfied, err := w.evaluateExpression(eventObject, &pod)
	if err != nil {
		w.logger.Error(err, "Failed to evaluate gate condition",
			"gate", w.gateName, "pod", w.podKey)
		return false
	}

	return satisfied
}

func (w *GateWatcher) evaluateExpression(eventObject *unstructured.Unstructured, pod *corev1.Pod) (bool, error) {
	if w.condition.Expression == "" {
		return false, fmt.Errorf("expression is required")
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
	env, _ := cel.NewEnv(
		cel.Variable("resource", cel.DynType),
		cel.Variable("pod", cel.DynType),
	)

	// Parse and check the expression first
	parsed, issues := env.Parse(w.condition.Expression)
	if issues.Err() != nil {
		return false, fmt.Errorf("failed to parse expression: %v", issues.Err())
	}
	checked, _ := env.Check(parsed)

	// Create program from checked expression
	prg, _ := env.Program(checked)

	// Evaluate expression with both object and pod
	out, _, err := prg.Eval(map[string]interface{}{
		"resource": resourceData,
		"pod":      podData,
	})
	if err != nil {
		return false, fmt.Errorf("failed to evaluate expression: %v", err)
	}

	// Convert result to bool
	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("expression did not evaluate to boolean")
	}
	return result, nil
}

// removeGate removes the gate from the pod
func (w *GateWatcher) removeGate() {
	// Get current pod state
	var pod corev1.Pod
	podNamespace, podName := strings.Split(w.podKey, "/")[0], strings.Split(w.podKey, "/")[1]

	if err := w.Get(w.ctx, types.NamespacedName{Namespace: podNamespace, Name: podName}, &pod); err != nil {
		w.logger.Error(err, "Failed to get pod for gate removal", "pod", w.podKey)
		return
	}

	// Remove the specific gate
	var newGates []corev1.PodSchedulingGate
	for _, gate := range pod.Spec.SchedulingGates {
		if gate.Name != w.gateName {
			newGates = append(newGates, gate)
		}
	}

	pod.Spec.SchedulingGates = newGates

	// Update the pod
	if err := w.Update(w.ctx, &pod); err != nil {
		w.logger.Error(err, "Failed to update pod scheduling gates",
			"gate", w.gateName, "pod", w.podKey)
		return
	}

	w.logger.Info("Successfully removed gate", "gate", w.gateName, "pod", w.podKey)
}

// watch is the main goroutine function that watches a gate condition
func (w *GateWatcher) watch() {
	// Parse API version properly
	gv, err := schema.ParseGroupVersion(w.condition.APIVersion)
	if err != nil {
		w.logger.Error(err, "Failed to parse API version", "apiVersion", w.condition.APIVersion)
		return
	}

	// Convert kind to lowercase plural for resource name
	resourceName := strings.ToLower(w.condition.Kind) + "s" // Simple pluralization

	resource := schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resourceName,
	}

	// Set up watcher using dynamic client
	watcher, err := w.Dynamic.Resource(resource).Namespace(w.namespace).Watch(
		w.ctx, v1.ListOptions{
			FieldSelector: fmt.Sprintf("metadata.name=%s", w.condition.Name),
		},
	)

	if err != nil {
		w.logger.Error(err, "Failed to create watcher for resource",
			"apiVersion", w.condition.APIVersion, "kind", w.condition.Kind,
			"name", w.condition.Name, "namespace", w.namespace)
		// Ensure we clean up this watcher from the controller's map
		w.remove(w)
		return
	}
	defer watcher.Stop()

	w.logger.Info("Watching resource for gate condition",
		"gate", w.gateName, "pod", w.podKey,
		"resource", fmt.Sprintf("%s/%s/%s", w.condition.APIVersion, w.condition.Kind, w.condition.Name))

	// Watch for events
	for {
		select {
		case <-w.ctx.Done():
			w.logger.Info("Gate watcher context cancelled", "gate", w.gateName, "pod", w.podKey)
			return
		case event := <-watcher.ResultChan():
			if event.Type == "ERROR" {
				w.logger.Error(fmt.Errorf("watcher error"), "Error from resource watcher",
					"gate", w.gateName, "pod", w.podKey)
				continue
			}

			eventObject := event.Object.(*unstructured.Unstructured)

			// Evaluate condition when resource changes
			if satisfied := w.evaluateCondition(eventObject); satisfied {
				w.logger.Info("Gate condition satisfied, removing gate",
					"gate", w.gateName, "pod", w.podKey)
				w.removeGate()

				// Safely stop and remove this watcher
				w.remove(w)

				return
			}
		}
	}
}
