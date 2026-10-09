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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/oyyyyy61/ai-job-operator/api/v1alpha1"
)

const (
	conditionReady     = "Ready"
	conditionCompleted = "Completed"
	conditionFailed    = "Failed"
	containerName      = "worker"

	ownerLabel   = "observability.gpustack.io/owner"
	teamLabel    = "observability.gpustack.io/team"
	projectLabel = "observability.gpustack.io/project"
	taskLabel    = "observability.gpustack.io/task"
)

var podGroupGVK = schema.GroupVersionKind{
	Group: "scheduling.volcano.sh", Version: "v1beta1", Kind: "PodGroup",
}

type resourceCollisionError struct {
	message string
}

func (e *resourceCollisionError) Error() string {
	return e.message
}

// AIJobReconciler reconciles an AIJob object.
type AIJobReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=ai.gpustack.io,resources=aijobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ai.gpustack.io,resources=aijobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ai.gpustack.io,resources=aijobs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch;update
// +kubebuilder:rbac:groups=scheduling.volcano.sh,resources=podgroups,verbs=get;list;watch;create;update;patch;delete

// Reconcile creates the Volcano PodGroup before the Pod and mirrors Pod state to AIJob status.
func (r *AIJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	job := &aiv1alpha1.AIJob{}
	if err := r.Get(ctx, req.NamespacedName, job); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !job.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if err := r.ensurePodGroup(ctx, job); err != nil {
		logger.Error(err, "unable to reconcile Volcano PodGroup")
		reason := "SchedulerUnavailable"
		collision := &resourceCollisionError{}
		if errors.As(err, &collision) {
			reason = "ResourceCollision"
		}
		_ = r.updateFailureStatus(ctx, job, reason, err.Error())
		return ctrl.Result{}, err
	}

	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Name: job.Name, Namespace: job.Namespace}
	if err := r.Get(ctx, podKey, pod); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		pod, err = r.desiredPod(job)
		if err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, pod); err != nil {
			_ = r.updateFailureStatus(ctx, job, "PodCreateFailed", err.Error())
			return ctrl.Result{}, err
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(job, pod, corev1.EventTypeNormal, "PodCreated", "CreatePod", "Created Pod %s", pod.Name)
		}
	} else if !metav1.IsControlledBy(pod, job) {
		err := fmt.Errorf("Pod %s/%s already exists and is not controlled by AIJob %s", pod.Namespace, pod.Name, job.Name)
		_ = r.updateFailureStatus(ctx, job, "ResourceCollision", err.Error())
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, job, pod); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AIJobReconciler) ensurePodGroup(ctx context.Context, job *aiv1alpha1.AIJob) error {
	desired, err := r.desiredPodGroup(job)
	if err != nil {
		return err
	}
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(podGroupGVK)
	err = r.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, current)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if !metav1.IsControlledBy(current, job) {
		return &resourceCollisionError{message: fmt.Sprintf(
			"PodGroup %s/%s already exists and is not controlled by AIJob %s",
			current.GetNamespace(), current.GetName(), job.Name,
		)}
	}

	desiredSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	currentSpec, _, _ := unstructured.NestedMap(current.Object, "spec")
	if reflect.DeepEqual(desiredSpec, currentSpec) {
		return nil
	}
	if err := unstructured.SetNestedMap(current.Object, desiredSpec, "spec"); err != nil {
		return err
	}
	return r.Update(ctx, current)
}

func (r *AIJobReconciler) desiredPodGroup(job *aiv1alpha1.AIJob) (*unstructured.Unstructured, error) {
	minResources := map[string]interface{}{
		string(aiv1alpha1.GPUResourceName): strconv.FormatInt(int64(effectiveGPU(job.Spec.GPU)), 10),
	}
	for name, quantity := range job.Spec.Resources.Limits {
		if name != aiv1alpha1.GPUResourceName {
			minResources[string(name)] = quantity.String()
		}
	}
	for name, quantity := range job.Spec.Resources.Requests {
		if name != aiv1alpha1.GPUResourceName {
			minResources[string(name)] = quantity.String()
		}
	}

	group := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": podGroupGVK.GroupVersion().String(),
		"kind":       podGroupGVK.Kind,
		"metadata": map[string]interface{}{
			"name":      job.Name,
			"namespace": job.Namespace,
		},
		"spec": map[string]interface{}{
			"minMember":         int64(1),
			"minResources":      minResources,
			"priorityClassName": priorityClassName(effectivePriority(job.Spec.Priority)),
			"queue":             effectiveQueue(job.Spec.Queue),
		},
	}}
	group.SetGroupVersionKind(podGroupGVK)
	if err := controllerutil.SetControllerReference(job, group, r.Scheme); err != nil {
		return nil, err
	}
	return group, nil
}

func (r *AIJobReconciler) desiredPod(job *aiv1alpha1.AIJob) (*corev1.Pod, error) {
	resources := *job.Spec.Resources.DeepCopy()
	delete(resources.Requests, aiv1alpha1.GPUResourceName)
	delete(resources.Limits, aiv1alpha1.GPUResourceName)

	labels := map[string]string{
		aiv1alpha1.LabelManaged: "true",
		aiv1alpha1.LabelJobName: labelValue(job.Name),
		taskLabel:               labelValue(job.Name),
	}
	addLabel(labels, ownerLabel, job.Spec.Ownership.Owner)
	addLabel(labels, teamLabel, job.Spec.Ownership.Team)
	addLabel(labels, projectLabel, job.Spec.Ownership.Project)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.Name,
			Namespace: job.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				aiv1alpha1.AnnotationGPUCount:  strconv.FormatInt(int64(effectiveGPU(job.Spec.GPU)), 10),
				aiv1alpha1.AnnotationPriority:  string(effectivePriority(job.Spec.Priority)),
				aiv1alpha1.AnnotationGroupName: job.Name,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:    effectiveRestartPolicy(job.Spec.RestartPolicy),
			NodeSelector:     copyStringMap(job.Spec.NodeSelector),
			Tolerations:      append([]corev1.Toleration(nil), job.Spec.Tolerations...),
			RuntimeClassName: job.Spec.RuntimeClassName,
			Containers: []corev1.Container{{
				Name:            containerName,
				Image:           job.Spec.Image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         append([]string(nil), job.Spec.Command...),
				Args:            append([]string(nil), job.Spec.Args...),
				Env:             environmentVariables(job.Spec.Env),
				Resources:       resources,
			}},
		},
	}
	if err := controllerutil.SetControllerReference(job, pod, r.Scheme); err != nil {
		return nil, err
	}
	return pod, nil
}

func (r *AIJobReconciler) updateStatus(ctx context.Context, job *aiv1alpha1.AIJob, pod *corev1.Pod) error {
	original := job.DeepCopy()
	job.Status.ObservedGeneration = job.Generation
	job.Status.PodName = pod.Name
	job.Status.NodeName = pod.Spec.NodeName
	job.Status.StartTime = pod.Status.StartTime
	job.Status.Message = podMessage(pod)

	switch pod.Status.Phase {
	case corev1.PodRunning:
		job.Status.Phase = aiv1alpha1.AIJobRunning
		meta.SetStatusCondition(&job.Status.Conditions, condition(metav1.ConditionTrue, "PodRunning", "The AIJob Pod is running", job.Generation))
		meta.SetStatusCondition(&job.Status.Conditions, stateCondition(conditionCompleted, metav1.ConditionFalse, "PodRunning", "The AIJob has not completed", job.Generation))
		meta.SetStatusCondition(&job.Status.Conditions, stateCondition(conditionFailed, metav1.ConditionFalse, "PodRunning", "The AIJob has not failed", job.Generation))
	case corev1.PodSucceeded:
		job.Status.Phase = aiv1alpha1.AIJobSucceeded
		job.Status.CompletionTime = completionTime(pod, job.Status.CompletionTime)
		meta.SetStatusCondition(&job.Status.Conditions, condition(metav1.ConditionFalse, "PodSucceeded", "The AIJob completed successfully", job.Generation))
		meta.SetStatusCondition(&job.Status.Conditions, stateCondition(conditionCompleted, metav1.ConditionTrue, "PodSucceeded", "The AIJob completed successfully", job.Generation))
		meta.SetStatusCondition(&job.Status.Conditions, stateCondition(conditionFailed, metav1.ConditionFalse, "PodSucceeded", "The AIJob did not fail", job.Generation))
	case corev1.PodFailed:
		job.Status.Phase = aiv1alpha1.AIJobFailed
		job.Status.CompletionTime = completionTime(pod, job.Status.CompletionTime)
		meta.SetStatusCondition(&job.Status.Conditions, condition(metav1.ConditionFalse, "PodFailed", "The AIJob Pod failed", job.Generation))
		meta.SetStatusCondition(&job.Status.Conditions, stateCondition(conditionCompleted, metav1.ConditionFalse, "PodFailed", "The AIJob did not complete successfully", job.Generation))
		meta.SetStatusCondition(&job.Status.Conditions, stateCondition(conditionFailed, metav1.ConditionTrue, "PodFailed", "The AIJob Pod failed", job.Generation))
	default:
		job.Status.Phase = aiv1alpha1.AIJobPending
		meta.SetStatusCondition(&job.Status.Conditions, condition(metav1.ConditionFalse, "PodPending", "The AIJob Pod is waiting for scheduling or startup", job.Generation))
		meta.SetStatusCondition(&job.Status.Conditions, stateCondition(conditionCompleted, metav1.ConditionFalse, "PodPending", "The AIJob has not completed", job.Generation))
		meta.SetStatusCondition(&job.Status.Conditions, stateCondition(conditionFailed, metav1.ConditionFalse, "PodPending", "The AIJob has not failed", job.Generation))
	}

	if reflect.DeepEqual(original.Status, job.Status) {
		return nil
	}
	return r.Status().Patch(ctx, job, client.MergeFrom(original))
}

func (r *AIJobReconciler) updateFailureStatus(ctx context.Context, job *aiv1alpha1.AIJob, reason, message string) error {
	original := job.DeepCopy()
	job.Status.ObservedGeneration = job.Generation
	job.Status.Phase = aiv1alpha1.AIJobPending
	job.Status.Message = message
	meta.SetStatusCondition(&job.Status.Conditions, condition(metav1.ConditionFalse, reason, message, job.Generation))
	meta.SetStatusCondition(&job.Status.Conditions, stateCondition(conditionCompleted, metav1.ConditionFalse, reason, "The AIJob has not completed", job.Generation))
	meta.SetStatusCondition(&job.Status.Conditions, stateCondition(conditionFailed, metav1.ConditionFalse, reason, "The AIJob workload has not failed", job.Generation))
	return r.Status().Patch(ctx, job, client.MergeFrom(original))
}

func condition(status metav1.ConditionStatus, reason, message string, generation int64) metav1.Condition {
	return stateCondition(conditionReady, status, reason, message, generation)
}

func stateCondition(conditionType string, status metav1.ConditionStatus, reason, message string, generation int64) metav1.Condition {
	return metav1.Condition{
		Type: conditionType, Status: status, Reason: reason, Message: message,
		ObservedGeneration: generation,
	}
}

func podMessage(pod *corev1.Pod) string {
	for _, podCondition := range pod.Status.Conditions {
		if podCondition.Type == corev1.PodScheduled && podCondition.Status == corev1.ConditionFalse {
			return podCondition.Message
		}
	}
	if pod.Status.Message != "" {
		return pod.Status.Message
	}
	return string(pod.Status.Phase)
}

func completionTime(pod *corev1.Pod, existing *metav1.Time) *metav1.Time {
	if existing != nil {
		return existing
	}
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if terminated := containerStatus.State.Terminated; terminated != nil && !terminated.FinishedAt.IsZero() {
			finishedAt := terminated.FinishedAt
			return &finishedAt
		}
	}
	now := metav1.Now()
	return &now
}

func priorityClassName(priority aiv1alpha1.Priority) string {
	return fmt.Sprintf("ai-%s", priority)
}

func effectiveGPU(value int32) int32 {
	if value < 1 {
		return 1
	}
	return value
}

func effectivePriority(value aiv1alpha1.Priority) aiv1alpha1.Priority {
	if value == "" {
		return aiv1alpha1.PriorityNormal
	}
	return value
}

func effectiveQueue(value string) string {
	if value == "" {
		return "default"
	}
	return value
}

func effectiveRestartPolicy(value corev1.RestartPolicy) corev1.RestartPolicy {
	if value == "" {
		return corev1.RestartPolicyNever
	}
	return value
}

func addLabel(labels map[string]string, key, value string) {
	if value != "" {
		labels[key] = value
	}
}

func labelValue(value string) string {
	if len(value) <= 63 {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return value[:54] + "-" + hex.EncodeToString(sum[:4])
}

func environmentVariables(source []aiv1alpha1.EnvironmentVariable) []corev1.EnvVar {
	if source == nil {
		return nil
	}
	result := make([]corev1.EnvVar, len(source))
	for index, item := range source {
		result[index] = corev1.EnvVar{Name: item.Name, Value: item.Value}
	}
	return result
}

func copyStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// SetupWithManager sets up the controller with the Manager.
func (r *AIJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha1.AIJob{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
