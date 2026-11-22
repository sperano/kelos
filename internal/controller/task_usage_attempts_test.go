package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kelos "github.com/kelos-dev/kelos/api/v1alpha2"
)

const captureUsageFixtures = "../capture/testdata/usage"

func int64p(v int64) *int64 { return &v }

// TestBreakdownDecodesEveryCaptureFixture guards the wire contract: every
// expected breakdown of the capture fixtures decodes into the API type
// with no field left over, and re-encodes to the same JSON.
func TestBreakdownDecodesEveryCaptureFixture(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(captureUsageFixtures, "*", "*", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no capture fixtures found: %v", err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		var b kelos.UsageBreakdown
		if err := dec.Decode(&b); err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		var want, got any
		if err := json.Unmarshal(data, &want); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: round trip\n got: %s\nwant: %s", file, encoded, data)
		}
	}
}

func TestBreakdownFromResults(t *testing.T) {
	valid := `{"schemaVersion":1,"runner":"codex","sessionID":"th-1","final":true,"coverage":"Partial","notes":["subagent-usage-not-reported"],"subagents":[{"id":"th-2"}],"models":[{"model":"gpt-5.5","modelSource":"Configured","uncachedInputTokens":3000,"cacheReadInputTokens":8000,"outputTokens":900,"reasoningOutputTokens":300,"webSearchRequests":1}]}`
	tests := []struct {
		name    string
		results map[string]string
		want    kelos.UsageBreakdown
	}{
		{
			name:    "no breakdown key",
			results: map[string]string{"cost-usd": "1"},
			want:    missingBreakdown(noteAttemptUsageUnavailable),
		},
		{
			name: "nil results",
			want: missingBreakdown(noteAttemptUsageUnavailable),
		},
		{
			name:    "not JSON",
			results: map[string]string{usageBreakdownResultKey: "{oops"},
			want:    missingBreakdown(noteBreakdownUnparseable),
		},
		{
			name:    "unknown coverage",
			results: map[string]string{usageBreakdownResultKey: `{"schemaVersion":1,"coverage":"Mostly"}`},
			want:    missingBreakdown(noteBreakdownUnparseable),
		},
		{
			name:    "missing schema version",
			results: map[string]string{usageBreakdownResultKey: `{"coverage":"Complete"}`},
			want:    missingBreakdown(noteBreakdownUnparseable),
		},
		{
			name:    "negative counter",
			results: map[string]string{usageBreakdownResultKey: `{"schemaVersion":1,"coverage":"Complete","models":[{"outputTokens":-5}]}`},
			want:    missingBreakdown(noteBreakdownUnparseable),
		},
		{
			name:    "unknown model source",
			results: map[string]string{usageBreakdownResultKey: `{"schemaVersion":1,"coverage":"Complete","models":[{"model":"m","modelSource":"Guessed"}]}`},
			want:    missingBreakdown(noteBreakdownUnparseable),
		},
		{
			name:    "valid breakdown",
			results: map[string]string{usageBreakdownResultKey: valid},
			want: kelos.UsageBreakdown{
				SchemaVersion: 1,
				Runner:        "codex",
				SessionID:     "th-1",
				Final:         true,
				Coverage:      kelos.UsageCoveragePartial,
				Notes:         []string{"subagent-usage-not-reported"},
				Subagents:     []kelos.SubagentUsageRef{{ID: "th-2"}},
				Models: []kelos.ModelUsage{{
					Model:                 "gpt-5.5",
					ModelSource:           kelos.ModelSourceConfigured,
					UncachedInputTokens:   int64p(3000),
					CacheReadInputTokens:  int64p(8000),
					OutputTokens:          int64p(900),
					ReasoningOutputTokens: int64p(300),
					WebSearchRequests:     int64p(1),
				}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := breakdownFromResults(tt.results)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("breakdownFromResults()\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

func TestBreakdownFromResultsKeepsCost(t *testing.T) {
	got := breakdownFromResults(map[string]string{usageBreakdownResultKey: `{"schemaVersion":1,"coverage":"Complete","reportedCostUSD":"0.3125","costSource":"claude-code","models":[{"model":"claude-opus-5-5","reportedCostUSD":"0.2625"}]}`})
	if got.ReportedCostUSD != "0.3125" || got.Models[0].ReportedCostUSD != "0.2625" {
		t.Errorf("costs = %q / %q, want 0.3125 / 0.2625", got.ReportedCostUSD, got.Models[0].ReportedCostUSD)
	}
}

func TestBreakdownFromResultsRejectsCostsTheCRDRefuses(t *testing.T) {
	for _, cost := range []string{"-1", "1e-05", "500m", ".5", "01", "0." + strings.Repeat("1", maxUsageCostLength)} {
		for _, raw := range []string{
			fmt.Sprintf(`{"schemaVersion":1,"coverage":"Complete","reportedCostUSD":%q}`, cost),
			fmt.Sprintf(`{"schemaVersion":1,"coverage":"Complete","models":[{"model":"m","reportedCostUSD":%q}]}`, cost),
		} {
			got := breakdownFromResults(map[string]string{usageBreakdownResultKey: raw})
			if !reflect.DeepEqual(got, missingBreakdown(noteBreakdownUnparseable)) {
				t.Errorf("cost %q in %s: got %+v, want Missing unparseable", cost, raw, got)
			}
		}
	}
}

func TestBreakdownFromResultsTrimsToCRDLimits(t *testing.T) {
	var models, subagents, notes []string
	for i := 0; i < maxUsageModels+1; i++ {
		models = append(models, fmt.Sprintf(`{"model":"m%02d","outputTokens":1}`, i))
	}
	for i := 0; i < maxUsageSubagents+1; i++ {
		subagents = append(subagents, fmt.Sprintf(`{"id":"s%02d"}`, i))
	}
	for i := 0; i < maxUsageNotes+1; i++ {
		notes = append(notes, fmt.Sprintf(`"n%02d"`, i))
	}
	notes[0] = `"` + strings.Repeat("x", maxUsageNoteLength+10) + `"`
	raw := fmt.Sprintf(`{"schemaVersion":1,"coverage":"Complete","notes":[%s],"subagents":[%s],"models":[%s]}`,
		strings.Join(notes, ","), strings.Join(subagents, ","), strings.Join(models, ","))

	got := breakdownFromResults(map[string]string{usageBreakdownResultKey: raw})
	if len(got.Models) != maxUsageModels || got.Models[maxUsageModels-1].Model != "m31" {
		t.Errorf("models = %d (last %q), want the first %d", len(got.Models), got.Models[len(got.Models)-1].Model, maxUsageModels)
	}
	if len(got.Subagents) != maxUsageSubagents {
		t.Errorf("subagents = %d, want %d", len(got.Subagents), maxUsageSubagents)
	}
	if len(got.Notes) != maxUsageNotes {
		t.Fatalf("notes = %d, want %d", len(got.Notes), maxUsageNotes)
	}
	if got.Notes[0] != strings.Repeat("x", maxUsageNoteLength) {
		t.Errorf("first note = %q, want it cut to %d bytes", got.Notes[0], maxUsageNoteLength)
	}
	if last := got.Notes[maxUsageNotes-1]; last != noteBreakdownTruncated {
		t.Errorf("last note = %q, want %q", last, noteBreakdownTruncated)
	}
	if got.Coverage != kelos.UsageCoveragePartial {
		t.Errorf("coverage = %q, want Partial after dropping usage", got.Coverage)
	}
}

func TestBreakdownFromResultsTrimsOtherCounters(t *testing.T) {
	var counters []string
	for i := 0; i < maxUsageOtherCounters+2; i++ {
		counters = append(counters, fmt.Sprintf(`"c%02d":%d`, i, i))
	}
	raw := fmt.Sprintf(`{"schemaVersion":1,"coverage":"Complete","models":[{"model":"m","otherCounters":{%s}}]}`, strings.Join(counters, ","))
	got := breakdownFromResults(map[string]string{usageBreakdownResultKey: raw})
	kept := got.Models[0].OtherCounters
	if len(kept) != maxUsageOtherCounters {
		t.Fatalf("otherCounters = %d, want %d", len(kept), maxUsageOtherCounters)
	}
	if _, ok := kept["c33"]; ok {
		t.Error("otherCounters kept c33, want the first keys in order")
	}
	if !reflect.DeepEqual(got.Notes, []string{noteBreakdownTruncated}) || got.Coverage != kelos.UsageCoveragePartial {
		t.Errorf("notes = %v coverage = %q, want [%s] Partial", got.Notes, got.Coverage, noteBreakdownTruncated)
	}
}

func attemptTestJob() *batchv1.Job {
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: "default", UID: "job-uid"}}
}

func attemptTestPod(name string, uid types.UID, created time.Time, job *batchv1.Job) corev1.Pod {
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: "default", UID: uid,
		CreationTimestamp: metav1.NewTime(created),
	}}
	if job != nil {
		controller := true
		pod.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID, Controller: &controller,
		}}
	}
	return pod
}

func TestUsageAttemptsReadsEveryOwnedPodOnce(t *testing.T) {
	job := attemptTestJob()
	base := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	retry := attemptTestPod("t1-b", "uid-b", base.Add(time.Minute), job)
	first := attemptTestPod("t1-a", "uid-a", base, job)
	stale := attemptTestPod("t1-old", "uid-old", base.Add(-time.Hour), &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "t1", UID: "other-job"}})
	task := &kelos.Task{Spec: kelos.TaskSpec{Type: "claude-code"}}

	firstResults := map[string]string{usageBreakdownResultKey: `{"schemaVersion":1,"runner":"claude-code","coverage":"Missing","notes":["no-final-result"]}`}
	latestResults := map[string]string{usageBreakdownResultKey: `{"schemaVersion":1,"runner":"claude-code","final":true,"coverage":"Complete","models":[{"model":"claude-opus-5-5","modelSource":"Reported","outputTokens":10}]}`}
	var read []string
	reader := func(pod string) map[string]string {
		read = append(read, pod)
		if pod == "t1-a" {
			return firstResults
		}
		return nil
	}

	got := usageAttempts(task, job, []corev1.Pod{retry, stale, first}, "t1-b", latestResults, reader)

	if !reflect.DeepEqual(read, []string{"t1-a"}) {
		t.Errorf("logs read = %v, want only the earlier attempt t1-a", read)
	}
	want := []kelos.UsageAttempt{
		{PodName: "t1-a", PodUID: "uid-a", Breakdown: kelos.UsageBreakdown{
			SchemaVersion: 1, Runner: "claude-code", Coverage: kelos.UsageCoverageMissing, Notes: []string{"no-final-result"},
		}},
		{PodName: "t1-b", PodUID: "uid-b", Breakdown: kelos.UsageBreakdown{
			SchemaVersion: 1, Runner: "claude-code", Final: true, Coverage: kelos.UsageCoverageComplete,
			Models: []kelos.ModelUsage{{Model: "claude-opus-5-5", ModelSource: kelos.ModelSourceReported, OutputTokens: int64p(10)}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("usageAttempts()\n got: %+v\nwant: %+v", got, want)
	}
}

func TestUsageAttemptsUnreadableAttemptIsMissing(t *testing.T) {
	job := attemptTestJob()
	base := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	pods := []corev1.Pod{attemptTestPod("t1-a", "uid-a", base, job), attemptTestPod("t1-b", "uid-b", base.Add(time.Minute), job)}
	task := &kelos.Task{Spec: kelos.TaskSpec{Worker: &kelos.WorkerSpec{Type: "opencode"}}}

	got := usageAttempts(task, job, pods, "t1-b", nil, func(string) map[string]string { return nil })

	want := missingBreakdown(noteAttemptUsageUnavailable)
	if len(got) != 2 || !reflect.DeepEqual(got[0].Breakdown, want) || !reflect.DeepEqual(got[1].Breakdown, want) {
		t.Errorf("usageAttempts() = %+v, want two Missing attempts", got)
	}
}

func TestUsageAttemptsSkipsAgentsWithoutBreakdown(t *testing.T) {
	job := attemptTestJob()
	pods := []corev1.Pod{attemptTestPod("t1-a", "uid-a", time.Now(), job)}
	for _, agent := range []string{"gemini", "cursor", ""} {
		task := &kelos.Task{Spec: kelos.TaskSpec{Type: agent}}
		if got := usageAttempts(task, job, pods, "t1-a", nil, func(string) map[string]string { return nil }); got != nil {
			t.Errorf("%q: usageAttempts() = %+v, want nil", agent, got)
		}
	}
}

func TestUsageAttemptsKeepsNewestWithinLimit(t *testing.T) {
	job := attemptTestJob()
	base := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	var pods []corev1.Pod
	for i := 0; i < maxUsageAttempts+2; i++ {
		pods = append(pods, attemptTestPod(fmt.Sprintf("t1-%02d", i), types.UID(fmt.Sprintf("uid-%02d", i)), base.Add(time.Duration(i)*time.Minute), job))
	}
	task := &kelos.Task{Spec: kelos.TaskSpec{Type: "codex"}}
	complete := map[string]string{usageBreakdownResultKey: `{"schemaVersion":1,"coverage":"Complete"}`}

	got := usageAttempts(task, job, pods, "t1-11", complete, func(string) map[string]string { return complete })

	if len(got) != maxUsageAttempts {
		t.Fatalf("attempts = %d, want %d", len(got), maxUsageAttempts)
	}
	if got[0].PodName != "t1-02" || got[maxUsageAttempts-1].PodName != "t1-11" {
		t.Errorf("attempts span %s..%s, want t1-02..t1-11", got[0].PodName, got[maxUsageAttempts-1].PodName)
	}
	if !reflect.DeepEqual(got[0].Breakdown.Notes, []string{noteEarlierAttemptsDropped}) || got[0].Breakdown.Coverage != kelos.UsageCoveragePartial {
		t.Errorf("oldest kept attempt = %+v, want the dropped-attempts note and Partial", got[0].Breakdown)
	}
	if got[1].Breakdown.Coverage != kelos.UsageCoverageComplete {
		t.Errorf("second attempt coverage = %q, want Complete", got[1].Breakdown.Coverage)
	}
}

func TestWithAttempts(t *testing.T) {
	attempts := []kelos.UsageAttempt{{PodName: "p", Breakdown: missingBreakdown(noteAttemptUsageUnavailable)}}
	if got := withAttempts(nil, nil); got != nil {
		t.Errorf("withAttempts(nil, nil) = %+v, want nil", got)
	}
	got := withAttempts(nil, attempts)
	if got == nil || got.CostUSD != nil || !reflect.DeepEqual(got.Attempts, attempts) {
		t.Errorf("withAttempts(nil, attempts) = %+v, want usage holding only the attempts", got)
	}
	legacy := &kelos.TaskUsage{CostUSD: mustQuantity("1")}
	got = withAttempts(legacy, attempts)
	if got.CostUSD.Cmp(*mustQuantity("1")) != 0 || !reflect.DeepEqual(got.Attempts, attempts) {
		t.Errorf("withAttempts(legacy, attempts) = %+v, want legacy cost kept and attempts added", got)
	}
}

func TestCreateTaskRecordRetainsAttempts(t *testing.T) {
	scheme := newWorkerPoolTestScheme()
	attempts := []kelos.UsageAttempt{{PodName: "t1-a", PodUID: "uid-a", Breakdown: kelos.UsageBreakdown{
		SchemaVersion: 1, Runner: "claude-code", Final: true, Coverage: kelos.UsageCoverageComplete, IncludesSubagents: true,
		Models: []kelos.ModelUsage{{Model: "claude-opus-5-5", ModelSource: kelos.ModelSourceReported, OutputTokens: int64p(190)}},
	}}}
	task := &kelos.Task{
		ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: "default", UID: "uid-1"},
		Spec:       kelos.TaskSpec{Type: "claude-code"},
		Status: kelos.TaskStatus{
			Phase:          kelos.TaskPhaseSucceeded,
			CompletionTime: nowTime(time.Now()),
			Usage:          &kelos.TaskUsage{Attempts: attempts},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(task).Build()
	e := &budgetEnforcer{Client: cl, now: time.Now}
	if err := e.createTaskRecord(context.Background(), task); err != nil {
		t.Fatalf("createTaskRecord: %v", err)
	}
	var records kelos.TaskRecordList
	if err := cl.List(context.Background(), &records); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records.Items) != 1 || records.Items[0].Spec.Usage == nil || !reflect.DeepEqual(records.Items[0].Spec.Usage.Attempts, attempts) {
		t.Errorf("records = %+v, want one record carrying the attempts", records.Items)
	}
}
