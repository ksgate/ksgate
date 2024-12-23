package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	GatePrefix = "gateman.kdex.dev/"
)

// PodController reconciles Pod objects
type PodController struct {
	client.Client
	Scheme *runtime.Scheme
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
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip if pod is not in SchedulingGated state
	// if pod.Status.Phase != "SchedulingGated" {
	// 	return ctrl.Result{}, nil
	// }

	// Skip if pod has no scheduling gates
	// if len(pod.Spec.SchedulingGates) == 0 {
	// 	return ctrl.Result{}, nil
	// }

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
		return ctrl.Result{}, nil
	}

	logger.Info("Processing gateman.kdex.dev pod scheduling gates",
		"pod", req.NamespacedName,
		"gates", ourGates)

	// Process each of our gates
	var removedGates []corev1.PodSchedulingGate
	for _, gate := range ourGates {
		shouldRemove, err := r.evaluateGate(ctx, &pod, gate)
		if err != nil {
			logger.Error(err, "Failed evaluating gate", "gate", gate.Name)
			continue
		}

		if shouldRemove {
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
	}

	if (len(ourGates) - len(removedGates)) > 0 {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// evaluateGate determines if a gate should be removed
func (r *PodController) evaluateGate(ctx context.Context, pod *corev1.Pod, gate corev1.PodSchedulingGate) (bool, error) {
	logger := log.FromContext(ctx)

	// Look for annotation matching the gate name
	annotationKey := gate.Name
	condition, exists := pod.Annotations[annotationKey]
	if !exists {
		logger.Error(nil, "No condition annotation found for gate", "gate", gate.Name)
		return false, fmt.Errorf("missing gate condition annotation: %v", gate.Name)
	}

	// Parse the JSON condition
	var gateCondition map[string]interface{}
	if err := json.Unmarshal([]byte(condition), &gateCondition); err != nil {
		logger.Error(err, "Failed to parse gate condition", "gate", gate.Name, "condition", condition)
		return false, fmt.Errorf("invalid gate condition format: %v", err)
	}

	// Evaluate the condition based on the JSON content
	satisfied, err := r.evaluateCondition(ctx, pod, gateCondition)
	if err != nil {
		logger.Error(err, "Failed to evaluate condition", "gate", gate.Name, "condition", gateCondition)
		return false, err
	}

	return satisfied, nil
}

func (r *PodController) evaluateCondition(ctx context.Context, pod *corev1.Pod, condition map[string]interface{}) (bool, error) {
	expression := condition["expression"]
	if expression == nil {
		return r.evaluateResourceExists(ctx, pod, condition)
	}
	return r.evaluateExpression(ctx, pod, condition)
}

// Example condition evaluators
func (r *PodController) evaluateResourceExists(ctx context.Context, pod *corev1.Pod, condition map[string]interface{}) (bool, error) {
	// Check if resource exists
	_, err := r.resourceLookup(ctx, pod, condition)

	if apierrors.IsNotFound(err) {
		return false, nil
	}

	return err == nil, err
}

func (r *PodController) evaluateExpression(ctx context.Context, pod *corev1.Pod, condition map[string]interface{}) (bool, error) {
	// Check if resource exists
	resource, err := r.resourceLookup(ctx, pod, condition)

	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}

		if err.Error() == "missing required fields for resourceLookup" {
			return false, err
		}
		return false, nil
	}

	// Get the expression from the condition
	expr, ok := condition["expression"].(string)
	if !ok {
		return false, fmt.Errorf("expression not specified")
	}

	// Convert pod to JSON
	podJSON, err := json.Marshal(pod)
	if err != nil {
		return false, fmt.Errorf("failed to marshal pod: %v", err)
	}
	var podData map[string]interface{}
	if err := json.Unmarshal(podJSON, &podData); err != nil {
		return false, fmt.Errorf("failed to unmarshal pod: %v", err)
	}

	// Convert unstructured to JSON
	resourceJSON, err := resource.MarshalJSON()
	if err != nil {
		return false, fmt.Errorf("failed to marshal resource: %v", err)
	}
	var resourceData map[string]interface{}
	if err := json.Unmarshal(resourceJSON, &resourceData); err != nil {
		return false, fmt.Errorf("failed to unmarshal resource: %v", err)
	}

	// Create CEL environment with declarations for our types
	env, err := cel.NewEnv(
		cel.Variable("resource", cel.AnyType),
		cel.Variable("pod", cel.AnyType),
	)
	if err != nil {
		return false, fmt.Errorf("failed to create CEL environment: %v", err)
	}

	// Parse and check the expression first
	parsed, issues := env.Parse(expr)
	if issues.Err() != nil {
		return false, fmt.Errorf("failed to parse expression: %v", issues.Err())
	}

	checked, issues := env.Check(parsed)
	if issues.Err() != nil {
		return false, fmt.Errorf("failed to type-check expression: %v", issues.Err())
	}

	// Create program from checked expression
	prg, err := env.Program(checked)
	if err != nil {
		return false, fmt.Errorf("failed to create program: %v", err)
	}

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

func (r *PodController) resourceLookup(ctx context.Context, pod *corev1.Pod, condition map[string]interface{}) (*unstructured.Unstructured, error) {
	// Extract required fields
	// Create an unstructured object to query the resource
	apiVersion, ok1 := condition["apiVersion"].(string)
	kind, ok2 := condition["kind"].(string)
	name, ok3 := condition["name"].(string)
	namespace, ok4 := condition["namespace"].(string)

	if !ok4 {
		namespace = pod.Namespace
	}

	if !ok1 || !ok2 || !ok3 {
		return nil, fmt.Errorf("missing required fields for resourceLookup")
	}

	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(apiVersion)
	obj.SetKind(kind)

	err := r.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, obj)

	if err == nil {
		return obj, nil
	}

	return nil, err
}

// SetupWithManager sets up the controller with the Manager
func (r *PodController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("gateman").
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
