// Package volcano 定义了 Volcano PodGroup 的最小化类型。
// 我们只需要用这些类型来创建/读取 PodGroup 对象，
// 真正的 gang scheduling 逻辑由集群中运行的 volcano-scheduler 负责。
package volcano

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ---- GroupVersion 信息 ----

var (
	SchemeGroupVersion = schema.GroupVersion{Group: "scheduling.volcano.sh", Version: "v1beta1"}
	SchemeBuilder      = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&PodGroup{},
		&PodGroupList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// ---- PodGroup 类型定义（精简版，只包含我们需要的字段） ----

// +kubebuilder:object:root=true
type PodGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PodGroupSpec   `json:"spec,omitempty"`
	Status PodGroupStatus `json:"status,omitempty"`
}

type PodGroupSpec struct {
	// 最小成员数：不满足则一个都不调度
	MinMember int32 `json:"minMember,omitempty"`
}

// PodGroupPhase is the phase of a PodGroup at the current time.
type PodGroupPhase string

const (
	PodGroupPending PodGroupPhase = "Pending"
	PodGroupRunning PodGroupPhase = "Running"
	PodGroupUnknown PodGroupPhase = "Unknown"
	PodGroupInqueue PodGroupPhase = "Inqueue"
)

type PodGroupStatus struct {
	// Volcano 填写的当前阶段
	Phase   PodGroupPhase `json:"phase,omitempty"`
	Running int32         `json:"running,omitempty"`
	Failed  int32         `json:"failed,omitempty"`
}

// +kubebuilder:object:root=true
type PodGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PodGroup `json:"items"`
}

// ---- DeepCopy 方法（满足 runtime.Object 接口） ----

func (in *PodGroup) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *PodGroup) DeepCopy() *PodGroup {
	if in == nil {
		return nil
	}
	out := new(PodGroup)
	in.DeepCopyInto(out)
	return out
}

func (in *PodGroup) DeepCopyInto(out *PodGroup) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	out.Status = in.Status
}

func (in *PodGroupList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *PodGroupList) DeepCopy() *PodGroupList {
	if in == nil {
		return nil
	}
	out := new(PodGroupList)
	in.DeepCopyInto(out)
	return out
}

func (in *PodGroupList) DeepCopyInto(out *PodGroupList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]PodGroup, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}
