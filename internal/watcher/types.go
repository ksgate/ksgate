package watcher

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	// Namespaced indicates if the resource is namespaced. If false, the resource is considered to be Cluster scoped.
	Namespaced bool `json:"namespaced,omitempty"`

	// Expression is a CEL expression that must evaluate to true.
	// When omitted, the existence of the resource is used to satisfy the condition.
	Expression string `json:"expression,omitempty"`
}

// GateWatcher represents a goroutine that watches a specific gate condition
type GateWatcher struct {
	client.Client
	Cancel    context.CancelFunc
	Dynamic   dynamic.Interface
	condition *GateCondition
	// controller   *controller.PodController
	ctx          context.Context
	gateName     string
	logger       logr.Logger
	namespace    string
	podKey       string // format: namespace/name
	podName      string
	podNamespace string
	remove       func(*GateWatcher)
	// requeueAttempts tracks the number of retries for failed operations
	requeueAttempts int
}
