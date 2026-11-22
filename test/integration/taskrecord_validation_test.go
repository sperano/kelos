package integration

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	kelos "github.com/kelos-dev/kelos/api/v1alpha2"
)

var _ = Describe("TaskRecord API validation", func() {
	const ns = "test-taskrecord-validation"

	BeforeEach(func() {
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		_ = k8sClient.Create(ctx, namespace)
	})

	It("accepts a well-formed TaskRecord", func() {
		completionTime := metav1.Now()
		record := &kelos.TaskRecord{
			ObjectMeta: metav1.ObjectMeta{Name: "valid", Namespace: ns},
			Spec: kelos.TaskRecordSpec{
				TaskRef:        kelos.TaskReference{Name: "task-1", UID: types.UID("task-uid")},
				Phase:          kelos.TaskPhaseSucceeded,
				CompletionTime: &completionTime,
			},
		}
		Expect(k8sClient.Create(ctx, record)).Should(Succeed())
	})

	It("rejects a spec-less TaskRecord", func() {
		record := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "kelos.dev/v1alpha2",
				"kind":       "TaskRecord",
				"metadata": map[string]interface{}{
					"name":      "no-spec",
					"namespace": ns,
				},
			},
		}
		record.SetGroupVersionKind(schema.GroupVersionKind{Group: "kelos.dev", Version: "v1alpha2", Kind: "TaskRecord"})
		Expect(k8sClient.Create(ctx, record)).ShouldNot(Succeed())
	})

	It("rejects a TaskRecord without completionTime", func() {
		record := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "kelos.dev/v1alpha2",
				"kind":       "TaskRecord",
				"metadata": map[string]interface{}{
					"name":      "no-completion-time",
					"namespace": ns,
				},
				"spec": map[string]interface{}{
					"taskRef": map[string]interface{}{
						"name": "task-1",
						"uid":  "task-uid",
					},
					"phase": "Succeeded",
				},
			},
		}
		record.SetGroupVersionKind(schema.GroupVersionKind{Group: "kelos.dev", Version: "v1alpha2", Kind: "TaskRecord"})
		Expect(k8sClient.Create(ctx, record)).ShouldNot(Succeed())
	})

	It("retains a usage breakdown per attempt", func() {
		completionTime := metav1.Now()
		output := int64(190)
		attempts := []kelos.UsageAttempt{{
			PodName: "task-1-abcde",
			PodUID:  types.UID("pod-uid"),
			Breakdown: kelos.UsageBreakdown{
				SchemaVersion:     kelos.UsageBreakdownSchemaVersion,
				Runner:            "claude-code",
				Final:             true,
				Coverage:          kelos.UsageCoverageComplete,
				IncludesSubagents: true,
				Models: []kelos.ModelUsage{{
					Model:         "claude-opus-5-5",
					ModelSource:   kelos.ModelSourceReported,
					OutputTokens:  &output,
					OtherCounters: map[string]int64{"advisorTokens": 12},
				}},
				ReportedCostUSD: "0.3125",
				CostSource:      "claude-code",
			},
		}}
		record := &kelos.TaskRecord{
			ObjectMeta: metav1.ObjectMeta{Name: "with-attempts", Namespace: ns},
			Spec: kelos.TaskRecordSpec{
				TaskRef:        kelos.TaskReference{Name: "task-1", UID: types.UID("task-uid")},
				Phase:          kelos.TaskPhaseSucceeded,
				CompletionTime: &completionTime,
				Usage:          &kelos.TaskUsage{Attempts: attempts},
			},
		}
		Expect(k8sClient.Create(ctx, record)).Should(Succeed())

		var got kelos.TaskRecord
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "with-attempts"}, &got)).Should(Succeed())
		Expect(got.Spec.Usage.Attempts).To(HaveLen(1))
		b := got.Spec.Usage.Attempts[0].Breakdown
		Expect(b.Coverage).To(Equal(kelos.UsageCoverageComplete))
		Expect(*b.Models[0].OutputTokens).To(Equal(int64(190)))
		Expect(b.Models[0].OtherCounters).To(Equal(map[string]int64{"advisorTokens": 12}))
		Expect(b.ReportedCostUSD).To(Equal("0.3125"))
	})

	It("rejects a usage breakdown with an unknown coverage or a cost that is not a plain decimal", func() {
		for name, breakdown := range map[string]map[string]interface{}{
			"bad-coverage":  {"schemaVersion": int64(1), "coverage": "Mostly"},
			"negative-cost": {"schemaVersion": int64(1), "coverage": "Complete", "reportedCostUSD": "-1"},
			"exponent-cost": {"schemaVersion": int64(1), "coverage": "Complete", "reportedCostUSD": "1e-05"},
		} {
			record := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "kelos.dev/v1alpha2",
					"kind":       "TaskRecord",
					"metadata":   map[string]interface{}{"name": name, "namespace": ns},
					"spec": map[string]interface{}{
						"taskRef":        map[string]interface{}{"name": "task-1", "uid": "task-uid"},
						"phase":          "Succeeded",
						"completionTime": "2026-11-22T00:00:00Z",
						"usage": map[string]interface{}{
							"attempts": []interface{}{map[string]interface{}{"podName": "p", "breakdown": breakdown}},
						},
					},
				},
			}
			record.SetGroupVersionKind(schema.GroupVersionKind{Group: "kelos.dev", Version: "v1alpha2", Kind: "TaskRecord"})
			Expect(k8sClient.Create(ctx, record)).ShouldNot(Succeed(), name)
		}
	})
})
