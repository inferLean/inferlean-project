package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

func TestEnhanceAnalysisReportReturnsNilWithoutEnv(t *testing.T) {
	t.Setenv(envBaseURL, "")
	t.Setenv(envAPIKey, "")
	t.Setenv(envModel, "")

	enhanced, warning := EnhanceAnalysisReport(context.Background(), &model.AnalysisReport{})
	if enhanced != nil || warning != "" {
		t.Fatalf("expected no enhancement without env, got enhanced=%+v warning=%q", enhanced, warning)
	}
}

func TestEnhanceAnalysisReportParsesJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"summary\":\"short summary\",\"explanation\":\"short explanation\",\"action_highlights\":[\"step one\"]}"}}]}`)
	}))
	defer server.Close()

	t.Setenv(envBaseURL, server.URL)
	t.Setenv(envAPIKey, "test-key")
	t.Setenv(envModel, "test-model")

	report := &model.AnalysisReport{
		GeneratedAt: time.Now().UTC(),
		VLLMInformation: model.VLLMInformation{
			VLLMVersion: "0.18.0",
		},
		AnalysisSummary: &model.AnalysisSummary{},
	}
	enhanced, warning := EnhanceAnalysisReport(context.Background(), report)
	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if enhanced == nil || enhanced.Summary != "short summary" || len(enhanced.ActionHighlights) != 1 {
		t.Fatalf("unexpected enhancement: %+v", enhanced)
	}
}

func TestEnhanceAnalysisReportReturnsWarningOnServerFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv(envBaseURL, server.URL)
	t.Setenv(envAPIKey, "test-key")
	t.Setenv(envModel, "test-model")

	enhanced, warning := EnhanceAnalysisReport(context.Background(), &model.AnalysisReport{
		GeneratedAt:     time.Now().UTC(),
		AnalysisSummary: &model.AnalysisSummary{},
	})
	if enhanced != nil {
		t.Fatalf("expected nil enhancement on server failure, got %+v", enhanced)
	}
	if warning == "" {
		t.Fatalf("expected warning on server failure")
	}
}
