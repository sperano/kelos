package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"sort"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kelos "github.com/kelos-dev/kelos/api/v1alpha2"
)

// usageBreakdownResultKey is the outputs key kelos-capture writes the
// normalized usage under (internal/capture).
const usageBreakdownResultKey = "usage-breakdown"

// Limits mirroring the TaskUsage.attempts and UsageBreakdown CRD
// validation, so an oversized breakdown is trimmed instead of failing the
// whole Task status update.
const (
	maxUsageAttempts      = 10
	maxUsageNotes         = 16
	maxUsageNoteLength    = 64
	maxUsageSubagents     = 64
	maxUsageModels        = 32
	maxUsageOtherCounters = 32
)

// Notes the controller adds to an attempt's breakdown.
const (
	noteAttemptUsageUnavailable = "attempt-usage-unavailable"
	noteBreakdownUnparseable    = "breakdown-unparseable"
	noteBreakdownTruncated      = "breakdown-truncated"
	noteEarlierAttemptsDropped  = "earlier-attempts-dropped"
)

// normalizedUsageAgents are the agent types kelos-capture writes a usage
// breakdown for.
var normalizedUsageAgents = map[string]bool{
	"claude-code": true,
	"codex":       true,
	"opencode":    true,
}

// outputsReader reads the outputs results of one Pod's agent container.
type outputsReader func(podName string) map[string]string

// usageAttempts returns one UsageAttempt per agent Pod of the Task's Job,
// oldest first. latestPod's results are the ones already read; the other
// Pods' logs are read through read. Returns nil for agent types whose
// usage Kelos does not normalize.
func usageAttempts(task *kelos.Task, job *batchv1.Job, pods []corev1.Pod, latestPod string, latestResults map[string]string, read outputsReader) []kelos.UsageAttempt {
	if !normalizedUsageAgents[resolveTaskType(task)] {
		return nil
	}
	owned := attemptPods(job, pods)
	dropped := false
	if len(owned) > maxUsageAttempts {
		owned = owned[len(owned)-maxUsageAttempts:]
		dropped = true
	}
	attempts := make([]kelos.UsageAttempt, 0, len(owned))
	for _, pod := range owned {
		results := latestResults
		if pod.Name != latestPod {
			results = read(pod.Name)
		}
		attempts = append(attempts, kelos.UsageAttempt{
			PodName:   pod.Name,
			PodUID:    pod.UID,
			Breakdown: breakdownFromResults(results),
		})
	}
	if dropped && len(attempts) > 0 {
		addUsageNote(&attempts[0].Breakdown, noteEarlierAttemptsDropped)
	}
	return attempts
}

// attemptPods returns the Pods the Job controls (all of them when job is
// nil), sorted by creation time then name.
func attemptPods(job *batchv1.Job, pods []corev1.Pod) []corev1.Pod {
	var owned []corev1.Pod
	for _, pod := range pods {
		if job != nil && !metav1.IsControlledBy(&pod, job) {
			continue
		}
		owned = append(owned, pod)
	}
	sort.SliceStable(owned, func(i, j int) bool {
		left, right := owned[i], owned[j]
		if left.CreationTimestamp.Time.Equal(right.CreationTimestamp.Time) {
			return left.Name < right.Name
		}
		return left.CreationTimestamp.Time.Before(right.CreationTimestamp.Time)
	})
	return owned
}

// breakdownFromResults decodes the usage-breakdown result. An attempt that
// printed none, or an unreadable one, becomes coverage Missing with a note:
// absent usage is recorded as unknown, never as zero.
func breakdownFromResults(results map[string]string) kelos.UsageBreakdown {
	raw, ok := results[usageBreakdownResultKey]
	if !ok {
		return missingBreakdown(noteAttemptUsageUnavailable)
	}
	var b kelos.UsageBreakdown
	if err := json.NewDecoder(bytes.NewReader([]byte(raw))).Decode(&b); err != nil {
		return missingBreakdown(noteBreakdownUnparseable)
	}
	if b.SchemaVersion < 1 || !validCoverage(b.Coverage) || !validModelSources(b.Models) || invalidCost(&b) {
		return missingBreakdown(noteBreakdownUnparseable)
	}
	clampBreakdown(&b)
	return b
}

func missingBreakdown(note string) kelos.UsageBreakdown {
	return kelos.UsageBreakdown{
		SchemaVersion: kelos.UsageBreakdownSchemaVersion,
		Coverage:      kelos.UsageCoverageMissing,
		Notes:         []string{note},
	}
}

func validCoverage(c kelos.UsageCoverage) bool {
	switch c {
	case kelos.UsageCoverageComplete, kelos.UsageCoveragePartial, kelos.UsageCoverageMissing:
		return true
	}
	return false
}

func validModelSources(models []kelos.ModelUsage) bool {
	for _, m := range models {
		switch m.ModelSource {
		case "", kelos.ModelSourceReported, kelos.ModelSourceConfigured:
		default:
			return false
		}
		for _, v := range []*int64{m.UncachedInputTokens, m.CacheReadInputTokens, m.CacheWriteInputTokens,
			m.CacheWrite5mInputTokens, m.CacheWrite1hInputTokens, m.OutputTokens,
			m.ReasoningOutputTokens, m.WebSearchRequests} {
			if v != nil && *v < 0 {
				return false
			}
		}
		for _, v := range m.OtherCounters {
			if v < 0 {
				return false
			}
		}
	}
	return true
}

// usageCostPattern mirrors the reportedCostUSD CRD pattern.
var usageCostPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// maxUsageCostLength mirrors the reportedCostUSD CRD MaxLength.
const maxUsageCostLength = 40

// invalidCost reports a cost the CRD would refuse.
func invalidCost(b *kelos.UsageBreakdown) bool {
	costs := []string{b.ReportedCostUSD}
	for _, m := range b.Models {
		costs = append(costs, m.ReportedCostUSD)
	}
	for _, c := range costs {
		if c != "" && (len(c) > maxUsageCostLength || !usageCostPattern.MatchString(c)) {
			return true
		}
	}
	return false
}

// clampBreakdown trims lists to the CRD limits. Dropping models or
// subagents loses usage, so it lowers Complete to Partial.
func clampBreakdown(b *kelos.UsageBreakdown) {
	truncated := false
	if len(b.Models) > maxUsageModels {
		b.Models = b.Models[:maxUsageModels]
		truncated = true
	}
	if len(b.Subagents) > maxUsageSubagents {
		b.Subagents = b.Subagents[:maxUsageSubagents]
		truncated = true
	}
	for i := range b.Models {
		if len(b.Models[i].OtherCounters) > maxUsageOtherCounters {
			b.Models[i].OtherCounters = firstCounters(b.Models[i].OtherCounters, maxUsageOtherCounters)
			truncated = true
		}
	}
	var kept []string
	for _, note := range b.Notes {
		if len(note) > maxUsageNoteLength {
			note = note[:maxUsageNoteLength]
		}
		kept = append(kept, note)
	}
	if len(kept) > maxUsageNotes {
		kept = kept[:maxUsageNotes]
		truncated = true
	}
	b.Notes = kept
	if truncated {
		addUsageNote(b, noteBreakdownTruncated)
	}
}

// addUsageNote appends note (keeping the list within its limit by
// replacing the last note), and lowers Complete coverage to Partial.
func addUsageNote(b *kelos.UsageBreakdown, note string) {
	for _, existing := range b.Notes {
		if existing == note {
			return
		}
	}
	if len(b.Notes) >= maxUsageNotes {
		b.Notes = b.Notes[:maxUsageNotes-1]
	}
	b.Notes = append(b.Notes, note)
	if b.Coverage == kelos.UsageCoverageComplete {
		b.Coverage = kelos.UsageCoveragePartial
	}
}

func firstCounters(m map[string]int64, n int) map[string]int64 {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]int64, n)
	for _, k := range keys[:n] {
		out[k] = m[k]
	}
	return out
}

// withAttempts attaches attempts to usage, creating it when the legacy
// totals were absent so that a Task whose attempts reported no usage still
// gets a TaskRecord saying so.
func withAttempts(usage *kelos.TaskUsage, attempts []kelos.UsageAttempt) *kelos.TaskUsage {
	if len(attempts) == 0 {
		return usage
	}
	if usage == nil {
		usage = &kelos.TaskUsage{}
	}
	usage.Attempts = attempts
	return usage
}

// podOutputsReader adapts readOutputs to an outputsReader for the Task's
// namespace and agent container.
func (r *TaskReconciler) podOutputsReader(ctx context.Context, namespace string) outputsReader {
	return func(podName string) map[string]string {
		_, results := r.readOutputs(ctx, namespace, podName, kelos.AgentContainerName)
		return results
	}
}
