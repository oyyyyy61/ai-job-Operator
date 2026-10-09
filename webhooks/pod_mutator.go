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

package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	aiv1alpha1 "github.com/oyyyyy61/ai-job-operator/api/v1alpha1"
)

const (
	workerContainerName = "worker"
	priorityLowValue    = int32(100)
	priorityNormalValue = int32(1000)
	priorityHighValue   = int32(10000)
)

// PodMutator injects scheduling and GPU settings into operator-managed Pods.
type PodMutator struct {
	decoder admission.Decoder
}

// NewPodMutator constructs a decoder-backed admission handler.
func NewPodMutator(scheme *runtime.Scheme) (*PodMutator, error) {
	return &PodMutator{decoder: admission.NewDecoder(scheme)}, nil
}

// +kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,failurePolicy=fail,sideEffects=None,groups="",resources=pods,verbs=create,versions=v1,name=mpod.ai.gpustack.io,admissionReviewVersions=v1

// Handle implements admission.Handler.
func (m *PodMutator) Handle(ctx context.Context, request admission.Request) admission.Response {
	pod := &corev1.Pod{}
	if err := m.decoder.Decode(request, pod); err != nil {
		return admission.Errored(400, err)
	}
	if err := MutatePod(pod); err != nil {
		return admission.Denied(err.Error())
	}
	marshaled, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(500, err)
	}
	return admission.PatchResponseFromRaw(request.Object.Raw, marshaled)
}

// MutatePod contains the deterministic, idempotent part of the admission logic.
func MutatePod(pod *corev1.Pod) error {
	if pod.Labels[aiv1alpha1.LabelManaged] != "true" {
		return nil
	}

	gpuText := pod.Annotations[aiv1alpha1.AnnotationGPUCount]
	gpuCount, err := strconv.ParseInt(gpuText, 10, 64)
	if err != nil || gpuCount < 1 || gpuCount > 8 {
		return fmt.Errorf("annotation %s must be an integer from 1 to 8", aiv1alpha1.AnnotationGPUCount)
	}
	priority := aiv1alpha1.Priority(pod.Annotations[aiv1alpha1.AnnotationPriority])
	if priority == "" {
		priority = aiv1alpha1.PriorityNormal
	}
	var priorityValue int32
	switch priority {
	case aiv1alpha1.PriorityLow:
		priorityValue = priorityLowValue
	case aiv1alpha1.PriorityNormal:
		priorityValue = priorityNormalValue
	case aiv1alpha1.PriorityHigh:
		priorityValue = priorityHighValue
	default:
		return fmt.Errorf("annotation %s must be low, normal, or high", aiv1alpha1.AnnotationPriority)
	}

	containerIndex := -1
	for index := range pod.Spec.Containers {
		if pod.Spec.Containers[index].Name == workerContainerName {
			containerIndex = index
			break
		}
	}
	if containerIndex < 0 {
		return fmt.Errorf("managed Pod must contain a %q container", workerContainerName)
	}

	container := &pod.Spec.Containers[containerIndex]
	if container.Resources.Requests == nil {
		container.Resources.Requests = corev1.ResourceList{}
	}
	if container.Resources.Limits == nil {
		container.Resources.Limits = corev1.ResourceList{}
	}
	quantity := *resource.NewQuantity(gpuCount, resource.DecimalSI)
	container.Resources.Requests[aiv1alpha1.GPUResourceName] = quantity
	container.Resources.Limits[aiv1alpha1.GPUResourceName] = quantity
	pod.Spec.SchedulerName = "volcano"
	pod.Spec.PriorityClassName = "ai-" + string(priority)
	pod.Spec.Priority = &priorityValue
	return nil
}
