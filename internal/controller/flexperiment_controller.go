package controller

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mlv1 "github.com/yourusername/fl-operator/api/v1"
)

// FLExperimentReconciler reconciles a FLExperiment object
type FLExperimentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ml.example.com,resources=flexperiments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ml.example.com,resources=flexperiments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ml.example.com,resources=flexperiments/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *FLExperimentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. 获取 FLExperiment 资源
	var exp mlv1.FLExperiment
	if err := r.Get(ctx, req.NamespacedName, &exp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. 如果已完成，跳过
	if exp.Status.Phase == "Completed" {
		return ctrl.Result{}, nil
	}

	// 3. 创建 Aggregator Pod
	aggName := exp.Name + "-aggregator"
	aggPod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: aggName, Namespace: exp.Namespace}, aggPod)

	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating Aggregator Pod", "name", aggName)

		aggPod = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      aggName,
				Namespace: exp.Namespace,
				Labels:    map[string]string{"app": aggName, "role": "aggregator"},
			},
			Spec: corev1.PodSpec{
				RestartPolicy: "OnFailure",
				Containers: []corev1.Container{
					{
						Name:      "aggregator",
						Image:     exp.Spec.Image,
						Resources: exp.Spec.Resources,
						Ports: []corev1.ContainerPort{
							{ContainerPort: 8080, Name: "http"},
						},
						Env: []corev1.EnvVar{
							{Name: "ROLE", Value: "aggregator"},
							{Name: "ROUNDS", Value: strconv.Itoa(int(exp.Spec.Rounds))},
							{Name: "TRAINER_COUNT", Value: strconv.Itoa(int(exp.Spec.TrainerReplicas))},
						},
					},
				},
			},
		}

		if err := ctrl.SetControllerReference(&exp, aggPod, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, aggPod); err != nil {
			return ctrl.Result{}, err
		}

		exp.Status.AggregatorPod = aggName
		exp.Status.Phase = "Creating"
		if err := r.Status().Update(ctx, &exp); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	}

	// 4. 创建 Aggregator Service
	aggSvcName := aggName
	aggSvc := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: aggSvcName, Namespace: exp.Namespace}, aggSvc)

	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating Aggregator Service", "name", aggSvcName)

		aggSvc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      aggSvcName,
				Namespace: exp.Namespace,
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": aggName},
				Ports: []corev1.ServicePort{
					{
						Port:       8080,
						TargetPort: intstr.FromInt(8080),
						Name:       "http",
					},
				},
			},
		}

		if err := ctrl.SetControllerReference(&exp, aggSvc, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, aggSvc); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	}

	// 5. 创建 Trainer Pods
	trainerPods := []string{}
	for i := 0; i < int(exp.Spec.TrainerReplicas); i++ {
		trainerName := fmt.Sprintf("%s-trainer-%d", exp.Name, i)
		trainerPods = append(trainerPods, trainerName)

		trainerPod := &corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{Name: trainerName, Namespace: exp.Namespace}, trainerPod)

		if err != nil && errors.IsNotFound(err) {
			logger.Info("Creating Trainer Pod", "name", trainerName, "rank", i)

			trainerPod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      trainerName,
					Namespace: exp.Namespace,
					Labels:    map[string]string{"app": exp.Name + "-trainer", "role": "trainer"},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: "OnFailure",
					Containers: []corev1.Container{
						{
							Name:      "trainer",
							Image:     exp.Spec.Image,
							Resources: exp.Spec.Resources,
							Env: []corev1.EnvVar{
								{Name: "ROLE", Value: "trainer"},
								{Name: "RANK", Value: strconv.Itoa(i)},
								{Name: "AGGREGATOR_ADDR", Value: aggSvcName},
								{Name: "ROUNDS", Value: strconv.Itoa(int(exp.Spec.Rounds))},
							},
						},
					},
				},
			}

			if err := ctrl.SetControllerReference(&exp, trainerPod, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}

			if err := r.Create(ctx, trainerPod); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// 6. 更新状态
	exp.Status.TrainerPods = trainerPods
	exp.Status.Phase = "Running"
	if err := r.Status().Update(ctx, &exp); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Reconciliation complete", "phase", exp.Status.Phase)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FLExperimentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mlv1.FLExperiment{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
