package controller

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// func TestPodController_Reconcile(t *testing.T) {
// 	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

// 	scheme := runtime.NewScheme()
// 	_ = clientgoscheme.AddToScheme(scheme)
// 	_ = corev1.AddToScheme(scheme)

// 	tests := []struct {
// 		name             string
// 		existingPod      *corev1.Pod
// 		interceptorFuncs *interceptor.Funcs
// 		expectedError    bool
// 		expectedGates    []corev1.PodSchedulingGate
// 		addResource      bool
// 	}{
// 		{
// 			name:          "pod not found",
// 			existingPod:   nil,
// 			expectedError: false,
// 		},
// 		{
// 			name: "pod not in SchedulingGated state",
// 			existingPod: &corev1.Pod{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Name:      "test-pod",
// 					Namespace: "default",
// 				},
// 				Status: corev1.PodStatus{
// 					Phase: corev1.PodRunning,
// 				},
// 			},
// 			expectedError: false,
// 		},
// 		{
// 			name: "pod with no scheduling gates",
// 			existingPod: &corev1.Pod{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Name:      "test-pod",
// 					Namespace: "default",
// 				},
// 				Status: corev1.PodStatus{
// 					Phase: "SchedulingGated",
// 				},
// 			},
// 			expectedError: false,
// 		},
// 		{
// 			name: "pod with non-matching gate prefix",
// 			existingPod: &corev1.Pod{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Name:      "test-pod",
// 					Namespace: "default",
// 				},
// 				Spec: corev1.PodSpec{
// 					SchedulingGates: []corev1.PodSchedulingGate{
// 						{Name: "other.domain/gate"},
// 					},
// 				},
// 				Status: corev1.PodStatus{
// 					Phase: "SchedulingGated",
// 				},
// 			},
// 			expectedError: false,
// 			expectedGates: []corev1.PodSchedulingGate{
// 				{Name: "other.domain/gate"},
// 			},
// 		},
// 		{
// 			name: "pod with matching gate but no annotation",
// 			existingPod: &corev1.Pod{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Name:      "test-pod",
// 					Namespace: "default",
// 				},
// 				Spec: corev1.PodSpec{
// 					SchedulingGates: []corev1.PodSchedulingGate{
// 						{Name: "k8s.ksgate.org/test-gate"},
// 					},
// 				},
// 				Status: corev1.PodStatus{
// 					Phase: "SchedulingGated",
// 				},
// 			},
// 			expectedError: false,
// 			expectedGates: []corev1.PodSchedulingGate{
// 				{Name: "k8s.ksgate.org/test-gate"},
// 			},
// 		},
// 		{
// 			name: "pod with matching gate and invalid JSON annotation",
// 			existingPod: &corev1.Pod{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Name:      "test-pod",
// 					Namespace: "default",
// 					Annotations: map[string]string{
// 						"k8s.ksgate.org/test-gate": "invalid-json",
// 					},
// 				},
// 				Spec: corev1.PodSpec{
// 					SchedulingGates: []corev1.PodSchedulingGate{
// 						{Name: "k8s.ksgate.org/test-gate"},
// 					},
// 				},
// 				Status: corev1.PodStatus{
// 					Phase: "SchedulingGated",
// 				},
// 			},
// 			expectedError: false,
// 			expectedGates: []corev1.PodSchedulingGate{
// 				{Name: "k8s.ksgate.org/test-gate"},
// 			},
// 		},
// 		{
// 			name: "pod with matching gate and valid condition that evaluates to false",
// 			existingPod: &corev1.Pod{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Name:      "test-pod",
// 					Namespace: "default",
// 					Annotations: map[string]string{
// 						"k8s.ksgate.org/test-gate": `{
// 							"apiVersion":"v1",
// 							"kind":"ConfigMap",
// 							"name":"test",
// 							"namespace":"default"
// 						}`,
// 					},
// 				},
// 				Spec: corev1.PodSpec{
// 					SchedulingGates: []corev1.PodSchedulingGate{
// 						{Name: "k8s.ksgate.org/test-gate"},
// 					},
// 				},
// 				Status: corev1.PodStatus{
// 					Phase: "SchedulingGated",
// 				},
// 			},
// 			expectedError: false,
// 			expectedGates: []corev1.PodSchedulingGate{
// 				{Name: "k8s.ksgate.org/test-gate"},
// 			},
// 		},
// 		{
// 			name: "pod with matching gate and valid condition that evaluates to true",
// 			existingPod: &corev1.Pod{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Name:      "test-pod",
// 					Namespace: "default",
// 					Annotations: map[string]string{
// 						"k8s.ksgate.org/test-gate": `{
// 							"apiVersion":"v1",
// 							"kind":"ConfigMap",
// 							"name":"test",
// 							"namespace":"default"
// 						}`,
// 					},
// 				},
// 				Spec: corev1.PodSpec{
// 					SchedulingGates: []corev1.PodSchedulingGate{
// 						{Name: "k8s.ksgate.org/test-gate"},
// 					},
// 				},
// 				Status: corev1.PodStatus{
// 					Phase: "SchedulingGated",
// 				},
// 			},
// 			expectedError: false,
// 			expectedGates: nil,
// 			addResource:   true,
// 		},
// 		{
// 			name: "pod with multiple gates - only remove matching gate when condition is true",
// 			existingPod: &corev1.Pod{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Name:      "test-pod",
// 					Namespace: "default",
// 					Annotations: map[string]string{
// 						"k8s.ksgate.org/test-gate": `{
// 							"apiVersion":"v1",
// 							"kind":"ConfigMap",
// 							"name":"test",
// 							"namespace":"default"
// 						}`,
// 					},
// 				},
// 				Spec: corev1.PodSpec{
// 					SchedulingGates: []corev1.PodSchedulingGate{
// 						{Name: "other.domain/gate"},
// 						{Name: "k8s.ksgate.org/test-gate"},
// 						{Name: "another.domain/gate"},
// 					},
// 				},
// 				Status: corev1.PodStatus{
// 					Phase: "SchedulingGated",
// 				},
// 			},
// 			expectedError: false,
// 			expectedGates: []corev1.PodSchedulingGate{
// 				{Name: "other.domain/gate"},
// 				{Name: "another.domain/gate"},
// 			},
// 			addResource: true,
// 		},
// 		{
// 			name: "pod update fails",
// 			existingPod: &corev1.Pod{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Name:      "test-pod",
// 					Namespace: "default",
// 					Annotations: map[string]string{
// 						"k8s.ksgate.org/test-gate": `{
// 							"apiVersion":"v1",
// 							"kind":"ConfigMap",
// 							"name":"test",
// 							"namespace":"default"
// 						}`,
// 					},
// 				},
// 				Spec: corev1.PodSpec{
// 					SchedulingGates: []corev1.PodSchedulingGate{
// 						{Name: "k8s.ksgate.org/test-gate"},
// 					},
// 				},
// 				Status: corev1.PodStatus{
// 					Phase: "SchedulingGated",
// 				},
// 			},
// 			expectedError: true,
// 			expectedGates: []corev1.PodSchedulingGate{
// 				{Name: "k8s.ksgate.org/test-gate"},
// 			},
// 			interceptorFuncs: &interceptor.Funcs{
// 				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
// 					return errors.New("update failed")
// 				},
// 			},
// 			addResource: true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			// Create fake client builder
// 			builder := fake.NewClientBuilder().WithScheme(scheme)

// 			// Only add the pod if it exists
// 			if tt.existingPod != nil {
// 				builder = builder.WithObjects(tt.existingPod)
// 			}

// 			// Add ConfigMap for tests that need it
// 			if tt.addResource {
// 				builder = builder.WithObjects(&corev1.ConfigMap{
// 					ObjectMeta: metav1.ObjectMeta{
// 						Name:      "test",
// 						Namespace: "default",
// 					},
// 				})
// 			}

// 			if tt.interceptorFuncs != nil {
// 				builder.WithInterceptorFuncs(
// 					*tt.interceptorFuncs,
// 				)
// 			}

// 			// Build the client
// 			client := builder.Build()

// 			// Create controller
// 			r := &PodController{
// 				Client:  client,
// 				Scheme:  scheme,
// 				Logger:  ctrl.Log.WithName("test"),
// 				Dynamic: dynamic.NewForConfigOrDie(&rest.Config{}),
// 			}

// 			// Create request
// 			req := ctrl.Request{
// 				NamespacedName: types.NamespacedName{
// 					Name:      "test-pod",
// 					Namespace: "default",
// 				},
// 			}

// 			// Run reconcile
// 			_, err := r.Reconcile(context.Background(), req)

// 			// Check error
// 			if tt.expectedError {
// 				assert.Error(t, err)
// 			} else {
// 				assert.NoError(t, err)
// 			}

// 			// If pod should exist, verify its gates
// 			if tt.existingPod != nil {
// 				var pod corev1.Pod
// 				err = client.Get(context.Background(), req.NamespacedName, &pod)
// 				assert.NoError(t, err)
// 				assert.Equal(t, tt.expectedGates, pod.Spec.SchedulingGates)
// 			}
// 		})
// 	}
// }

func TestPodController_SetupWithManager(t *testing.T) {
	type fields struct {
		Client client.Client
		Scheme *runtime.Scheme
	}
	type args struct {
		mgr ctrl.Manager
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "nil manager",
			fields: fields{
				Client: nil,
				Scheme: nil,
			},
			args: args{
				mgr: nil,
			},
			wantErr: true,
		},
		{
			name: "manager",
			fields: fields{
				Client: nil,
				Scheme: nil,
			},
			args: args{
				mgr: func() manager.Manager {
					m, _ := ctrl.NewManager(&rest.Config{}, manager.Options{})
					return m
				}(),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &PodController{
				Client: tt.fields.Client,
				Scheme: tt.fields.Scheme,
			}
			if err := r.SetupWithManager(tt.args.mgr); (err != nil) != tt.wantErr {
				t.Errorf("PodController.SetupWithManager() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPodController_podToRequests(t *testing.T) {
	type fields struct {
		Client client.Client
		Scheme *runtime.Scheme
	}
	type args struct {
		ctx context.Context
		obj client.Object
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []reconcile.Request
	}{
		// TODO: Add test cases.
		{
			name: "pod not pending",
			fields: fields{
				Client: nil,
				Scheme: nil,
			},
			args: args{
				ctx: context.Background(),
				obj: &corev1.Pod{},
			},
		},
		{
			name: "pod is pending",
			args: args{
				ctx: context.Background(),
				obj: &corev1.Pod{
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
					},
				},
			},
		},
		{
			name: "pod is scheduled but has no scheduling gates",
			args: args{
				ctx: context.Background(),
				obj: &corev1.Pod{
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.PodScheduled,
								Reason: "Foo",
							},
						},
					},
				},
			},
		},
		{
			name: "pod is scheduled and has scheduling gates",
			args: args{
				ctx: context.Background(),
				obj: &corev1.Pod{
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.PodScheduled,
								Reason: "SchedulingGated",
							},
						},
					},
					Spec: corev1.PodSpec{
						SchedulingGates: []corev1.PodSchedulingGate{
							{
								Name: "foo",
							},
						},
					},
				},
			},
		},
		{
			name: "pod has scheduling gate with our prefix",
			args: args{
				ctx: context.Background(),
				obj: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.PodScheduled,
								Reason: "SchedulingGated",
							},
						},
					},
					Spec: corev1.PodSpec{
						SchedulingGates: []corev1.PodSchedulingGate{
							{
								Name: "k8s.ksgate.org/foo",
							},
						},
					},
				},
			},
			want: []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Name:      "test-pod",
						Namespace: "default",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &PodController{
				Client: tt.fields.Client,
				Scheme: tt.fields.Scheme,
			}
			if got := r.podToRequests(tt.args.ctx, tt.args.obj); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PodController.podToRequests() = %v, want %v", got, tt.want)
			}
		})
	}
}
