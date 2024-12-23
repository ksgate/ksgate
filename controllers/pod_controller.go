package controllers

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	// TODO: Implement your custom gate evaluation logic here
	// This is where you would check conditions specific to your gates

	// Example: Remove gate after some condition is met
	return false, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *PodController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}
