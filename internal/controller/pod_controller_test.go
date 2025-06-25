package controller

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	discfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	kfake "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPodController_Reconcile(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name                     string
		existingPod              *corev1.Pod
		interceptorFuncs         *interceptor.Funcs
		expectedError            bool
		addResource              bool
		numberOfWatchersExpected int
	}{
		{
			name:                     "pod not found",
			existingPod:              nil,
			expectedError:            false,
			numberOfWatchersExpected: 0,
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
			expectedError:            false,
			numberOfWatchersExpected: 0,
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
			expectedError:            false,
			numberOfWatchersExpected: 0,
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
			expectedError:            false,
			numberOfWatchersExpected: 0,
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
						{Name: "k8s.ksgate.org/test-gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError:            false,
			numberOfWatchersExpected: 0,
		},
		{
			name: "pod with matching gate and invalid JSON annotation",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"k8s.ksgate.org/test-gate": "invalid-json",
					},
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "k8s.ksgate.org/test-gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError:            false,
			numberOfWatchersExpected: 0,
		},
		{
			name: "pod with matching gate and valid condition that evaluates to false",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"k8s.ksgate.org/test-gate": `{
							"apiVersion":"v1",
							"kind":"ConfigMap",
							"name":"test",
							"namespace":"default"
						}`,
					},
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "k8s.ksgate.org/test-gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError:            false,
			numberOfWatchersExpected: 1,
		},
		{
			name: "pod with matching gate and valid condition that evaluates to true",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"k8s.ksgate.org/test-gate": `{
							"apiVersion":"v1",
							"kind":"ConfigMap",
							"name":"test",
							"namespace":"default"
						}`,
					},
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "k8s.ksgate.org/test-gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError:            false,
			addResource:              true,
			numberOfWatchersExpected: 1,
		},
		{
			name: "pod with multiple gates",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"k8s.ksgate.org/test-gate": `{
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
						{Name: "k8s.ksgate.org/test-gate"},
						{Name: "another.domain/gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError:            false,
			addResource:              true,
			numberOfWatchersExpected: 1,
		},
		{
			name: "pod with cluster scoped gate",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"k8s.ksgate.org/test-gate": `{
							"apiVersion":"v1",
							"kind":"PersistentVolume",
							"name":"test",
							"namespaced":false
						}`,
					},
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "other.domain/gate"},
						{Name: "k8s.ksgate.org/test-gate"},
						{Name: "another.domain/gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError:            false,
			addResource:              true,
			numberOfWatchersExpected: 1,
		},
		{
			name: "pod with invalid condition",
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"k8s.ksgate.org/test-gate": `{
						}`,
					},
				},
				Spec: corev1.PodSpec{
					SchedulingGates: []corev1.PodSchedulingGate{
						{Name: "other.domain/gate"},
						{Name: "k8s.ksgate.org/test-gate"},
						{Name: "another.domain/gate"},
					},
				},
				Status: corev1.PodStatus{
					Phase: "SchedulingGated",
				},
			},
			expectedError:            false,
			addResource:              true,
			numberOfWatchersExpected: 0,
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

			clientset := kfake.NewSimpleClientset()
			disc, ok := clientset.Discovery().(*discfake.FakeDiscovery)
			if !ok {
				t.Fatalf("couldn't convert Discovery() to *FakeDiscovery")
			}

			// Build the client
			client := builder.Build()

			// Create controller
			r := &PodController{
				Client:    client,
				Dynamic:   dynamic.NewForConfigOrDie(&rest.Config{}),
				Discovery: disc,
				Scheme:    scheme,
				Logger:    ctrl.Log.WithName("test"),
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

			assert.Equal(t, tt.numberOfWatchersExpected, len(r.GetWatcherStats()))

			if tt.existingPod != nil {
				podKey := fmt.Sprintf("%s/%s", tt.existingPod.Namespace, tt.existingPod.Name)
				r.cleanupGateWatchers(podKey)
				assert.Equal(t, 0, len(r.GetWatcherStats()))
			}
		})
	}
}

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
