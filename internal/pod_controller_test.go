package internal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPodController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name          string
		existingPod   *corev1.Pod
		expectedError bool
		expectedGates []corev1.PodSchedulingGate
		addResource   bool
	}{
		{
			name:          "pod not found",
			existingPod:   nil,
			expectedError: false,
		},
		{
			name: "pod not in SchedulingGated state",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			expectedError: false,
		},
		{
			name: "pod with no scheduling gates",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError: false,
		},
		{
			name: "pod with non-matching gate prefix",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "other.domain/gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError: false,
			expectedGates: []corev1.PodSchedulingGate{
				{Name: "other.domain/gate"},
			},
		},
		{
			name: "pod with matching gate but no annotation",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "gateman.kdex.dev/test-gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError: false,
			expectedGates: []corev1.PodSchedulingGate{
				{Name: "gateman.kdex.dev/test-gate"},
			},
		},
		{
			name: "pod with matching gate and invalid JSON annotation",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"gateman.kdex.dev/test-gate": "invalid-json",
					},
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "gateman.kdex.dev/test-gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError: false,
			expectedGates: []corev1.PodSchedulingGate{
				{Name: "gateman.kdex.dev/test-gate"},
			},
		},
		{
			name: "pod with matching gate and valid condition that evaluates to false",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"gateman.kdex.dev/test-gate": `{
							"type":"resourceExists",
							"apiVersion":"v1",
							"kind":"ConfigMap",
							"name":"test",
							"namespace":"default"
						}`,
					},
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "gateman.kdex.dev/test-gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError: false,
			expectedGates: []corev1.PodSchedulingGate{
				{Name: "gateman.kdex.dev/test-gate"},
			},
		},
		{
			name: "pod with matching gate and valid condition that evaluates to true",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"gateman.kdex.dev/test-gate": `{
							"type":"resourceExists",
							"apiVersion":"v1",
							"kind":"ConfigMap",
							"name":"test",
							"namespace":"default"
						}`,
					},
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "gateman.kdex.dev/test-gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError: false,
			expectedGates: nil,
			addResource:   true,
		},
		{
			name: "pod with multiple gates - only remove matching gate when condition is true",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"gateman.kdex.dev/test-gate": `{
							"type":"resourceExists",
							"apiVersion":"v1",
							"kind":"ConfigMap",
							"name":"test",
							"namespace":"default"
						}`,
					},
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "other.domain/gate"},
						{Name: "gateman.kdex.dev/test-gate"},
						{Name: "another.domain/gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError: false,
			expectedGates: []corev1.PodSchedulingGate{
				{Name: "other.domain/gate"},
				{Name: "another.domain/gate"},
			},
			addResource: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client builder
			builder := fake.NewClientBuilder().WithScheme(scheme)

			// Only add the pod if it exists
			if tt.existingPod != nil {
				builder = builder.WithObjects(tt.existingPod)
			}

			// Add ConfigMap for tests that need it
			if tt.addResource {
				builder = builder.WithObjects(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
				})
			}

			// Build the client
			client := builder.Build()

			// Create controller
			r := &PodController{
				Client: client,
				Scheme: scheme,
			}

			// Create request
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-pod",
					Namespace: "default",
				},
			}

			// Run reconcile
			_, err := r.Reconcile(context.Background(), req)

			// Check error
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// If pod should exist, verify its gates
			if tt.existingPod != nil {
				var pod corev1.Pod
				err = client.Get(context.Background(), req.NamespacedName, &pod)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedGates, pod.Spec.SchedulingGates)
			}
		})
	}
}

func TestEvaluateCondition(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)

	// Create a test pod
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	tests := []struct {
		name           string
		condition      map[string]interface{}
		objects        []runtime.Object
		expectedResult bool
		expectedError  bool
	}{
		// {
		// 	name:           "missing type field",
		// 	condition:      map[string]interface{}{},
		// 	expectedResult: false,
		// 	expectedError:  true,
		// },
		// {
		// 	name: "unknown condition type",
		// 	condition: map[string]interface{}{
		// 		"type": "unknown",
		// 	},
		// 	expectedResult: false,
		// 	expectedError:  true,
		// },
		// {
		// 	name: "resourceExists - missing required fields",
		// 	condition: map[string]interface{}{
		// 		"type": "resourceExists",
		// 	},
		// 	expectedResult: false,
		// 	expectedError:  true,
		// },
		// {
		// 	name: "resourceExists - valid fields",
		// 	condition: map[string]interface{}{
		// 		"type":       "resourceExists",
		// 		"apiVersion": "v1",
		// 		"kind":       "ConfigMap",
		// 		"name":       "test",
		// 		"namespace":  "default",
		// 	},
		// 	objects: []runtime.Object{
		// 		&corev1.ConfigMap{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      "test",
		// 				Namespace: "default",
		// 			},
		// 		},
		// 	},
		// 	expectedResult: true,
		// 	expectedError:  false,
		// },
		// {
		// 	name: "labelExists - missing required fields",
		// 	condition: map[string]interface{}{
		// 		"type": "labelExists",
		// 	},
		// 	expectedResult: false,
		// 	expectedError:  true,
		// },
		// {
		// 	name: "labelExists - resource not found",
		// 	condition: map[string]interface{}{
		// 		"type":       "labelExists",
		// 		"apiVersion": "v1",
		// 		"kind":       "ConfigMap",
		// 		"name":       "test-cm",
		// 		"namespace":  "default",
		// 		"label":      "my-label",
		// 	},
		// 	expectedResult: false,
		// 	expectedError:  false,
		// },
		// {
		// 	name: "labelExists - label not present",
		// 	condition: map[string]interface{}{
		// 		"type":       "labelExists",
		// 		"apiVersion": "v1",
		// 		"kind":       "ConfigMap",
		// 		"name":       "test-cm",
		// 		"namespace":  "default",
		// 		"label":      "my-label",
		// 		"value":      "any-value",
		// 	},
		// 	objects: []runtime.Object{
		// 		&corev1.ConfigMap{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      "test-cm",
		// 				Namespace: "default",
		// 				Labels: map[string]string{
		// 					"other-label": "value",
		// 				},
		// 			},
		// 		},
		// 	},
		// 	expectedResult: false,
		// 	expectedError:  false,
		// },
		// {
		// 	name: "labelExists - label present",
		// 	condition: map[string]interface{}{
		// 		"type":       "labelExists",
		// 		"apiVersion": "v1",
		// 		"kind":       "ConfigMap",
		// 		"name":       "test-cm",
		// 		"namespace":  "default",
		// 		"label":      "my-label",
		// 		"value":      "any-value",
		// 	},
		// 	objects: []runtime.Object{
		// 		&corev1.ConfigMap{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      "test-cm",
		// 				Namespace: "default",
		// 				Labels: map[string]string{
		// 					"my-label": "any-value",
		// 				},
		// 			},
		// 		},
		// 	},
		// 	expectedResult: true,
		// 	expectedError:  false,
		// },
		// {
		// 	name: "labelExists - label present with specific value match",
		// 	condition: map[string]interface{}{
		// 		"type":       "labelExists",
		// 		"apiVersion": "v1",
		// 		"kind":       "ConfigMap",
		// 		"name":       "test-cm",
		// 		"namespace":  "default",
		// 		"label":      "my-label",
		// 		"value":      "expected-value",
		// 	},
		// 	objects: []runtime.Object{
		// 		&corev1.ConfigMap{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      "test-cm",
		// 				Namespace: "default",
		// 				Labels: map[string]string{
		// 					"my-label": "expected-value",
		// 				},
		// 			},
		// 		},
		// 	},
		// 	expectedResult: true,
		// 	expectedError:  false,
		// },
		// {
		// 	name: "labelExists - label present but value mismatch",
		// 	condition: map[string]interface{}{
		// 		"type":       "labelExists",
		// 		"apiVersion": "v1",
		// 		"kind":       "ConfigMap",
		// 		"name":       "test-cm",
		// 		"namespace":  "default",
		// 		"label":      "my-label",
		// 		"value":      "expected-value",
		// 	},
		// 	objects: []runtime.Object{
		// 		&corev1.ConfigMap{
		// 			ObjectMeta: metav1.ObjectMeta{
		// 				Name:      "test-cm",
		// 				Namespace: "default",
		// 				Labels: map[string]string{
		// 					"my-label": "different-value",
		// 				},
		// 			},
		// 		},
		// 	},
		// 	expectedResult: false,
		// 	expectedError:  false,
		// },
		{
			name: "expression - missing required fields",
			condition: map[string]interface{}{
				"type": "expression",
			},
			expectedResult: false,
			expectedError:  true,
		},
		{
			name: "expression - invalid expression",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "invalid && syntax ||",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
				},
			},
			expectedResult: false,
			expectedError:  true,
		},
		{
			name: "expression - simple true condition",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "true",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
		{
			name: "expression - simple false condition",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "false",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
				},
			},
			expectedResult: false,
			expectedError:  false,
		},
		{
			name: "expression - complex boolean expression (true)",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "true && (true || false)",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
		{
			name: "expression - complex boolean expression (false)",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "true && false",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
				},
			},
			expectedResult: false,
			expectedError:  false,
		},
		{
			name: "expression - with pod variables",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "pod.metadata.name == 'test-pod'",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
		{
			name: "expression - with pod variables (false case)",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "pod.metadata.name == 'different-pod'",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
				},
			},
			expectedResult: false,
			expectedError:  false,
		},
		{
			name: "expression - with complex pod variable access",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "pod.metadata.namespace == 'default' && pod.metadata.name == 'test-pod'",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
		{
			name: "expression - with target resource fields",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "resource.metadata.name == 'test-cm' && resource.metadata.namespace == 'default'",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tt.objects...).
				Build()

			r := &PodController{
				Client: client,
				Scheme: scheme,
			}

			result, err := r.evaluateCondition(context.Background(), testPod, tt.condition)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}
