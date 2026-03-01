package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FLExperimentSpec defines the desired state of FLExperiment
type FLExperimentSpec struct {
	// 联邦学习任务的容器镜像
	Image string `json:"image"`

	// Trainer 节点数量
	TrainerReplicas int32 `json:"trainerReplicas"`

	// 联邦学习的轮次
	Rounds int32 `json:"rounds,omitempty"`

	// 资源限制
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// FLExperimentStatus defines the observed state of FLExperiment
type FLExperimentStatus struct {
	// 当前阶段: Pending, Running, Completed, Failed
	Phase string `json:"phase,omitempty"`

	// Aggregator Pod 名称
	AggregatorPod string `json:"aggregatorPod,omitempty"`

	// Trainer Pods 名称列表
	TrainerPods []string `json:"trainerPods,omitempty"`
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
