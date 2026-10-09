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

package controllers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/oyyyyy61/ai-job-operator/api/v1alpha1"
)

func TestReconcileCreatesPodGroupAndUnmutatedPod(t *testing.T) {
	scheme := runtime.NewScheme()
	must(t, corev1.AddToScheme(scheme))
	must(t, aiv1alpha1.AddToScheme(scheme))
	scheme.AddKnownTypeWithName(podGroupGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(podGroupGVK.GroupVersion().WithKind("PodGroupList"), &unstructured.UnstructuredList{})

	job := &aiv1alpha1.AIJob{
		TypeMeta: metav1.TypeMeta{APIVersion: aiv1alpha1.GroupVersion.String(), Kind: "AIJob"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "train-high", Namespace: "default", UID: types.UID("job-uid"), Generation: 1,
		},
		Spec: aiv1alpha1.AIJobSpec{
			Image:    "example.invalid/trainer:v1",
			GPU:      2,
			Priority: aiv1alpha1.PriorityHigh,
			Queue:    "default",
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("500m"),
			}, Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			}},
			Ownership: aiv1alpha1.Ownership{Owner: "alice", Team: "ml", Project: "demo"},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&aiv1alpha1.AIJob{}).WithObjects(job).Build()
	reconciler := &AIJobReconciler{Client: client, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: job.Name, Namespace: job.Namespace},
	})
	must(t, err)

	pod := &corev1.Pod{}
	must(t, client.Get(context.Background(), types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, pod))
	if _, found := pod.Spec.Containers[0].Resources.Limits[aiv1alpha1.GPUResourceName]; found {
		t.Fatal("controller must leave GPU injection to the mutating webhook")
	}
	if got := pod.Annotations[aiv1alpha1.AnnotationGPUCount]; got != "2" {
		t.Fatalf("gpu annotation = %q, want 2", got)
	}
	if got := pod.Labels[ownerLabel]; got != "alice" {
		t.Fatalf("owner label = %q, want alice", got)
	}

	group := &unstructured.Unstructured{}
	group.SetGroupVersionKind(podGroupGVK)
	must(t, client.Get(context.Background(), types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, group))
	priority, _, err := unstructured.NestedString(group.Object, "spec", "priorityClassName")
	must(t, err)
	if priority != "ai-high" {
		t.Fatalf("PodGroup priorityClassName = %q, want ai-high", priority)
	}
	gpu, _, err := unstructured.NestedString(group.Object, "spec", "minResources", "nvidia.com/gpu")
	must(t, err)
	if gpu != "2" {
		t.Fatalf("PodGroup GPU minResource = %q, want 2", gpu)
	}
	memory, _, err := unstructured.NestedString(group.Object, "spec", "minResources", "memory")
	must(t, err)
	if memory != "1Gi" {
		t.Fatalf("PodGroup memory minResource = %q, want 1Gi", memory)
	}

	updated := &aiv1alpha1.AIJob{}
	must(t, client.Get(context.Background(), types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, updated))
	if updated.Status.Phase != aiv1alpha1.AIJobPending {
		t.Fatalf("AIJob phase = %q, want Pending", updated.Status.Phase)
	}
}

func TestDesiredPodAppliesDefaults(t *testing.T) {
	scheme := runtime.NewScheme()
	must(t, aiv1alpha1.AddToScheme(scheme))
	job := &aiv1alpha1.AIJob{
		ObjectMeta: metav1.ObjectMeta{Name: "defaults", Namespace: "default", UID: types.UID("job-uid")},
		Spec: aiv1alpha1.AIJobSpec{
			Image: "example.invalid/trainer:v1",
			Env:   []aiv1alpha1.EnvironmentVariable{{Name: "MODEL", Value: "demo"}},
		},
	}
	reconciler := &AIJobReconciler{Scheme: scheme}
	pod, err := reconciler.desiredPod(job)
	must(t, err)
	if pod.Annotations[aiv1alpha1.AnnotationGPUCount] != "1" || pod.Annotations[aiv1alpha1.AnnotationPriority] != "normal" {
		t.Fatalf("default annotations are incomplete: %#v", pod.Annotations)
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("restartPolicy = %q, want Never", pod.Spec.RestartPolicy)
	}
	if len(pod.Spec.Containers[0].Env) != 1 || pod.Spec.Containers[0].Env[0].Value != "demo" {
		t.Fatalf("literal environment variables were not copied: %#v", pod.Spec.Containers[0].Env)
	}
}

func TestLabelValueIsStableAndValidLength(t *testing.T) {
	longName := "this-is-a-very-long-aijob-name-that-exceeds-the-kubernetes-label-value-limit"
	first := labelValue(longName)
	second := labelValue(longName)
	if first != second || len(first) != 63 {
		t.Fatalf("labelValue() = %q (%d chars), want stable 63-char value", first, len(first))
	}
}

func TestReconcileRefusesUnownedPodGroup(t *testing.T) {
	scheme := runtime.NewScheme()
	must(t, corev1.AddToScheme(scheme))
	must(t, aiv1alpha1.AddToScheme(scheme))
	scheme.AddKnownTypeWithName(podGroupGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(podGroupGVK.GroupVersion().WithKind("PodGroupList"), &unstructured.UnstructuredList{})

	job := &aiv1alpha1.AIJob{
		ObjectMeta: metav1.ObjectMeta{Name: "collision", Namespace: "default", UID: types.UID("job-uid")},
		Spec:       aiv1alpha1.AIJobSpec{Image: "example.invalid/trainer:v1"},
	}
	group := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": podGroupGVK.GroupVersion().String(),
		"kind":       podGroupGVK.Kind,
		"metadata": map[string]interface{}{
			"name": "collision", "namespace": "default",
		},
	}}
	group.SetGroupVersionKind(podGroupGVK)

	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&aiv1alpha1.AIJob{}).WithObjects(job, group).Build()
	reconciler := &AIJobReconciler{Client: client, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: job.Name, Namespace: job.Namespace},
	})
	if err == nil {
		t.Fatal("expected an unowned PodGroup collision to fail reconciliation")
	}

	updated := &aiv1alpha1.AIJob{}
	must(t, client.Get(context.Background(), types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, updated))
	ready := findCondition(updated.Status.Conditions, conditionReady)
	if ready == nil || ready.Reason != "ResourceCollision" {
		t.Fatalf("Ready condition = %#v, want ResourceCollision", ready)
	}
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for index := range conditions {
		if conditions[index].Type == conditionType {
			return &conditions[index]
		}
	}
	return nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
