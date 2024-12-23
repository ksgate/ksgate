package controllers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPodController_resourceLookup(t *testing.T) {
	tests := []struct {
		name      string
		condition map[string]interface{}
		objects   []runtime.Object
		wantErr   bool
	}{
		{
			name: "successful configmap lookup",
			condition: map[string]interface{}{
				"type":       "resourceExists",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
					Data: map[string]string{"key": "value"},
				},
			},
			wantErr: false,
		},
		{
			name: "resource not found",
			condition: map[string]interface{}{
				"type":       "resourceExists",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "nonexistent",
				"namespace":  "default",
			},
			objects: []runtime.Object{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			r := &PodController{
				Client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tt.objects...).Build(),
				Scheme: scheme,
			}

			_, err := r.resourceLookup(context.Background(), tt.condition)
			if (err != nil) != tt.wantErr {
				t.Errorf("resourceLookup() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPodController_evaluateExpression(t *testing.T) {
	tests := []struct {
		name      string
		condition map[string]interface{}
		pod       *corev1.Pod
		objects   []runtime.Object
		want      bool
		wantErr   bool
	}{
		{
			name: "simple true expression",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "resource.data.key == 'value'",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
					Data: map[string]string{"key": "value"},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "expression with pod reference",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "pod.metadata.name == 'test-pod' && resource.data.key == 'value'",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cm",
						Namespace: "default",
					},
					Data: map[string]string{"key": "value"},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "invalid expression",
			condition: map[string]interface{}{
				"type":       "expression",
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "test-cm",
				"namespace":  "default",
				"expression": "invalid expression",
			},
			pod:     &corev1.Pod{},
			objects: []runtime.Object{},
			want:    false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			r := &PodController{
				Client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tt.objects...).Build(),
				Scheme: scheme,
			}

			got, err := r.evaluateExpression(context.Background(), tt.condition, tt.pod)
			if (err != nil) != tt.wantErr {
				t.Errorf("evaluateExpression() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("evaluateExpression() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPodController_Reconcile(t *testing.T) {
	tests := []struct {
		name    string
		pod     *corev1.Pod
		objects []runtime.Object
		wantErr bool
	}{
		{
			name: "pod with no conditions",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			objects: []runtime.Object{},
			wantErr: false,
		},
		// Add more test cases based on your Reconcile logic
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			allObjects := append(tt.objects, tt.pod)
			r := &PodController{
				Client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(allObjects...).Build(),
				Scheme: scheme,
			}

			_, err := r.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.pod.Name,
					Namespace: tt.pod.Namespace,
				},
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
