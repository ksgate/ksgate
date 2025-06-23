package controller

import (
	"sync"

	"github.com/go-logr/logr"
	"github.com/ksgate/ksgate/internal/watcher"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	GatePrefix = "k8s.ksgate.org/"
)

// PodController reconciles Pod objects
type PodController struct {
	client.Client
	Scheme *runtime.Scheme
	Logger logr.Logger

	Dynamic   *dynamic.DynamicClient
	Discovery discovery.DiscoveryInterface

	// Goroutine management
	gateWatchers map[string]*watcher.GateWatcher // key: namespace/name/gate-name
	watcherMutex sync.RWMutex
}
