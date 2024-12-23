package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	GatePrefix = "gateman.kdex.dev/"
)

// PodController reconciles Pod objects
type PodController struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile handles Pod events
func (r *PodController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get the Pod
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip if pod is not in SchedulingGated state
	if pod.Status.Phase != "SchedulingGated" {
		return ctrl.Result{}, nil
	}

	// Skip if pod has no scheduling gates
	if len(pod.Spec.SchedulingGates) == 0 {
		return ctrl.Result{}, nil
	}

	// Find our gates
	var ourGates []corev1.PodSchedulingGate
	var otherGates []corev1.PodSchedulingGate

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

	logger.Info("Processing pod scheduling gates",
		"pod", req.NamespacedName,
		"gates", ourGates)

	// Process each of our gates
	removedGates := false
	for _, gate := range ourGates {
		shouldRemove, err := r.evaluateGate(ctx, &pod, gate)
		if err != nil {
			logger.Error(err, "Failed evaluating gate", "gate", gate.Name)
			continue
		}

		if shouldRemove {
			removedGates = true
			// Remove this gate by excluding it from otherGates
			continue
		}

		// Keep gate by adding to otherGates
		otherGates = append(otherGates, gate)
	}

	// Update pod if we removed any gates
	if removedGates {
		pod.Spec.SchedulingGates = otherGates
		if err := r.Update(ctx, &pod); err != nil {
			logger.Error(err, "Failed to update pod scheduling gates")
			return ctrl.Result{}, err
		}
		logger.Info("Updated pod scheduling gates", "pod", req.NamespacedName)
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
		logger.Info("No annotation found for gate", "gate", gate.Name)
		return false, nil
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
	// Get the condition type
	conditionType, ok := condition["type"].(string)
	if !ok {
		return false, fmt.Errorf("condition type not specified")
	}

	switch conditionType {
	case "resourceExists":
		return r.evaluateResourceExists(ctx, condition)
	case "labelExists":
		return r.evaluateLabelExists(ctx, condition)
	case "expression":
		return r.evaluateExpression(ctx, condition)
	default:
		return false, fmt.Errorf("unknown condition type: %s", conditionType)
	}
}

// Example condition evaluators
func (r *PodController) evaluateResourceExists(ctx context.Context, condition map[string]interface{}) (bool, error) {
	// Extract required fields
	apiVersion, ok1 := condition["apiVersion"].(string)
	kind, ok2 := condition["kind"].(string)
	name, ok3 := condition["name"].(string)
	namespace, ok4 := condition["namespace"].(string)

	if !ok1 || !ok2 || !ok3 || !ok4 {
		return false, fmt.Errorf("missing required fields for resourceExists condition")
	}

	// Create an unstructured object to query the resource
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(apiVersion)
	obj.SetKind(kind)

	// Check if resource exists
	err := r.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, obj)

	return err == nil, nil
}

func (r *PodController) evaluateLabelExists(ctx context.Context, condition map[string]interface{}) (bool, error) {
	// Implementation for checking if a label exists on a resource
	return false, nil
}

func (r *PodController) evaluateExpression(ctx context.Context, condition map[string]interface{}) (bool, error) {
	// Implementation for evaluating CEL expressions
	return false, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *PodController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}
