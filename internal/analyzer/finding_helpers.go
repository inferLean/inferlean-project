package analyzer

import "github.com/inferLean/inferlean-project/internal/model"

func evidence(metric string, value float64, note string) model.EvidenceItem {
	return model.EvidenceItem{
		Metric: metric,
		Value:  value,
		Note:   note,
	}
}

func insufficientFinding(spec DetectorSpec, summary string, confidence float64, evidenceItems ...model.EvidenceItem) model.Finding {
	return enrichFindingNarrative(model.Finding{
		ID:         spec.ID,
		Category:   spec.Category,
		Status:     model.FindingStatusInsufficientData,
		Severity:   model.SeverityNone,
		Confidence: clampFloat(confidence, 0, 1),
		Summary:    summary,
		Evidence:   evidenceItems,
	})
}

func absentFinding(spec DetectorSpec, summary string, confidence float64, evidenceItems ...model.EvidenceItem) model.Finding {
	return enrichFindingNarrative(model.Finding{
		ID:         spec.ID,
		Category:   spec.Category,
		Status:     model.FindingStatusAbsent,
		Severity:   model.SeverityNone,
		Confidence: clampFloat(confidence, 0, 1),
		Summary:    summary,
		Evidence:   evidenceItems,
	})
}

func presentFinding(spec DetectorSpec, severity, summary string, confidence float64, evidenceItems ...model.EvidenceItem) model.Finding {
	return enrichFindingNarrative(model.Finding{
		ID:         spec.ID,
		Category:   spec.Category,
		Status:     model.FindingStatusPresent,
		Severity:   severity,
		Confidence: clampFloat(confidence, 0, 1),
		Summary:    summary,
		Evidence:   evidenceItems,
	})
}
