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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/oyyyyy61/ai-job-operator/api/v1alpha1"
)

func TestMutatePodInjectsGPUAndScheduling(t *testing.T) {
	pod := managedPod("2", "high")
	defaultPriority := int32(0)
	pod.Spec.Priority = &defaultPriority
	if err := MutatePod(pod); err != nil {
		t.Fatal(err)
	}
	assertQuantity(t, pod.Spec.Containers[0].Resources.Requests, aiv1alpha1.GPUResourceName, "2")
	assertQuantity(t, pod.Spec.Containers[0].Resources.Limits, aiv1alpha1.GPUResourceName, "2")
	if pod.Spec.SchedulerName != "volcano" {
		t.Fatalf("schedulerName = %q, want volcano", pod.Spec.SchedulerName)
	}
	if pod.Spec.PriorityClassName != "ai-high" {
		t.Fatalf("priorityClassName = %q, want ai-high", pod.Spec.PriorityClassName)
	}
	if pod.Spec.Priority == nil || *pod.Spec.Priority != priorityHighValue {
		t.Fatalf("priority = %v, want %d", pod.Spec.Priority, priorityHighValue)
	}

	if err := MutatePod(pod); err != nil {
		t.Fatalf("second mutation must be idempotent: %v", err)
	}
	assertQuantity(t, pod.Spec.Containers[0].Resources.Limits, aiv1alpha1.GPUResourceName, "2")
}

func TestMutatePodLeavesUnmanagedPodAlone(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: workerContainerName}}}}
	if err := MutatePod(pod); err != nil {
		t.Fatal(err)
	}
	if pod.Spec.SchedulerName != "" {
		t.Fatalf("unmanaged Pod schedulerName changed to %q", pod.Spec.SchedulerName)
	}
}

func TestMutatePodRejectsInvalidGPUCount(t *testing.T) {
	pod := managedPod("zero", "normal")
	if err := MutatePod(pod); err == nil {
		t.Fatal("expected invalid GPU annotation to be rejected")
	}
}

func managedPod(gpu, priority string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{aiv1alpha1.LabelManaged: "true"},
			Annotations: map[string]string{
				aiv1alpha1.AnnotationGPUCount: gpu,
				aiv1alpha1.AnnotationPriority: priority,
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: workerContainerName}}},
	}
}

func assertQuantity(t *testing.T, resources corev1.ResourceList, name corev1.ResourceName, want string) {
	t.Helper()
	got, found := resources[name]
	if !found {
		t.Fatalf("resource %s was not injected", name)
	}
	if got.Cmp(resource.MustParse(want)) != 0 {
		t.Fatalf("resource %s = %s, want %s", name, got.String(), want)
	}
}
