package internal

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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// +kubebuilder:rbac:groups="*",resources=*,verbs=get;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=pods/status,verbs=get

// Reconcile handles Pod events
func (r *PodController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get the Pod
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		// Pod was deleted, clean up any watchers
		r.cleanupPodWatchers(req.NamespacedName.String())
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Find our gates
	ourGates := []corev1.PodSchedulingGate{}

	for _, gate := range pod.Spec.SchedulingGates {
		if strings.HasPrefix(gate.Name, GatePrefix) {
			ourGates = append(ourGates, gate)
		}
	}

	// Skip if no gates with our prefix
	if len(ourGates) == 0 {
		r.cleanupPodWatchers(req.NamespacedName.String())
		return ctrl.Result{}, nil
	}

	logger.Info("Processing k8s.ksgate.org pod scheduling gates",
		"pod", req.NamespacedName,
		"gates", ourGates)

	// Ensure gate watchers are running for this pod
	r.ensureGateWatchers(ctx, &pod, ourGates)

	return ctrl.Result{}, nil
}

// ensureGateWatchers starts watchers for all gates of a pod
func (r *PodController) ensureGateWatchers(ctx context.Context, pod *corev1.Pod, gates []corev1.PodSchedulingGate) {
	logger := log.FromContext(ctx)

	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	r.watcherMutex.Lock()
	defer r.watcherMutex.Unlock()

	// Initialize watchers map if needed
	if r.gateWatchers == nil {
		r.gateWatchers = make(map[string]*GateWatcher)
	}

	// Track which gates we're managing
	managedGates := make(map[string]bool)

	for _, gate := range gates {
		gateKey := fmt.Sprintf("%s/%s", podKey, gate.Name)
		managedGates[gateKey] = true

		// Check if watcher already exists
		if _, exists := r.gateWatchers[gateKey]; exists {
			continue
		}

		// Get condition from annotation
		annotationValue, exists := pod.Annotations[gate.Name]
		if !exists {
			logger.Info("No condition annotation found for gate",
				"gate", gate.Name, "pod", podKey)
			continue
		}

		// Parse condition using the new struct
		var condition GateCondition
		if err := json.Unmarshal([]byte(annotationValue), &condition); err != nil {
			logger.Info("Failed to parse gate condition",
				"gate", gate.Name, "condition", annotationValue, "error", err.Error())
			continue
		}

		// Validate required fields
		if condition.APIVersion == "" || condition.Kind == "" || condition.Name == "" {
			logger.Info("Missing required fields in gate condition",
				"gate", gate.Name, "pod", podKey, "condition", condition)
			continue
		}

		// Start watcher
		r.startGateWatcher(ctx, pod, gate, &condition)
	}

	// Clean up watchers for gates that no longer exist
	watchersToRemove := []*GateWatcher{}
	for gateKey, watcher := range r.gateWatchers {
		if strings.HasPrefix(gateKey, podKey+"/") && !managedGates[gateKey] {
			watchersToRemove = append(watchersToRemove, watcher)
		}
	}

	// Stop watchers first (without mutex since we already hold it)
	for _, watcher := range watchersToRemove {
		r.stopWatcherOnly(watcher)
	}

	// Then remove them from the map
	for _, watcher := range watchersToRemove {
		delete(r.gateWatchers, watcher.gateKey())
	}
}

// startGateWatcher starts a new goroutine to watch a specific gate
func (r *PodController) startGateWatcher(ctx context.Context, pod *corev1.Pod, gate corev1.PodSchedulingGate, condition *GateCondition) {
	logger := log.FromContext(ctx)

	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	gateKey := fmt.Sprintf("%s/%s", podKey, gate.Name)

	watcherCtx, cancel := context.WithCancel(ctx)

	watcher := &GateWatcher{
		podKey:     podKey,
		gateName:   gate.Name,
		condition:  condition,
		ctx:        watcherCtx,
		cancel:     cancel,
		controller: r,
	}

	r.gateWatchers[gateKey] = watcher

	logger.Info("Starting gate watcher",
		"gate", gate.Name, "pod", podKey)

	go watcher.watch()
}

// cleanupPodWatchers stops all watchers for a pod
func (r *PodController) cleanupPodWatchers(podKey string) {
	r.watcherMutex.Lock()
	defer r.watcherMutex.Unlock()

	for gateKey, watcher := range r.gateWatchers {
		if strings.HasPrefix(gateKey, podKey+"/") {
			watcher.cancel()
			delete(r.gateWatchers, gateKey)
		}
	}
}

// stopAndRemoveWatcher safely stops and removes a watcher from the controller's map
func (r *PodController) stopAndRemoveWatcher(watcher *GateWatcher) {
	r.watcherMutex.Lock()
	defer r.watcherMutex.Unlock()

	watcher.cancel()
	delete(r.gateWatchers, watcher.gateKey())
}

// stopWatcherOnly stops a watcher without removing it from the map (for use when mutex is already held)
func (r *PodController) stopWatcherOnly(watcher *GateWatcher) {
	watcher.cancel()
}

// GetWatcherStats returns statistics about active watchers for monitoring
func (r *PodController) GetWatcherStats() map[string]int {
	r.watcherMutex.RLock()
	defer r.watcherMutex.RUnlock()

	stats := make(map[string]int)
	for gateKey := range r.gateWatchers {
		// Extract pod key from gate key (format: namespace/name/gate-name)
		parts := strings.Split(gateKey, "/")
		if len(parts) >= 2 {
			podKey := fmt.Sprintf("%s/%s", parts[0], parts[1])
			stats[podKey]++
		}
	}
	return stats
}

func (w *GateWatcher) gateKey() string {
	return fmt.Sprintf("%s/%s", w.podKey, w.gateName)
}

// watch is the main goroutine function that watches a gate condition
func (w *GateWatcher) watch() {
	logger := log.FromContext(w.ctx)

	// Use the structured condition
	condition := w.condition

	// Determine namespace - use condition namespace if specified, otherwise pod namespace
	namespace := condition.Namespace
	if namespace == "" {
		namespace = strings.Split(w.podKey, "/")[0]
	}

	// Parse API version properly
	gv, err := schema.ParseGroupVersion(condition.APIVersion)
	if err != nil {
		logger.Error(err, "Failed to parse API version", "apiVersion", condition.APIVersion)
		return
	}

	// Convert kind to lowercase plural for resource name
	resourceName := strings.ToLower(condition.Kind) + "s" // Simple pluralization

	resource := schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resourceName,
	}

	// Set up watcher using dynamic client
	watcher, err := w.controller.Dynamic.Resource(resource).Namespace(namespace).Watch(
		w.ctx, v1.ListOptions{
			FieldSelector: fmt.Sprintf("metadata.name=%s", condition.Name),
		},
	)

	if err != nil {
		logger.Error(err, "Failed to create watcher for resource",
			"apiVersion", condition.APIVersion, "kind", condition.Kind,
			"name", condition.Name, "namespace", namespace)
		// Ensure we clean up this watcher from the controller's map
		w.controller.stopAndRemoveWatcher(w)
		return
	}
	defer watcher.Stop()

	logger.Info("Watching resource for gate condition",
		"gate", w.gateName, "pod", w.podKey,
		"resource", fmt.Sprintf("%s/%s/%s", condition.APIVersion, condition.Kind, condition.Name))

	// Watch for events
	for {
		select {
		case <-w.ctx.Done():
			logger.Info("Gate watcher context cancelled", "gate", w.gateName, "pod", w.podKey)
			return
		case event := <-watcher.ResultChan():
			if event.Type == "ERROR" {
				logger.Error(fmt.Errorf("watcher error"), "Error from resource watcher",
					"gate", w.gateName, "pod", w.podKey)
				continue
			}

			// Evaluate condition when resource changes
			if satisfied := w.evaluateCondition(); satisfied {
				logger.Info("Gate condition satisfied, removing gate",
					"gate", w.gateName, "pod", w.podKey)
				w.removeGate()

				// Safely stop and remove this watcher
				w.controller.stopAndRemoveWatcher(w)

				return
			}
		}
	}
}

// evaluateCondition evaluates the gate condition
func (w *GateWatcher) evaluateCondition() bool {
	logger := log.FromContext(w.ctx)

	// Get current pod state
	var pod corev1.Pod
	podNamespace, podName := strings.Split(w.podKey, "/")[0], strings.Split(w.podKey, "/")[1]

	if err := w.controller.Get(w.ctx, types.NamespacedName{Namespace: podNamespace, Name: podName}, &pod); err != nil {
		logger.Error(err, "Failed to get pod for condition evaluation", "pod", w.podKey)
		// If pod is not found, clean up this watcher
		if apierrors.IsNotFound(err) {
			w.controller.stopAndRemoveWatcher(w)
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

	// Evaluate condition using existing logic
	satisfied, err := w.controller.evaluateCondition(w.ctx, &pod, w.condition)
	if err != nil {
		logger.Error(err, "Failed to evaluate gate condition",
			"gate", w.gateName, "pod", w.podKey)
		return false
	}

	return satisfied
}

// removeGate removes the gate from the pod
func (w *GateWatcher) removeGate() {
	logger := log.FromContext(w.ctx)

	// Get current pod state
	var pod corev1.Pod
	podNamespace, podName := strings.Split(w.podKey, "/")[0], strings.Split(w.podKey, "/")[1]

	if err := w.controller.Get(w.ctx, types.NamespacedName{Namespace: podNamespace, Name: podName}, &pod); err != nil {
		logger.Error(err, "Failed to get pod for gate removal", "pod", w.podKey)
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
	if err := w.controller.Update(w.ctx, &pod); err != nil {
		logger.Error(err, "Failed to update pod scheduling gates",
			"gate", w.gateName, "pod", w.podKey)
		return
	}

	logger.Info("Successfully removed gate", "gate", w.gateName, "pod", w.podKey)
}

func (r *PodController) evaluateCondition(ctx context.Context, pod *corev1.Pod, condition *GateCondition) (bool, error) {
	if condition == nil {
		return false, fmt.Errorf("condition is required")
	}

	if condition.Expression == "" {
		return r.evaluateResourceExists(ctx, pod, condition)
	}
	return r.evaluateExpression(ctx, pod, condition)
}

// Example condition evaluators
func (r *PodController) evaluateResourceExists(ctx context.Context, pod *corev1.Pod, condition *GateCondition) (bool, error) {
	// Check if resource exists
	resource, err := r.resourceLookup(ctx, pod, condition)

	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}

		return false, err
	}

	return resource != nil, nil
}

func (r *PodController) evaluateExpression(ctx context.Context, pod *corev1.Pod, condition *GateCondition) (bool, error) {
	if condition.Expression == "" {
		return false, fmt.Errorf("expression is required")
	}

	// Check if resource exists
	resource, err := r.resourceLookup(ctx, pod, condition)

	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}

		return false, err
	}

	// Convert pod to JSON
	podJSON, _ := json.Marshal(pod)
	var podData map[string]interface{}
	_ = json.Unmarshal(podJSON, &podData)

	// Convert unstructured to JSON
	resourceJSON, _ := resource.MarshalJSON()
	var resourceData map[string]interface{}
	_ = json.Unmarshal(resourceJSON, &resourceData)

	// Create CEL environment with declarations for our types
	env, _ := cel.NewEnv(
		cel.Variable("resource", cel.DynType),
		cel.Variable("pod", cel.DynType),
	)

	// Parse and check the expression first
	parsed, issues := env.Parse(condition.Expression)
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

func (r *PodController) resourceLookup(ctx context.Context, pod *corev1.Pod, condition *GateCondition) (*unstructured.Unstructured, error) {
	// Determine namespace - use condition namespace if specified, otherwise pod namespace
	namespace := condition.Namespace
	if namespace == "" {
		namespace = pod.Namespace
	}

	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(condition.APIVersion)
	obj.SetKind(condition.Kind)

	err := r.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      condition.Name,
	}, obj)

	if err == nil {
		return obj, nil
	}

	return nil, err
}

// SetupWithManager sets up the controller with the Manager
func (r *PodController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("ksgate").
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.podToRequests),
		).
		Complete(r)
}

// podToRequests maps a Pod to reconciliation requests
func (r *PodController) podToRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	pod := obj.(*corev1.Pod)

	// Skip if pod is not in SchedulingGated state
	if pod.Status.Phase != "Pending" {
		return nil
	}

	isSchedulingGated := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "PodScheduled" && condition.Reason == "SchedulingGated" {
			isSchedulingGated = true
		}
	}

	// Skip if pod has no scheduling gates
	if !isSchedulingGated {
		return nil
	}

	// Check if any gates have our prefix
	hasOurGate := false
	for _, gate := range pod.Spec.SchedulingGates {
		if strings.HasPrefix(gate.Name, GatePrefix) {
			hasOurGate = true
			break
		}
	}

	if !hasOurGate {
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		},
	}
}
