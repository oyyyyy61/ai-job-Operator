/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	LabelManaged = "ai.gpustack.io/managed"
	LabelJobName = "ai.gpustack.io/job-name"

	AnnotationGPUCount  = "ai.gpustack.io/gpu-count"
	AnnotationPriority  = "ai.gpustack.io/priority"
	AnnotationGroupName = "scheduling.k8s.io/group-name"

	GPUResourceName = corev1.ResourceName("nvidia.com/gpu")
)

// Priority controls the Volcano PodGroup and Pod scheduling priority.
// +kubebuilder:validation:Enum=low;normal;high
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

// AIJobPhase is a summarized view of the managed Pod phase.
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed
type AIJobPhase string

const (
	AIJobPending   AIJobPhase = "Pending"
	AIJobRunning   AIJobPhase = "Running"
	AIJobSucceeded AIJobPhase = "Succeeded"
	AIJobFailed    AIJobPhase = "Failed"
)

// Ownership adds the labels consumed by the existing GPU observability stack.
type Ownership struct {
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	Owner string `json:"owner,omitempty"`
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	Team string `json:"team,omitempty"`
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	Project string `json:"project,omitempty"`
}

// EnvironmentVariable is a literal environment value. Secret references are intentionally excluded.
type EnvironmentVariable struct {
	// +kubebuilder:validation:Pattern=`^[A-Za-z_][A-Za-z0-9_]*$`
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// AIJobSpec defines one GPU workload managed by the operator.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable; create a new AIJob for a new run"
type AIJobSpec struct {
	// Image is the worker container image.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	Command []string              `json:"command,omitempty"`
	Args    []string              `json:"args,omitempty"`
	Env     []EnvironmentVariable `json:"env,omitempty"`

	// GPU is the number of whole NVIDIA GPUs injected into requests and limits.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=8
	GPU int32 `json:"gpu,omitempty"`

	// Priority maps to one of ai-low, ai-normal, or ai-high.
	// +kubebuilder:default=normal
	Priority Priority `json:"priority,omitempty"`

	// Queue is the Volcano queue used by the generated PodGroup.
	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Queue string `json:"queue,omitempty"`

	// Resources configures CPU, memory, and other non-GPU resources.
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	NodeSelector map[string]string   `json:"nodeSelector,omitempty"`
	Tolerations  []corev1.Toleration `json:"tolerations,omitempty"`

	RuntimeClassName *string `json:"runtimeClassName,omitempty"`

	// +kubebuilder:default=Never
	// +kubebuilder:validation:Enum=Never;OnFailure
	RestartPolicy corev1.RestartPolicy `json:"restartPolicy,omitempty"`

	Ownership Ownership `json:"ownership,omitempty"`
}

// AIJobStatus defines the observed state of AIJob.
type AIJobStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Phase              AIJobPhase         `json:"phase,omitempty"`
	PodName            string             `json:"podName,omitempty"`
	NodeName           string             `json:"nodeName,omitempty"`
	Message            string             `json:"message,omitempty"`
	StartTime          *metav1.Time       `json:"startTime,omitempty"`
	CompletionTime     *metav1.Time       `json:"completionTime,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=aijob;aj
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Priority",type=string,JSONPath=`.spec.priority`
// +kubebuilder:printcolumn:name="GPU",type=integer,JSONPath=`.spec.gpu`
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=`.status.podName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AIJob is the Schema for the aijobs API.
type AIJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIJobSpec   `json:"spec,omitempty"`
	Status AIJobStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AIJobList contains a list of AIJob.
type AIJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AIJob{}, &AIJobList{})
}
