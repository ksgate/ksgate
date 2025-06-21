package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/cel-go/cel"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	GatePrefix = "k8s.ksgate.org/"
)

// GateCondition represents a condition that must be satisfied for a pod to be scheduled.
// This struct matches the condition.schema.json specification.
type GateCondition struct {
	// APIVersion is the API version of the resource.
	APIVersion string `json:"apiVersion"`

	// Kind is the Kind of the resource.
	Kind string `json:"kind"`

	// Name is the name of the resource.
	Name string `json:"name"`

	// Namespace is the namespace of the resource. When omitted, the pod's namespace is used.
	Namespace string `json:"namespace,omitempty"`

	// Expression is a CEL expression that must evaluate to true.
	// When omitted, the existence of the resource is used to satisfy the condition.
	Expression string `json:"expression,omitempty"`
}

// GateWatcher represents a goroutine that watches a specific gate condition
type GateWatcher struct {
	cancel     context.CancelFunc
	condition  *GateCondition
	controller *PodController
	ctx        context.Context
	gateName   string
	podKey     string // format: namespace/name
}

// PodController reconciles Pod objects
type PodController struct {
	client.Client
	Scheme *runtime.Scheme
	Logger logr.Logger

	Dynamic *dynamic.DynamicClient

	// Goroutine management
	gateWatchers map[string]*GateWatcher // key: namespace/name/gate-name
	watcherMutex sync.RWMutex
}

// +kubebuilder:rbac:groups="*",resources=*,verbs=get
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
	otherGates := []corev1.PodSchedulingGate{}

	for _, gate := range pod.Spec.SchedulingGates {
		if strings.HasPrefix(gate.Name, GatePrefix) {
			ourGates = append(ourGates, gate)
		} else {
			otherGates = append(otherGates, gate)
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

	// Process each of our gates (fallback to polling)
	var removedGates []corev1.PodSchedulingGate
	for _, gate := range ourGates {
		if r.evaluateGate(ctx, &pod, gate) {
			removedGates = append(removedGates, gate)
			// Remove this gate by excluding it from otherGates
			continue
		}

		// Keep gate by adding to otherGates
		otherGates = append(otherGates, gate)
	}

	// Update pod if we removed any gates
	if len(removedGates) > 0 {
		pod.Spec.SchedulingGates = otherGates
		if err := r.Update(ctx, &pod); err != nil {
			logger.Error(err, "Failed to update pod scheduling gates")
			return ctrl.Result{}, err
		}
		logger.Info("Updated pod scheduling gates", "pod", req.NamespacedName)

		// Clean up watchers for removed gates
		for _, gate := range removedGates {
			r.stopGateWatcher(req.NamespacedName.String(), gate.Name)
		}
	}

	// Fallback to polling if any gates remain (for backward compatibility)
	if (len(ourGates) - len(removedGates)) > 0 {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

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
	for gateKey, watcher := range r.gateWatchers {
		if strings.HasPrefix(gateKey, podKey+"/") && !managedGates[gateKey] {
			watcher.cancel()
			delete(r.gateWatchers, gateKey)
		}
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

// stopGateWatcher stops a specific gate watcher
func (r *PodController) stopGateWatcher(podKey, gateName string) {
	gateKey := fmt.Sprintf("%s/%s", podKey, gateName)

	r.watcherMutex.Lock()
	defer r.watcherMutex.Unlock()

	if watcher, exists := r.gateWatchers[gateKey]; exists {
		watcher.cancel()
		delete(r.gateWatchers, gateKey)
		r.Logger.Info("Stopped gate watcher", "gate", gateName, "pod", podKey)
	}
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

// evaluateGate determines if a gate should be removed
func (r *PodController) evaluateGate(ctx context.Context, pod *corev1.Pod, gate corev1.PodSchedulingGate) bool {
	logger := log.FromContext(ctx)

	// Look for annotation matching the gate name
	annotationKey := gate.Name
	annotationValue, exists := pod.Annotations[annotationKey]
	if !exists {
		logger.Info("No condition annotation found matching gate name. This is an incorrect usage of the gate.", "gate", gate.Name, "pod", pod.Name, "namespace", pod.Namespace)
		return false
	}

	// Parse the JSON condition using the new struct
	var condition GateCondition
	if err := json.Unmarshal([]byte(annotationValue), &condition); err != nil {
		logger.Info("Failed to parse gate condition", "gate", gate.Name, "condition", annotationValue, "message", err.Error())
		return false
	}

	// Validate required fields
	if condition.APIVersion == "" || condition.Kind == "" || condition.Name == "" {
		logger.Info("Missing required fields in gate condition", "gate", gate.Name, "condition", condition)
		return false
	}

	// Evaluate the condition based on the JSON content
	satisfied, err := r.evaluateCondition(ctx, pod, &condition)
	if err != nil {
		logger.Info("Failed to evaluate condition", "gate", gate.Name, "condition", annotationValue, "message", err.Error())
		return false
	}

	return satisfied
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
