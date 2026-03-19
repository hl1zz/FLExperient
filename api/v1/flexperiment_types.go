package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FLExperimentSpec defines the desired state of FLExperiment
type FLExperimentSpec struct {
	// 联邦学习任务的容器镜像
	Image string `json:"image"`

	// Worker 节点数量
	WorkerReplicas int32 `json:"workerReplicas"`

	// 联邦学习的轮次
	Rounds int32 `json:"rounds,omitempty"`

	// 资源限制
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// FLExperimentStatus defines the observed state of FLExperiment
type FLExperimentStatus struct {
	// 当前阶段: "" → CreatingMaster → MasterRunning → CreatingWorkers → Training → Completed / Failed
	Phase string `json:"phase,omitempty"`

	// Master Pod 名称
	MasterPod string `json:"masterPod,omitempty"`

	// Worker Pods 名称列表
	WorkerPods []string `json:"workerPods,omitempty"`

	// 已就绪的 Worker 数量（已 Running）
	ReadyWorkers int32 `json:"readyWorkers,omitempty"`

	// 当前训练轮次（由 Controller 轮询 Master /status 获取）
	CurrentRound int32 `json:"currentRound,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// FLExperiment is the Schema for the flexperiments API
type FLExperiment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FLExperimentSpec   `json:"spec,omitempty"`
	Status FLExperimentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// FLExperimentList contains a list of FLExperiment
type FLExperimentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FLExperiment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FLExperiment{}, &FLExperimentList{})
}
