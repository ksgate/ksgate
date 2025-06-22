package watcher

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(testPod).
				Build()

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

func createTimestampString(t time.Time) string {
	return t.Format(time.RFC3339)
}

func createPastTimestamp(secondsAgo int) string {
	return time.Now().Add(-time.Duration(secondsAgo) * time.Second).Format(time.RFC3339)
}

func createFutureTimestamp(secondsFromNow int) string {
	return time.Now().Add(time.Duration(secondsFromNow) * time.Second).Format(time.RFC3339)
}
