package recommender

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/inferLean/inferlean-project/internal/model"
)

const (
	corpusCalibrationMinScore   = 0.70
	corpusPreciseMinScore       = 0.82
	corpusSelectionMaxDistance  = 0.35
	defaultLongPrefillThreshold = 2048
)

type issueCandidate struct {
	Finding  model.Finding
	Headline string
}

type ruleBuildContext struct {
	report    *model.AnalysisReport
	derived   derivedContext
	objective Objective
	baseline  *model.Prediction
	present   map[string]bool
}

type corpusCalibration struct {
	Match              profileMatch
	Baseline           *measurementSelection
	BaselinePrediction *model.Prediction
	Precise            bool
}

func collectIssueCandidates(summary *model.AnalysisSummary) []issueCandidate {
	if summary == nil || len(summary.Findings) == 0 {
		return nil
	}
	issues := make([]issueCandidate, 0, len(summary.Findings))
	for _, finding := range summary.Findings {
		if finding.Status != model.FindingStatusPresent {
			continue
		}
		issues = append(issues, issueCandidate{
			Finding:  finding,
			Headline: recommendationIssueHeadline(finding),
		})
	}
	sort.SliceStable(issues, func(i, j int) bool {
		ri := issues[i].Finding.Rank
		rj := issues[j].Finding.Rank
		if ri <= 0 {
			ri = 1_000_000 + i
		}
		if rj <= 0 {
			rj = 1_000_000 + j
		}
		if ri != rj {
			return ri < rj
		}
		if issues[i].Finding.ImportanceScore != issues[j].Finding.ImportanceScore {
			return issues[i].Finding.ImportanceScore > issues[j].Finding.ImportanceScore
		}
		return issues[i].Finding.ID < issues[j].Finding.ID
	})
	return issues
}

func recommendationIssueHeadline(finding model.Finding) string {
	switch finding.ID {
	case "queue_dominated_ttft":
		return "Queue-heavy TTFT hurts responsiveness"
	case "throughput_saturation_with_queue_pressure":
		return "Queue pressure is limiting throughput"
	case "underutilized_gpu_or_conservative_batching":
		return "Conservative batching leaves GPU headroom unused"
	case "kv_cache_pressure_preemptions":
		return "KV cache preemptions increase tail latency"
	case "prefix_cache_ineffective":
		return "Low prefix-cache hit rate inflates prefill cost"
	case "prompt_recomputation_thrashing":
		return "Prompt recomputation adds avoidable latency"
	case "prefill_heavy_workload":
		return "Prefill-heavy traffic dominates end-to-end latency"
	case "decode_bound_generation":
		return "Decode path is the dominant generation bottleneck"
	case "cpu_or_host_bottleneck":
		return "CPU or host constraints throttle GPU throughput"
	case "gpu_memory_saturation_without_throughput":
		return "GPU memory saturation caps throughput gains"
	case "gpu_hardware_instability":
		return "GPU hardware instability signals were detected"
	default:
		if strings.TrimSpace(finding.Summary) != "" {
			return strings.TrimSpace(finding.Summary)
		}
		return humanizeIdentifier(finding.ID)
	}
}

func buildIssueRecommendations(report *model.AnalysisReport, derived derivedContext, objective Objective, baseline *model.Prediction) []model.RecommendationItem {
	issues := collectIssueCandidates(report.AnalysisSummary)
	if len(issues) == 0 {
		return nil
	}
	ctx := ruleBuildContext{
		report:    report,
		derived:   derived,
		objective: objective,
		baseline:  baseline,
		present:   presentFindings(report.AnalysisSummary),
	}
	recommendations := make([]model.RecommendationItem, 0, len(issues))
	for _, issue := range issues {
		item := buildRuleRecommendation(issue, ctx)
		item.Priority = len(recommendations) + 1
		item.SharedActionID = buildSharedActionID(item)
		recommendations = append(recommendations, item)
	}
	return recommendations
}

func buildRuleRecommendation(issue issueCandidate, ctx ruleBuildContext) model.RecommendationItem {
	switch issue.Finding.ID {
	case "underutilized_gpu_or_conservative_batching":
		return buildUnderutilizedRecommendation(issue, ctx)
	case "queue_dominated_ttft":
		return buildQueueDominatedRecommendation(issue, ctx)
	case "kv_cache_pressure_preemptions":
		return buildMemoryPressureRecommendation(issue, ctx, true)
	case "prompt_recomputation_thrashing":
		return buildMemoryPressureRecommendation(issue, ctx, false)
	case "prefix_cache_ineffective":
		return buildPrefixCacheRecommendation(issue, ctx)
	case "prefill_heavy_workload":
		return buildPrefillHeavyRecommendation(issue, ctx)
	case "cpu_or_host_bottleneck":
		return buildOperationalRecommendation(
			issue,
			ctx.objective,
			"Increase CPU resources or reduce host-side work before tuning GPU batching further.",
			0.78,
			"Rule-based operational recommendation from host bottleneck evidence and current CPU-side pressure.",
			"Keep async scheduling enabled and consider a larger stream_interval only for throughput-oriented streaming workloads after CPU headroom is restored.",
			"Replay the workload and confirm GPU utilization rises after CPU contention is reduced.",
		)
	case "throughput_saturation_with_queue_pressure":
		return buildOperationalRecommendation(
			issue,
			ctx.objective,
			"Add serving capacity before changing batching knobs; current GPUs are already saturated and queue pressure remains high.",
			0.84,
			"Rule-based operational recommendation from sustained queue pressure under high serving saturation.",
			"Do not increase max_num_seqs or max_num_batched_tokens first when the current bottleneck is already saturated GPU capacity.",
			"Scale capacity and confirm waiting requests and queue delay fall on the same traffic profile.",
		)
	case "decode_bound_generation":
		return buildOperationalRecommendation(
			issue,
			ctx.objective,
			"Treat decode throughput as the bottleneck; validate model size, requested output length, and optional speculative decoding before changing scheduler limits.",
			0.60,
			"Rule-based operational recommendation from decode-bound generation evidence.",
			"Aggressive batching can worsen inter-token latency when decode is already dominant.",
			"Compare inter-token latency and output token rate after any decode-path change.",
		)
	case "gpu_memory_saturation_without_throughput":
		return buildOperationalRecommendation(
			issue,
			ctx.objective,
			"Reduce memory footprint or change parallelism before raising concurrency; memory is saturated without proportional throughput gain.",
			0.74,
			"Rule-based operational recommendation from GPU memory saturation without throughput evidence.",
			"Raising concurrency on a memory-bound setup can increase churn and preemptions without improving throughput.",
			"Confirm memory pressure and preemptions fall before retrying throughput-oriented batching increases.",
		)
	case "gpu_hardware_instability":
		return buildOperationalRecommendation(
			issue,
			ctx.objective,
			"Stabilize the GPU, driver, or host before applying tuning changes; XID-like failures make performance conclusions unreliable.",
			0.92,
			"Rule-based operational recommendation from hardware instability evidence.",
			"Do not treat tuning results as valid until the hardware fault path is resolved.",
			"Verify the instability signal disappears on the next collection window before retuning.",
		)
	case "text_only_workload_on_multimodal_stack":
		return buildTextOnlyMultimodalRecommendation(issue, ctx)
	default:
		return buildOperationalRecommendation(
			issue,
			ctx.objective,
			fmt.Sprintf("Address %s before applying broader tuning changes.", strings.ToLower(issue.Headline)),
			0.45,
			fmt.Sprintf("Generic operational fallback for analyzer issue %s.", issue.Finding.ID),
			"No deterministic parameter change was synthesized for this issue.",
			"Re-run the same workload after the issue is addressed and compare the ranked findings.",
		)
	}
}

func buildUnderutilizedRecommendation(issue issueCandidate, ctx ruleBuildContext) model.RecommendationItem {
	changes := recommendedHeadroomBatchingChanges(ctx)
	if len(changes) == 0 {
		return buildOperationalRecommendation(
			issue,
			ctx.objective,
			"Raise batching and concurrency conservatively once the effective vLLM config is available; current signals show unused GPU headroom.",
			0.56,
			"Rule-based operational fallback from underutilization evidence because the current batching knobs were unavailable.",
			"Keep the batching and concurrency change set coupled rather than changing only one knob.",
			"Confirm GPU utilization rises without queue time becoming the dominant latency component.",
		)
	}

	throughputDelta := clampFloat(math.Max(issue.Finding.HeuristicImprovementPct*0.60, 10), 10, 22)
	ttftDelta := 4.0
	p50Delta := 6.0
	p95Delta := 8.0
	gpuDelta := clampFloat(throughputDelta*0.75, 8, 18)
	if normalizeObjective(ctx.objective) == LatencyFirstObjective {
		throughputDelta = clampFloat(throughputDelta*0.65, 6, 14)
		ttftDelta = 2
		p50Delta = 3
		p95Delta = 4
		gpuDelta = clampFloat(gpuDelta*0.65, 5, 10)
	}
	return bindIssue(issue, model.RecommendationItem{
		ID:                   "rule_underutilized_gpu",
		Objective:            string(ctx.objective),
		ActionKind:           model.RecommendationActionKindParameterChange,
		RecommendationSource: model.RecommendationSourceRule,
		Summary:              conciseRuleSummary("Increase batching and scheduler concurrency to use available GPU headroom", changes),
		Changes:              changes,
		PredictedEffect:      applyFallbackEffect(ctx.baseline, throughputDelta, ttftDelta, p50Delta, p95Delta, gpuDelta),
		Confidence:           clampFloat(0.58+(issue.Finding.Confidence*0.20), 0.58, 0.82),
		SafetyNotes: []string{
			"Apply the batching and concurrency changes together, then replay the same workload before widening the step size.",
		},
		ValidationChecks: []string{
			"Confirm GPU utilization rises while queue time stays secondary to prefill and decode time.",
			"Check that TTFT and p95 remain inside the guardrail for the selected optimization objective.",
		},
		Basis: "Rule-based batching increase from underutilization evidence and current low effective GPU load.",
	})
}

func buildQueueDominatedRecommendation(issue issueCandidate, ctx ruleBuildContext) model.RecommendationItem {
	if currentHeadroomLoadPct(ctx) < 65 {
		changes := recommendedHeadroomBatchingChanges(ctx)
		if len(changes) > 0 {
			return bindIssue(issue, model.RecommendationItem{
				ID:                   "rule_queue_dominated_ttft",
				Objective:            string(ctx.objective),
				ActionKind:           model.RecommendationActionKindParameterChange,
				RecommendationSource: model.RecommendationSourceRule,
				Summary:              conciseRuleSummary("Increase admission headroom to reduce queue-heavy TTFT", changes),
				Changes:              changes,
				PredictedEffect:      applyFallbackEffect(ctx.baseline, 10, -12, -6, -9, 8),
				Confidence:           clampFloat(0.55+(issue.Finding.Confidence*0.18), 0.55, 0.78),
				SafetyNotes: []string{
					"Use a moderate step size for latency-sensitive traffic and stop increasing batching if queue time stops improving.",
				},
				ValidationChecks: []string{
					"Confirm average queue time and TTFT both drop on the same traffic profile.",
					"Verify GPU utilization moves up rather than queueing moving to another bottleneck.",
				},
				Basis: "Rule-based queue relief from queue-dominated TTFT evidence with remaining GPU headroom.",
			})
		}
	}

	return buildOperationalRecommendation(
		issue,
		ctx.objective,
		"Reduce scheduler delay by adding serving headroom or by protecting shorter prefills before increasing latency-sensitive traffic.",
		0.66,
		"Rule-based operational recommendation from queue-dominated TTFT evidence without a safe exact batching change.",
		"If GPU saturation is already high, further batching increases can worsen tail latency instead of relieving the queue.",
		"Re-run the same workload and verify queue time is no longer the dominant contributor to TTFT.",
	)
}

func buildMemoryPressureRecommendation(issue issueCandidate, ctx ruleBuildContext, preferMemoryLimit bool) model.RecommendationItem {
	changes := recommendedMemoryPressureChanges(ctx, preferMemoryLimit)
	if len(changes) == 0 {
		return buildOperationalRecommendation(
			issue,
			ctx.objective,
			"Relieve KV-cache pressure with more memory headroom or a different parallelism plan before increasing concurrency.",
			0.70,
			"Rule-based operational recommendation from memory pressure evidence because no safe exact parameter change was available.",
			"Do not widen concurrency while preemptions or prompt recomputation remain active.",
			"Confirm preemptions and recomputed prompt tokens drop after the memory-pressure change.",
		)
	}

	actionSummary := "Reduce KV-cache churn and prompt recomputation"
	basis := "Rule-based memory-pressure correction from KV-cache pressure and recomputation evidence."
	throughputDelta := 6.0
	ttftDelta := -10.0
	p50Delta := -8.0
	p95Delta := -18.0
	gpuDelta := 4.0
	if hasNumericChange(changes, "gpu_memory_utilization") {
		actionSummary = "Increase usable KV-cache headroom before reducing concurrency"
		throughputDelta = 8
		gpuDelta = 6
	} else {
		throughputDelta = 4
		gpuDelta = 2
	}

	return bindIssue(issue, model.RecommendationItem{
		ID:                   "rule_" + issue.Finding.ID,
		Objective:            string(ctx.objective),
		ActionKind:           model.RecommendationActionKindParameterChange,
		RecommendationSource: model.RecommendationSourceRule,
		Summary:              conciseRuleSummary(actionSummary, changes),
		Changes:              changes,
		PredictedEffect:      applyFallbackEffect(ctx.baseline, throughputDelta, ttftDelta, p50Delta, p95Delta, gpuDelta),
		Confidence:           clampFloat(0.60+(issue.Finding.Confidence*0.22), 0.60, 0.84),
		SafetyNotes: []string{
			"Prefer a single memory-pressure change per rollout so you can see whether preemptions and recomputation actually fall.",
		},
		ValidationChecks: []string{
			"Confirm num_preemptions and prompt_tokens_recomputed both fall on the same workload window.",
			"Watch p95 latency first; throughput gains are secondary until the churn is controlled.",
		},
		Basis: basis,
	})
}

func buildPrefixCacheRecommendation(issue issueCandidate, ctx ruleBuildContext) model.RecommendationItem {
	flat := flattenMap(ctx.derived.CurrentConfig)
	if raw, ok := lookupAny(flat, "enable_prefix_caching"); ok {
		if enabled, ok := coerceBool(raw); ok && !enabled {
			change := model.ParameterChange{
				Name:             "enable_prefix_caching",
				CurrentValue:     raw,
				RecommendedValue: true,
			}
			return bindIssue(issue, model.RecommendationItem{
				ID:                   "rule_enable_prefix_caching",
				Objective:            string(ctx.objective),
				ActionKind:           model.RecommendationActionKindParameterChange,
				RecommendationSource: model.RecommendationSourceRule,
				Summary:              conciseRuleSummary("Enable prefix caching for repeated prompt prefixes", []model.ParameterChange{change}),
				Changes:              []model.ParameterChange{change},
				PredictedEffect:      applyFallbackEffect(ctx.baseline, 10, -8, -6, -8, 4),
				Confidence:           clampFloat(0.66+(issue.Finding.Confidence*0.18), 0.66, 0.84),
				SafetyNotes: []string{
					"Enable prefix caching first, then replay the same prompt mix before combining it with batch-size changes.",
				},
				ValidationChecks: []string{
					"Confirm prefix_cache_hits rises and average prefill time drops on the same workload.",
				},
				Basis: "Rule-based prefix-cache enablement from ineffective prefix reuse evidence and the current disabled cache setting.",
			})
		}
	}

	return buildOperationalRecommendation(
		issue,
		ctx.objective,
		"Normalize prompt templates and request routing to improve prefix reuse; prefix caching is already enabled or the config snapshot is incomplete.",
		0.62,
		"Rule-based operational recommendation from ineffective prefix-cache evidence without a safe exact toggle change.",
		"Low cache hit rate with caching already enabled usually means the prompt structure or request mix is fragmenting reuse.",
		"Confirm prefix cache hits increase before attributing any latency change to another tuning action.",
	)
}

func buildTextOnlyMultimodalRecommendation(issue issueCandidate, ctx ruleBuildContext) model.RecommendationItem {
	flat := flattenMap(ctx.derived.CurrentConfig)
	if raw, ok := lookupAny(flat, "language_model_only"); ok {
		if enabled, ok := coerceBool(raw); ok && !enabled {
			change := model.ParameterChange{
				Name:             "language_model_only",
				CurrentValue:     raw,
				RecommendedValue: true,
			}
			return bindIssue(issue, model.RecommendationItem{
				ID:                   "rule_enable_language_model_only",
				Objective:            string(ctx.objective),
				ActionKind:           model.RecommendationActionKindParameterChange,
				RecommendationSource: model.RecommendationSourceRule,
				Summary:              conciseRuleSummary("Disable multimodal pathways for text-only traffic", []model.ParameterChange{change}),
				Changes:              []model.ParameterChange{change},
				PredictedEffect:      applyFallbackEffect(ctx.baseline, 3, -3, -2, -4, 2),
				Confidence:           clampFloat(0.70+(issue.Finding.Confidence*0.18), 0.70, 0.88),
				SafetyNotes: []string{
					"Only enable language_model_only when the deployment will not receive image, video, or audio inputs.",
				},
				ValidationChecks: []string{
					"Confirm multimodal cache queries remain zero and request latency does not regress after disabling multimodal pathways.",
				},
				Basis: "Rule-based multimodal simplification from text-only traffic evidence on a multimodal-capable deployment.",
			})
		}
	}

	change := model.ParameterChange{
		Name:             "limit_mm_per_prompt",
		CurrentValue:     flat["limit_mm_per_prompt"],
		RecommendedValue: map[string]int{"image": 0, "video": 0, "audio": 0},
	}
	return bindIssue(issue, model.RecommendationItem{
		ID:                   "rule_limit_mm_per_prompt_to_zero",
		Objective:            string(ctx.objective),
		ActionKind:           model.RecommendationActionKindParameterChange,
		RecommendationSource: model.RecommendationSourceRule,
		Summary:              conciseRuleSummary("Set multimodal prompt limits to zero for text-only traffic", []model.ParameterChange{change}),
		Changes:              []model.ParameterChange{change},
		PredictedEffect:      applyFallbackEffect(ctx.baseline, 2, -2, -2, -3, 1),
		Confidence:           clampFloat(0.64+(issue.Finding.Confidence*0.18), 0.64, 0.82),
		SafetyNotes: []string{
			"Apply zero modality limits only when the deployment is expected to serve text-only requests throughout the rollout window.",
		},
		ValidationChecks: []string{
			"Verify text-only requests succeed normally and multimodal inputs are intentionally rejected or absent after the change.",
		},
		Basis: "Rule-based multimodal limit tightening from text-only traffic evidence when language_model_only was not directly available in the config snapshot.",
	})
}

func buildPrefillHeavyRecommendation(issue issueCandidate, ctx ruleBuildContext) model.RecommendationItem {
	changes := recommendedPrefillChanges(ctx)
	if len(changes) == 0 {
		return buildOperationalRecommendation(
			issue,
			ctx.objective,
			"Tune chunked prefill budgets and fairness using the current prompt mix before expanding batch size further.",
			0.60,
			"Rule-based operational recommendation from prefill-heavy workload evidence because the current chunked-prefill knobs were unavailable.",
			"Do not assume the largest token budget is best for prompt-heavy latency-sensitive traffic.",
			"Replay the same prompt mix and compare TTFT, queue time, and p95 latency after the prefill change.",
		)
	}

	throughputDelta := 4.0
	ttftDelta := -10.0
	p50Delta := -8.0
	p95Delta := -10.0
	gpuDelta := 3.0
	switch normalizeObjective(ctx.objective) {
	case ThroughputFirstObjective:
		throughputDelta = 8
		ttftDelta = 4
		p50Delta = 2
		p95Delta = -2
		gpuDelta = 6
	case BalancedObjective:
		throughputDelta = 5
		ttftDelta = -6
		p50Delta = -4
		p95Delta = -6
		gpuDelta = 4
	}

	return bindIssue(issue, model.RecommendationItem{
		ID:                   "rule_prefill_heavy_workload",
		Objective:            string(ctx.objective),
		ActionKind:           model.RecommendationActionKindParameterChange,
		RecommendationSource: model.RecommendationSourceRule,
		Summary:              conciseRuleSummary("Tune chunked prefill budget for the observed prompt mix", changes),
		Changes:              changes,
		PredictedEffect:      applyFallbackEffect(ctx.baseline, throughputDelta, ttftDelta, p50Delta, p95Delta, gpuDelta),
		Confidence:           clampFloat(0.57+(issue.Finding.Confidence*0.18), 0.57, 0.80),
		SafetyNotes: []string{
			"Keep chunked-prefill token budget and partial-prefill fairness settings aligned so short prompts are not starved by long prefills.",
		},
		ValidationChecks: []string{
			"Confirm queue time and TTFT improve for shorter prompts without regressing the overall throughput target.",
			"Check whether max_long_partial_prefills stays below max_num_partial_prefills when mixed prompt sizes are present.",
		},
		Basis: "Rule-based chunked-prefill tuning from prefill-heavy workload evidence and current scheduler budget settings.",
	})
}

func buildOperationalRecommendation(issue issueCandidate, objective Objective, summary string, confidence float64, basis string, safetyNote string, validation string) model.RecommendationItem {
	return bindIssue(issue, model.RecommendationItem{
		ID:                   memoryPressureRuleID(issue.Finding.ID),
		Objective:            string(objective),
		ActionKind:           model.RecommendationActionKindOperational,
		RecommendationSource: model.RecommendationSourceRule,
		Summary:              summary,
		Confidence:           clampFloat(confidence, 0, 1),
		SafetyNotes:          compactStrings(safetyNote),
		ValidationChecks:     compactStrings(validation),
		Basis:                basis,
	})
}

func memoryPressureRuleID(issueID string) string {
	switch issueID {
	case "kv_cache_pressure_preemptions":
		return "rule_kv_cache_pressure"
	default:
		return "rule_" + issueID
	}
}

func bindIssue(issue issueCandidate, item model.RecommendationItem) model.RecommendationItem {
	item.IssueID = issue.Finding.ID
	item.IssueSummary = issue.Headline
	item.IssueRank = issue.Finding.Rank
	item.IssueCategory = issue.Finding.Category
	if item.ActionKind == "" {
		if len(item.Changes) > 0 {
			item.ActionKind = model.RecommendationActionKindParameterChange
		} else {
			item.ActionKind = model.RecommendationActionKindOperational
		}
	}
	if item.RecommendationSource == "" {
		item.RecommendationSource = model.RecommendationSourceRule
	}
	return item
}

func buildSharedActionID(item model.RecommendationItem) string {
	if signature := recommendationChangeSignature(item.Changes); signature != "" {
		return "action_" + sanitizeIdentifier(signature)
	}
	summary := sanitizeIdentifier(strings.ToLower(strings.TrimSpace(item.Summary)))
	if summary == "" {
		return ""
	}
	return "action_" + summary
}

func sanitizeIdentifier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		" ", "_",
		",", "_",
		":", "_",
		"=", "_",
		".", "_",
		"/", "_",
		"|", "_",
	)
	value = replacer.Replace(value)
	value = strings.Trim(value, "_")
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return value
}

func recommendedHeadroomBatchingChanges(ctx ruleBuildContext) []model.ParameterChange {
	currentSeqs, hasSeqs := ctx.derived.CurrentNumeric["max_num_seqs"]
	currentTokens, hasTokens := ctx.derived.CurrentNumeric["max_num_batched_tokens"]
	if !hasSeqs && !hasTokens {
		return nil
	}

	seqMultiplier := 1.20
	tokenMultiplier := 1.20
	switch normalizeObjective(ctx.objective) {
	case ThroughputFirstObjective:
		seqMultiplier = 1.25
		tokenMultiplier = 1.25
	case LatencyFirstObjective:
		seqMultiplier = 1.10
		tokenMultiplier = 1.10
	}
	if ctx.present["prefill_heavy_workload"] {
		tokenMultiplier = math.Min(tokenMultiplier, 1.10)
	}

	recommended := map[string]float64{}
	if hasSeqs {
		recommended["max_num_seqs"] = math.Max(1, roundUpToStep(currentSeqs*seqMultiplier, 1))
	}
	if hasTokens {
		recommended["max_num_batched_tokens"] = math.Max(512, roundUpToStep(currentTokens*tokenMultiplier, 256))
	}
	return buildParameterChanges(ctx.derived.CurrentConfig, recommended)
}

func recommendedMemoryPressureChanges(ctx ruleBuildContext, preferMemoryLimit bool) []model.ParameterChange {
	currentMemoryUtil, hasMemoryUtil := ctx.derived.CurrentNumeric["gpu_memory_utilization"]
	if preferMemoryLimit && hasMemoryUtil {
		target := safeGPUMemoryUtilizationCap(ctx.objective)
		if currentMemoryUtil > 0 && currentMemoryUtil < target {
			return buildParameterChanges(ctx.derived.CurrentConfig, map[string]float64{
				"gpu_memory_utilization": roundToDecimals(math.Min(target, currentMemoryUtil+0.03), 2),
			})
		}
	}

	recommended := map[string]float64{}
	if currentSeqs, ok := ctx.derived.CurrentNumeric["max_num_seqs"]; ok && currentSeqs > 1 {
		recommended["max_num_seqs"] = math.Max(1, roundDownToStep(currentSeqs*0.85, 1))
	}
	if currentTokens, ok := ctx.derived.CurrentNumeric["max_num_batched_tokens"]; ok && currentTokens > 1024 {
		recommended["max_num_batched_tokens"] = math.Max(1024, roundDownToStep(currentTokens*0.85, 256))
	}
	return buildParameterChanges(ctx.derived.CurrentConfig, recommended)
}

func recommendedPrefillChanges(ctx ruleBuildContext) []model.ParameterChange {
	currentTokens, hasTokens := ctx.derived.CurrentNumeric["max_num_batched_tokens"]
	recommended := map[string]float64{}
	switch normalizeObjective(ctx.objective) {
	case ThroughputFirstObjective:
		if hasTokens {
			recommended["max_num_batched_tokens"] = math.Max(1024, roundUpToStep(currentTokens*1.10, 256))
		}
	case LatencyFirstObjective:
		if hasTokens {
			recommended["max_num_batched_tokens"] = math.Max(512, roundDownToStep(currentTokens*0.80, 256))
		}
	default:
		if hasTokens {
			recommended["max_num_batched_tokens"] = math.Max(1024, roundDownToStep(currentTokens*0.90, 256))
		}
	}

	if mixedPrefillFairnessEvidence(ctx) {
		currentPartial := ctx.derived.CurrentNumeric["max_num_partial_prefills"]
		currentLongPartial := ctx.derived.CurrentNumeric["max_long_partial_prefills"]
		currentThreshold := ctx.derived.CurrentNumeric["long_prefill_token_threshold"]

		if currentPartial < 2 {
			recommended["max_num_partial_prefills"] = 2
		}
		if currentLongPartial == 0 || currentLongPartial >= 2 {
			recommended["max_long_partial_prefills"] = 1
		}
		if currentThreshold <= 0 {
			recommended["long_prefill_token_threshold"] = inferredLongPrefillThreshold(ctx)
		}
	}

	return buildParameterChanges(ctx.derived.CurrentConfig, recommended)
}

func mixedPrefillFairnessEvidence(ctx ruleBuildContext) bool {
	if ctx.report != nil {
		if ctx.report.WorkloadProfile != nil && ctx.report.WorkloadProfile.ServingPattern == model.ServingPatternMixed {
			return true
		}
		if ctx.report.ObservedWorkloadProfile != nil && ctx.report.ObservedWorkloadProfile.ServingPattern == model.ServingPatternMixed {
			return true
		}
	}
	features := ctx.derived.Features
	return features.AvgRequestsWaiting >= 1 &&
		features.AvgPrefillTimeSeconds > 0 &&
		features.AvgDecodeTimeSeconds > 0 &&
		features.AvgPrefillTimeSeconds >= (features.AvgDecodeTimeSeconds*1.5)
}

func inferredLongPrefillThreshold(ctx ruleBuildContext) float64 {
	features := ctx.derived.Features
	if features.RequestSuccessDelta > 0 && features.PromptTokensDelta > 0 {
		avgPromptTokens := features.PromptTokensDelta / features.RequestSuccessDelta
		if avgPromptTokens > 0 {
			return math.Max(512, roundUpToStep(clampFloat(avgPromptTokens*1.25, 512, 4096), 256))
		}
	}
	return defaultLongPrefillThreshold
}

func currentHeadroomLoadPct(ctx ruleBuildContext) float64 {
	if ctx.report != nil && ctx.report.CurrentLoadSummary != nil && ctx.report.CurrentLoadSummary.CurrentGPULoadPct > 0 {
		return clampFloat(ctx.report.CurrentLoadSummary.CurrentGPULoadPct, 0, 100)
	}
	return clampFloat(ctx.derived.Features.AvgGPUUtilizationPct, 0, 100)
}

func roundUpToStep(value, step float64) float64 {
	if step <= 1 {
		return math.Ceil(value)
	}
	return math.Ceil(value/step) * step
}

func roundDownToStep(value, step float64) float64 {
	if step <= 1 {
		return math.Floor(value)
	}
	return math.Floor(value/step) * step
}

func roundToDecimals(value float64, decimals int) float64 {
	if decimals <= 0 {
		return math.Round(value)
	}
	scale := math.Pow10(decimals)
	return math.Round(value*scale) / scale
}

func safeGPUMemoryUtilizationCap(objective Objective) float64 {
	switch normalizeObjective(objective) {
	case ThroughputFirstObjective:
		return 0.94
	default:
		return 0.92
	}
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func hasNumericChange(changes []model.ParameterChange, name string) bool {
	for _, change := range changes {
		if change.Name == name {
			return true
		}
	}
	return false
}

func nearestCalibrationMatch(corpus *corpusDocument, derived derivedContext) *corpusCalibration {
	if corpus == nil {
		return nil
	}
	matches := rankCorpusProfiles(corpus, derived)
	for _, match := range matches {
		if match.Score < corpusCalibrationMinScore {
			continue
		}
		if match.Profile.GPUCount != derived.GPUCount {
			continue
		}
		if strings.TrimSpace(derived.HardwareClass) == "" || !strings.EqualFold(strings.TrimSpace(match.Profile.HardwareClass), strings.TrimSpace(derived.HardwareClass)) {
			continue
		}
		baselineSelection := selectNearestMeasurement(match.Profile, derived.CurrentNumeric)
		baselinePrediction := (*model.Prediction)(nil)
		precise := false
		if baselineSelection != nil {
			baselinePrediction = predictionFromMeasurement(
				baselineSelection.Measurement,
				baselinePredictionBasis(*baselineSelection),
				clampFloat(match.Score*baselineConfidence(*baselineSelection), 0.45, 0.98),
			)
			precise = match.Score >= corpusPreciseMinScore && baselineSelection.Distance <= corpusSelectionMaxDistance
		}
		return &corpusCalibration{
			Match:              match,
			Baseline:           baselineSelection,
			BaselinePrediction: baselinePrediction,
			Precise:            precise,
		}
	}
	return nil
}

func matchedCorpusProfileModel(calibration *corpusCalibration) *model.MatchedCorpusProfile {
	if calibration == nil {
		return nil
	}
	match := calibration.Match
	return &model.MatchedCorpusProfile{
		ID:            match.Profile.ID,
		CorpusVersion: match.CorpusVersion,
		ModelName:     match.Profile.ModelName,
		ModelFamily:   match.Profile.ModelFamily,
		GPUCount:      match.Profile.GPUCount,
		HardwareClass: match.Profile.HardwareClass,
		WorkloadClass: match.Profile.WorkloadClass,
		MatchScore:    match.Score,
		Basis:         match.Basis,
	}
}

func applyCorpusCalibration(report *model.RecommendationReport, calibration *corpusCalibration, derived derivedContext) {
	if report == nil || calibration == nil || len(report.Recommendations) == 0 {
		return
	}
	for index := range report.Recommendations {
		item := &report.Recommendations[index]
		calibrateRecommendationItem(item, calibration, derived, report.BaselinePrediction)
	}
}

func calibrateRecommendationItem(item *model.RecommendationItem, calibration *corpusCalibration, derived derivedContext, baseline *model.Prediction) {
	if item == nil || calibration == nil || item.ActionKind != model.RecommendationActionKindParameterChange || len(item.Changes) == 0 {
		return
	}
	requested := requestedNumericConfig(derived.CurrentNumeric, item.Changes)
	if len(requested) == 0 {
		return
	}
	selection := selectNearestMeasurement(calibration.Match.Profile, requested)
	if selection == nil {
		return
	}

	baseMetrics := baseline
	if baseMetrics == nil {
		baseMetrics = calibration.BaselinePrediction
	}
	effect := effectFromMeasurement(selection.Measurement, baseMetrics)
	calibrationConfidence := clampFloat(calibration.Match.Score*measurementCalibrationConfidence(*selection), 0.55, 0.96)
	calibrationBasis := fmt.Sprintf("Calibrated against benchmark profile %s using %s.", calibration.Match.Profile.ID, measurementCalibrationBasis(*selection))

	if calibration.Precise && selection.Distance <= corpusSelectionMaxDistance {
		item.PredictedEffect = effect
		item.Confidence = clampFloat(math.Max(item.Confidence, calibrationConfidence), 0, 0.98)
		item.Basis = strings.TrimSpace(item.Basis + " " + calibrationBasis)
		item.RecommendationSource = model.RecommendationSourceHybrid
		item.SafetyNotes = append([]string{
			fmt.Sprintf("Near benchmark profile %s was used to calibrate the predicted effect for this change set.", calibration.Match.Profile.ID),
		}, item.SafetyNotes...)
		return
	}

	if selection.Exact || selection.Distance <= corpusSelectionMaxDistance {
		item.PredictedEffect = effect
		item.Confidence = clampFloat(math.Max(item.Confidence, math.Min(calibrationConfidence, 0.82)), 0, 0.90)
		item.Basis = strings.TrimSpace(item.Basis + " " + calibrationBasis)
		item.RecommendationSource = model.RecommendationSourceHybrid
	}
}

func requestedNumericConfig(current map[string]float64, changes []model.ParameterChange) map[string]float64 {
	out := map[string]float64{}
	for key, value := range current {
		out[key] = value
	}
	for _, change := range changes {
		value, ok := coerceFloat(change.RecommendedValue)
		if !ok {
			continue
		}
		out[change.Name] = value
	}
	return out
}

func measurementCalibrationBasis(selection measurementSelection) string {
	if selection.Exact {
		return "an exact benchmarked parameter set"
	}
	return "the nearest benchmarked parameter set"
}

func measurementCalibrationConfidence(selection measurementSelection) float64 {
	if selection.Exact {
		return 0.95
	}
	return clampFloat(0.84-(selection.Distance*0.22), 0.58, 0.84)
}

func humanizeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Unknown"
	}
	value = strings.ReplaceAll(value, "_", " ")
	return strings.ToUpper(value[:1]) + value[1:]
}
