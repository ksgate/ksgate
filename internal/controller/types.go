package controller

import (
	"context"
	"sync"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	cancel       context.CancelFunc
	condition    *GateCondition
	controller   *PodController
	ctx          context.Context
	logger       logr.Logger
	namespace    string
	gateName     string
	podKey       string // format: namespace/name
	podNamespace string
	podName      string
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
