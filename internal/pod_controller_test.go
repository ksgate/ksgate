package internal

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestPodController_Reconcile(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name             string
		existingPod      *corev1.Pod
		interceptorFuncs *interceptor.Funcs
		expectedError    bool
		expectedGates    []corev1.PodSchedulingGate
		addResource      bool
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
		{
			name: "pod update fails",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"gateman.kdex.dev/test-gate": `{
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
			expectedError: true,
			expectedGates: []corev1.PodSchedulingGate{
				{Name: "gateman.kdex.dev/test-gate"},
			},
			interceptorFuncs: &interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return errors.New("update failed")
				},
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

			if tt.interceptorFuncs != nil {
				builder.WithInterceptorFuncs(
					*tt.interceptorFuncs,
				)
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

func TestPodController_evaluateGate(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)

	tests := []struct {
		name           string
		gate           corev1.PodSchedulingGate
		pod            *corev1.Pod
		objects        []runtime.Object
		expectedResult bool
	}{
		{
			name: "gate with no condition",
			gate: corev1.PodSchedulingGate{
				Name: "gateman.kdex.dev/test-gate",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			expectedResult: false,
		},
		{
			name: "gate with invalid condition JSON",
			gate: corev1.PodSchedulingGate{
				Name: "gateman.kdex.dev/test-gate",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"gateman.kdex.dev/test-gate": "{invalid json",
					},
				},
			},
			expectedResult: false,
		},
		{
			name: "gate with valid condition that evaluates to false",
			gate: corev1.PodSchedulingGate{
				Name: "gateman.kdex.dev/test-gate",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"gateman.kdex.dev/test-gate": `{
							"apiVersion":"v1",
							"kind":"ConfigMap",
							"name":"test",
							"expression":"false"
						}`,
					},
				},
			},
			expectedResult: false,
		},
		{
			name: "gate with valid condition that evaluates to true",
			gate: corev1.PodSchedulingGate{
				Name: "gateman.kdex.dev/test-gate",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"gateman.kdex.dev/test-gate": `{
							"apiVersion":"v1",
							"kind":"ConfigMap",
							"name":"test",
							"expression":"true"
						}`,
					},
				},
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
				},
			},
			expectedResult: true,
		},
		{
			name: "gate with valid condition on missing resource property",
			gate: corev1.PodSchedulingGate{
				Name: "gateman.kdex.dev/test-gate",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"gateman.kdex.dev/test-gate": `{
							"apiVersion":"v1",
							"kind":"ConfigMap",
							"name":"test",
							"expression":"resource.foo == 'bar'"
						}`,
					},
				},
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
				},
			},
			expectedResult: false,
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

			got := r.evaluateGate(context.Background(), tt.pod, tt.gate)
			if got != tt.expectedResult {
				t.Errorf("PodController.evaluateGate() = %v, want %v", got, tt.expectedResult)
			}
		})
	}
}

func TestPodController_evaluateCondition(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

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
		{
			name:           "missing condition",
			condition:      nil,
			expectedResult: false,
			expectedError:  true,
		},
		{
			name:           "resourceExists - missing required fields",
			condition:      map[string]interface{}{},
			expectedResult: false,
			expectedError:  true,
		},
		{
			name: "resourceExists - valid fields",
			condition: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test",
				"namespace":  "default",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
				},
			},
			expectedResult: true,
			expectedError:  false,
		},
		{
			name: "expression - missing required fields",
			condition: map[string]interface{}{
				"expression": "this is not a valid expression",
			},
			expectedResult: false,
			expectedError:  true,
		},
		{
			name: "expression - invalid expression",
			condition: map[string]interface{}{
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

func TestPodController_evaluateExpression(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

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
		expression     interface{}
		objects        []runtime.Object
		expectedResult bool
		expectedError  bool
	}{
		{
			name:           "1 missing expression",
			condition:      map[string]interface{}{},
			expectedResult: false,
			expectedError:  true,
		},
		{
			name: "2 resource missing",
			condition: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test",
				"namespace":  "default",
			},
			expression:     "true",
			expectedResult: false,
			expectedError:  false,
		},
		{
			name: "3 expression does not evaluate to boolean",
			condition: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test",
				"namespace":  "default",
			},
			expression: "45",
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
				},
			},
			expectedResult: false,
			expectedError:  true,
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

			result, err := r.evaluateExpression(context.Background(), testPod, tt.condition, tt.expression)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.expectedResult, result)
		})
	}
}
