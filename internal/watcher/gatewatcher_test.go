package watcher

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discfake "k8s.io/client-go/discovery/fake"
	dynfake "k8s.io/client-go/dynamic/fake"
	kfake "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestGateWatcher_evaluateCondition(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)

	// Create a test pod
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			SchedulingGates: []corev1.PodSchedulingGate{
				{
					Name: "k8s.ksgate.org/test-gate",
				},
			},
		},
	}

	tests := []struct {
		name           string
		condition      *GateCondition
		object         *unstructured.Unstructured
		addPod         bool
		removeGate     bool
		expectedResult bool
	}{
		{
			name: "resourceExists - valid fields",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test",
				Namespace:  "default",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test",
						"namespace": "default",
					},
				},
			},
			expectedResult: true,
			addPod:         true,
		},
		{
			name: "expression - missing required fields",
			condition: &GateCondition{
				Expression: "this is not a valid expression",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test",
						"namespace": "default",
					},
				},
			},
			expectedResult: false,
			addPod:         true,
		},
		{
			name: "expression - invalid expression",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "invalid && syntax ||",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			expectedResult: false,
			addPod:         true,
		},
		{
			name: "expression - simple true condition",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "true",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			expectedResult: true,
			addPod:         true,
		},
		{
			name: "expression - simple false condition",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "false",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			expectedResult: false,
			addPod:         true,
		},
		{
			name: "expression - complex boolean expression (true)",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "true && (true || false)",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			expectedResult: true,
			addPod:         true,
		},
		{
			name: "expression - complex boolean expression (false)",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "true && false",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			expectedResult: false,
			addPod:         true,
		},
		{
			name: "expression - with pod variables",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "this.metadata.name == 'test-pod'",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			expectedResult: true,
			addPod:         true,
		},
		{
			name: "expression - with pod variables (false case)",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "this.metadata.name == 'different-pod'",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			expectedResult: false,
			addPod:         true,
		},
		{
			name: "expression - with complex pod variable access",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "this.metadata.namespace == 'default' && this.metadata.name == 'test-pod'",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			expectedResult: true,
			addPod:         true,
		},
		{
			name: "expression - with target resource fields",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "resource.metadata.name == 'test-cm' && resource.metadata.namespace == 'default'",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			expectedResult: true,
			addPod:         true,
		},
		{
			name: "no pod exists",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "resource.metadata.name == 'test-cm' && resource.metadata.namespace == 'default'",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			expectedResult: true,
			addPod:         false,
		},
		{
			name: "pod no longer has gate",
			condition: &GateCondition{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "test-cm",
				Namespace:  "default",
				Expression: "true",
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": "default",
					},
				},
			},
			addPod:         true,
			removeGate:     true,
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeObjects := []runtime.Object{}

			if tt.addPod {
				podCopy := testPod.DeepCopy()
				if tt.removeGate {
					podCopy.Spec.SchedulingGates = []corev1.PodSchedulingGate{}
				}
				runtimeObjects = append(runtimeObjects, podCopy)
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(runtimeObjects...).
				Build()

			// fake.NewSimpleDynamicClient(scheme)

			w := &GateWatcher{
				Client:       client,
				condition:    tt.condition,
				ctx:          context.Background(),
				gateName:     "k8s.ksgate.org/test-gate",
				namespace:    "default",
				podKey:       "default/test-pod",
				podNamespace: "default",
				podName:      "test-pod",
			}

			result, _ := w.evaluateCondition(tt.object)

			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestGateWatcher_evaluateExpression(t *testing.T) {
	// Create a test pod
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			SchedulingGates: []corev1.PodSchedulingGate{
				{
					Name: "k8s.ksgate.org/test-gate",
				},
			},
		},
	}

	tests := []struct {
		name           string
		expression     string
		object         *unstructured.Unstructured
		expectedResult bool
		expectedError  bool
	}{
		{
			name:           "1 missing expression",
			expression:     "",
			expectedResult: false,
			expectedError:  true,
		},
		{
			name:       "expression - simple true condition",
			expression: "true",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test",
						"namespace": "default",
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
		{
			name:       "3 expression does not evaluate to boolean",
			expression: "45",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test",
						"namespace": "default",
					},
				},
			},
			expectedResult: false,
			expectedError:  true,
		},
		{
			name:       "time-based expression should return requeue duration",
			expression: "now() > timestamp('2030-01-01T00:00:00Z')",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test",
						"namespace": "default",
					},
				},
			},
			expectedResult: false,
			expectedError:  false,
		},
		{
			name:       "time-based that succeeds",
			expression: "now() < timestamp('2100-01-01T00:00:00Z')",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test",
						"namespace": "default",
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
		{
			name:       "time-based createFutureTimestamp",
			expression: "now() < timestamp(resource.metadata.creationTimestamp)",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":              "test",
						"namespace":         "default",
						"creationTimestamp": createFutureTimestamp(1000000000),
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
		{
			name:       "time-based createPastTimestamp",
			expression: "now() > timestamp(resource.metadata.creationTimestamp)",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":              "test",
						"namespace":         "default",
						"creationTimestamp": createPastTimestamp(1000000000),
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &GateWatcher{
				condition: &GateCondition{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       "test",
					Namespace:  "default",
					Expression: tt.expression,
				},
			}

			result, requeueAfter, err := w.evaluateExpression(tt.object, testPod)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.expectedResult, result)

			// Check if time-based expressions return a requeue duration
			if strings.Contains(tt.expression, "now()") && !result {
				assert.Greater(t, requeueAfter, time.Duration(0), "Time-based expressions should return requeue duration when false")
			}
		})
	}
}

func TestGateWatcher_Start(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	gateName := "k8s.ksgate.org/gate"

	// Create a test pod
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			SchedulingGates: []corev1.PodSchedulingGate{
				{
					Name: gateName,
				},
			},
		},
	}

	type fields struct {
		condition    *GateCondition
		podKey       string
		podName      string
		podNamespace string
	}
	tests := []struct {
		name          string
		fields        fields
		object        *unstructured.Unstructured
		condition     *assert.Comparison
		expectedError *error
	}{
		{
			name: "configmap exists",
			fields: fields{
				condition: &GateCondition{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       "test",
					Namespace:  testPod.Namespace,
					Namespaced: true,
				},
				podKey:       fmt.Sprintf("%s/%s", testPod.Namespace, testPod.Name),
				podName:      testPod.Name,
				podNamespace: testPod.Namespace,
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":              "test",
						"namespace":         testPod.Namespace,
						"creationTimestamp": createPastTimestamp(1000000000),
					},
				},
			},
		},
		{
			name: "configmap was created at least 10 seconds before",
			fields: fields{
				condition: &GateCondition{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       "test",
					Namespace:  testPod.Namespace,
					Namespaced: true,
					Expression: `(now() - timestamp(resource.metadata.creationTimestamp)) > duration('10s')`,
				},
				podKey:       fmt.Sprintf("%s/%s", testPod.Namespace, testPod.Name),
				podName:      testPod.Name,
				podNamespace: testPod.Namespace,
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":              "test",
						"namespace":         testPod.Namespace,
						"creationTimestamp": time.Now().Format(time.RFC3339),
					},
				},
			},
		},
		{
			name: "cluster scoped PersistentVolume",
			fields: fields{
				condition: &GateCondition{
					APIVersion: "v1",
					Kind:       "PersistentVolume",
					Name:       "test",
					Namespace:  "",
					Namespaced: false,
				},
				podKey:       fmt.Sprintf("%s/%s", testPod.Namespace, testPod.Name),
				podName:      testPod.Name,
				podNamespace: testPod.Namespace,
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "PersistentVolume",
					"metadata": map[string]interface{}{
						"name": "test",
					},
				},
			},
		},
		{
			name: "resource with odd resource plurality",
			fields: fields{
				condition: &GateCondition{
					APIVersion: "networking.k8s.io/v1",
					Kind:       "Ingress",
					Name:       "test",
					Namespace:  testPod.Namespace,
					Namespaced: true,
				},
				podKey:       fmt.Sprintf("%s/%s", testPod.Namespace, testPod.Name),
				podName:      testPod.Name,
				podNamespace: testPod.Namespace,
			},
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "networking.k8s.io/v1",
					"kind":       "Ingress",
					"metadata": map[string]interface{}{
						"name":      "test",
						"namespace": testPod.Namespace,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(testPod).
				Build()

			clientset := kfake.NewSimpleClientset()
			disc, ok := clientset.Discovery().(*discfake.FakeDiscovery)
			if !ok {
				t.Fatalf("couldn't convert Discovery() to *FakeDiscovery")
			}

			disc.Resources = []*metav1.APIResourceList{
				{
					GroupVersion: "networking.k8s.io/v1",
					APIResources: []metav1.APIResource{
						{
							Name:         "ingresses",
							Kind:         "Ingress",
							Namespaced:   true,
							SingularName: "ingress",
						},
					},
				},
			}

			dyn := dynfake.NewSimpleDynamicClient(scheme, testPod)
			ctx := context.Background()

			w := NewGateWatcher(
				ctx,
				c,
				dyn,
				disc,
				func(gw *GateWatcher) {},
				testPod,
				corev1.PodSchedulingGate{Name: gateName},
				tt.fields.condition,
			)

			w.Start()

			time.Sleep(500 * time.Millisecond)

			_, err := dyn.Resource(
				schema.GroupVersionResource{
					Group:    tt.object.GroupVersionKind().Group,
					Version:  tt.object.GroupVersionKind().Version,
					Resource: w.getResourceName(),
				},
			).Namespace(tt.object.GetNamespace()).Create(ctx, tt.object, metav1.CreateOptions{})

			if tt.expectedError != nil {
				assert.Equal(t, tt.expectedError, err)
				return
			}

			assert.Equal(t, fmt.Sprintf("%s/%s", tt.fields.podKey, gateName), w.GateKey())
			assert.Equal(t, tt.fields.podKey, w.PodKey())

			require.Eventually(
				t, func() bool {
					var updatedPod corev1.Pod
					err := c.Get(ctx, client.ObjectKey{Namespace: testPod.Namespace, Name: testPod.Name}, &updatedPod)
					if err != nil {
						return false
					}
					return len(updatedPod.Spec.SchedulingGates) == 0
				},
				20*time.Second,
				500*time.Millisecond,
			)
		})
	}
}

func createTimestampString(t time.Time) string {
	return t.Format(time.RFC3339)
}

func createPastTimestamp(secondsAgo int) string {
	return createTimestampString(time.Now().Add(-time.Duration(secondsAgo) * time.Second))
}

func createFutureTimestamp(secondsFromNow int) string {
	return createTimestampString(time.Now().Add(time.Duration(secondsFromNow) * time.Second))
}
