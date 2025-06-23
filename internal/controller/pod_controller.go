package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ksgate/ksgate/internal/watcher"
	corev1 "k8s.io/api/core/v1"
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
		r.cleanupPodWatchers(req.String())
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
		r.cleanupPodWatchers(req.String())
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
		r.gateWatchers = make(map[string]*watcher.GateWatcher)
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
		condition := watcher.GateCondition{
			APIVersion: "v1",
			Namespaced: true,
		}
		if err := json.Unmarshal([]byte(annotationValue), &condition); err != nil {
			logger.Info("Failed to parse gate condition",
				"gate", gate.Name, "condition", annotationValue, "error", err.Error())
			continue
		}

		if !condition.Namespaced {
			condition.Namespace = ""
		}

		// Validate required fields
		if condition.Kind == "" || condition.Name == "" {
			logger.Info("Missing required fields in gate condition",
				"gate", gate.Name, "pod", podKey, "condition", condition)
			continue
		}

		// Start watcher
		r.startGateWatcher(ctx, pod, gate, &condition)
	}

	// Clean up watchers for gates that no longer exist
	watchersToRemove := []*watcher.GateWatcher{}
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
		delete(r.gateWatchers, watcher.GateKey())
	}
}

// startGateWatcher starts a new goroutine to watch a specific gate
func (r *PodController) startGateWatcher(ctx context.Context, pod *corev1.Pod, gate corev1.PodSchedulingGate, condition *watcher.GateCondition) {
	watcher := watcher.NewGateWatcher(
		ctx, r.Client, r.Dynamic, r.Discovery, r.stopAndRemoveWatcher, pod, gate, condition)

	r.gateWatchers[watcher.GateKey()] = watcher

	watcher.Start()
}

// cleanupPodWatchers stops all watchers for a pod
func (r *PodController) cleanupPodWatchers(podKey string) {
	r.watcherMutex.Lock()
	defer r.watcherMutex.Unlock()

	for gateKey, watcher := range r.gateWatchers {
		if strings.HasPrefix(gateKey, podKey+"/") {
			watcher.Cancel()
			delete(r.gateWatchers, gateKey)
		}
	}
}

// stopAndRemoveWatcher safely stops and removes a watcher from the controller's map
func (r *PodController) stopAndRemoveWatcher(watcher *watcher.GateWatcher) {
	r.watcherMutex.Lock()
	defer r.watcherMutex.Unlock()

	watcher.Cancel()
	delete(r.gateWatchers, watcher.GateKey())
}

// stopWatcherOnly stops a watcher without removing it from the map (for use when mutex is already held)
func (r *PodController) stopWatcherOnly(watcher *watcher.GateWatcher) {
	watcher.Cancel()
}

// GetWatcherStats returns statistics about active watchers for monitoring
func (r *PodController) GetWatcherStats() map[string]int {
	r.watcherMutex.RLock()
	defer r.watcherMutex.RUnlock()

	stats := make(map[string]int)
	for _, watcher := range r.gateWatchers {
		stats[watcher.PodKey()]++
	}

	return stats
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
