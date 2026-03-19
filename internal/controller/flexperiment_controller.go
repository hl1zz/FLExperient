package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

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

	// 固定的名字规则
	masterName := exp.Name + "-master"
	masterSvcName := masterName

	logger.Info("Reconciling FLExperiment", "phase", exp.Status.Phase)

	switch exp.Status.Phase {

	// ========== 阶段 1: 创建 Master Service + Pod ==========
	case "", "Pending":
		return r.reconcileCreatingMaster(ctx, &exp, masterName, masterSvcName)

	// ========== 阶段 2: 等待 Master Pod Running ==========
	case "CreatingMaster":
		return r.reconcileWaitMasterRunning(ctx, &exp, masterName)

	// ========== 阶段 3: Master Running，创建 Workers ==========
	case "MasterRunning":
		return r.reconcileCreatingWorkers(ctx, &exp, masterSvcName)

	// ========== 阶段 4: 等所有 Worker Running ==========
	case "CreatingWorkers":
		return r.reconcileWaitWorkersRunning(ctx, &exp)

	// ========== 阶段 5: 训练中，监控状态 ==========
	case "Training":
		return r.reconcileTraining(ctx, &exp, masterName)

	// ========== 终态 ==========
	case "Completed":
		logger.Info("Experiment already completed")
		return ctrl.Result{}, nil
	case "Failed":
		logger.Info("Experiment failed")
		return ctrl.Result{}, nil

	default:
		logger.Info("Unknown phase, resetting to Pending", "phase", exp.Status.Phase)
		exp.Status.Phase = "Pending"
		if err := r.Status().Update(ctx, &exp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
}

// ========== 阶段 1: 创建 Master ==========
func (r *FLExperimentReconciler) reconcileCreatingMaster(ctx context.Context, exp *mlv1.FLExperiment, masterName, masterSvcName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1.1 创建 Master Service
	masterSvc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: masterSvcName, Namespace: exp.Namespace}, masterSvc)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating Master Service", "name", masterSvcName)

		masterSvc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      masterSvcName,
				Namespace: exp.Namespace,
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": masterName, "role": "master"},
				Ports: []corev1.ServicePort{
					{Port: 8080, TargetPort: intstr.FromInt(8080), Name: "http"},
				},
			},
		}
		if err := ctrl.SetControllerReference(exp, masterSvc, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, masterSvc); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// 1.2 创建 Master Pod
	masterPod := &corev1.Pod{}
	err = r.Get(ctx, types.NamespacedName{Name: masterName, Namespace: exp.Namespace}, masterPod)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating Master Pod", "name", masterName)

		masterPod = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      masterName,
				Namespace: exp.Namespace,
				Labels:    map[string]string{"app": masterName, "role": "master"},
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyOnFailure,
				Containers: []corev1.Container{
					{
						Name:      "master",
						Image:     exp.Spec.Image,
						Resources: exp.Spec.Resources,
						Ports: []corev1.ContainerPort{
							{ContainerPort: 8080, Name: "http"},
						},
						Env: []corev1.EnvVar{
							{Name: "ROLE", Value: "master"},
							{Name: "ROUNDS", Value: strconv.Itoa(int(exp.Spec.Rounds))},
							{Name: "WORKER_COUNT", Value: strconv.Itoa(int(exp.Spec.WorkerReplicas))},
						},
					},
				},
			},
		}
		if err := ctrl.SetControllerReference(exp, masterPod, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, masterPod); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// 1.3 更新 Status → CreatingMaster
	exp.Status.MasterPod = masterName
	exp.Status.Phase = "CreatingMaster"
	if err := r.Status().Update(ctx, exp); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Master resources created, waiting for Master to be Running")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// ========== 阶段 2: 等待 Master Running ==========
func (r *FLExperimentReconciler) reconcileWaitMasterRunning(ctx context.Context, exp *mlv1.FLExperiment, masterName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	masterPod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: masterName, Namespace: exp.Namespace}, masterPod); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Master Pod not found, resetting to Pending")
			exp.Status.Phase = "Pending"
			r.Status().Update(ctx, exp)
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	switch masterPod.Status.Phase {
	case corev1.PodRunning:
		logger.Info("Master Pod is Running, proceeding to create Workers")
		exp.Status.Phase = "MasterRunning"
		if err := r.Status().Update(ctx, exp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case corev1.PodFailed:
		logger.Info("Master Pod failed", "reason", masterPod.Status.Reason)
		exp.Status.Phase = "Failed"
		r.Status().Update(ctx, exp)
		return ctrl.Result{}, nil

	default:
		logger.Info("Waiting for Master Pod to be Running", "current_phase", masterPod.Status.Phase)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
}

// ========== 阶段 3: 创建 Workers ==========
func (r *FLExperimentReconciler) reconcileCreatingWorkers(ctx context.Context, exp *mlv1.FLExperiment, masterSvcName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 3.1 创建 Headless Service（一个 Service 覆盖所有 Worker）
	workerSvcName := exp.Name + "-workers"
	workerSvc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: workerSvcName, Namespace: exp.Namespace}, workerSvc)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating Headless Worker Service", "name", workerSvcName)

		workerSvc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      workerSvcName,
				Namespace: exp.Namespace,
			},
			Spec: corev1.ServiceSpec{
				ClusterIP: "None", // Headless Service
				Selector:  map[string]string{"app": exp.Name + "-worker"},
				Ports: []corev1.ServicePort{
					{Port: 8081, TargetPort: intstr.FromInt(8081), Name: "http"},
				},
			},
		}
		if err := ctrl.SetControllerReference(exp, workerSvc, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, workerSvc); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// 3.2 创建所有 Worker Pods
	workerPods := []string{}
	masterAddr := fmt.Sprintf("%s:8080", masterSvcName)

	for i := 0; i < int(exp.Spec.WorkerReplicas); i++ {
		workerName := fmt.Sprintf("%s-worker-%d", exp.Name, i)
		workerPods = append(workerPods, workerName)

		workerPod := &corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{Name: workerName, Namespace: exp.Namespace}, workerPod)
		if err != nil && errors.IsNotFound(err) {
			logger.Info("Creating Worker Pod", "name", workerName, "rank", i)

			workerPod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      workerName,
					Namespace: exp.Namespace,
					Labels: map[string]string{
						"app":  exp.Name + "-worker",
						"role": "worker",
						"rank": strconv.Itoa(i),
					},
				},
				Spec: corev1.PodSpec{
					Hostname:      workerName,    // Headless Service DNS 需要
					Subdomain:     workerSvcName, // 和 Headless Service 同名
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers: []corev1.Container{
						{
							Name:      "worker",
							Image:     exp.Spec.Image,
							Resources: exp.Spec.Resources,
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8081, Name: "http"},
							},
							Env: []corev1.EnvVar{
								{Name: "ROLE", Value: "worker"},
								{Name: "RANK", Value: strconv.Itoa(i)},
								{Name: "MASTER_ADDR", Value: masterAddr},
								{Name: "ROUNDS", Value: strconv.Itoa(int(exp.Spec.Rounds))},
								// Pod IP 通过 Downward API 注入，Worker 注册时用
								{
									Name: "POD_IP",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "status.podIP",
										},
									},
								},
							},
						},
					},
				},
			}
			if err := ctrl.SetControllerReference(exp, workerPod, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.Create(ctx, workerPod); err != nil {
				return ctrl.Result{}, err
			}
		} else if err != nil {
			return ctrl.Result{}, err
		}
	}

	// 3.3 更新 Status → CreatingWorkers
	exp.Status.WorkerPods = workerPods
	exp.Status.Phase = "CreatingWorkers"
	if err := r.Status().Update(ctx, exp); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("All Worker Pods created, waiting for them to be Running")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// ========== 阶段 4: 等所有 Worker Running ==========
func (r *FLExperimentReconciler) reconcileWaitWorkersRunning(ctx context.Context, exp *mlv1.FLExperiment) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	readyCount := int32(0)
	for i := 0; i < int(exp.Spec.WorkerReplicas); i++ {
		workerName := fmt.Sprintf("%s-worker-%d", exp.Name, i)
		workerPod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: workerName, Namespace: exp.Namespace}, workerPod); err != nil {
			if errors.IsNotFound(err) {
				logger.Info("Worker Pod not found, will retry", "name", workerName)
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}

		switch workerPod.Status.Phase {
		case corev1.PodRunning:
			readyCount++
		case corev1.PodFailed:
			logger.Info("Worker Pod failed", "name", workerName)
			exp.Status.Phase = "Failed"
			r.Status().Update(ctx, exp)
			return ctrl.Result{}, nil
		default:
			logger.Info("Worker Pod not ready yet", "name", workerName, "phase", workerPod.Status.Phase)
		}
	}

	exp.Status.ReadyWorkers = readyCount

	if readyCount == exp.Spec.WorkerReplicas {
		logger.Info("All Workers are Running, entering Training phase")
		exp.Status.Phase = "Training"
	}

	if err := r.Status().Update(ctx, exp); err != nil {
		return ctrl.Result{}, err
	}

	if exp.Status.Phase == "Training" {
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// ========== 阶段 5: 训练中，监控状态 ==========
func (r *FLExperimentReconciler) reconcileTraining(ctx context.Context, exp *mlv1.FLExperiment, masterName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 5.1 检查 Master Pod 状态
	masterPod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: masterName, Namespace: exp.Namespace}, masterPod); err != nil {
		return ctrl.Result{}, err
	}

	switch masterPod.Status.Phase {
	case corev1.PodSucceeded:
		logger.Info("Master Pod completed, experiment finished successfully")
		exp.Status.Phase = "Completed"
		r.Status().Update(ctx, exp)
		return ctrl.Result{}, nil

	case corev1.PodFailed:
		logger.Info("Master Pod failed")
		exp.Status.Phase = "Failed"
		r.Status().Update(ctx, exp)
		return ctrl.Result{}, nil
	}

	// 5.2 检查 Worker Pods 状态
	for i := 0; i < int(exp.Spec.WorkerReplicas); i++ {
		workerName := fmt.Sprintf("%s-worker-%d", exp.Name, i)
		workerPod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: workerName, Namespace: exp.Namespace}, workerPod); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return ctrl.Result{}, err
		}

		if workerPod.Status.Phase == corev1.PodFailed {
			logger.Info("Worker Pod failed during training", "name", workerName)
			exp.Status.Phase = "Failed"
			r.Status().Update(ctx, exp)
			return ctrl.Result{}, nil
		}

		// Worker Succeeded 是正常的（Master 发了 /shutdown）
		if workerPod.Status.Phase == corev1.PodSucceeded {
			logger.Info("Worker Pod completed", "name", workerName)
		}
	}

	logger.Info("Training in progress, checking again later")
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FLExperimentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mlv1.FLExperiment{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
