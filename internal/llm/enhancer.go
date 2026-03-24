package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/inferLean/inferlean-project/internal/model"
)

const (
	envBaseURL = "INFERLEAN_LLM_BASE_URL"
	envAPIKey  = "INFERLEAN_LLM_API_KEY"
	envModel   = "INFERLEAN_LLM_MODEL"
)

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func EnhanceAnalysisReport(ctx context.Context, report *model.AnalysisReport) (*model.LLMEnhancedOutput, string) {
	payload, err := json.Marshal(struct {
		VLLMInformation model.VLLMInformation  `json:"vllm_information"`
		Summary         *model.AnalysisSummary `json:"analysis_summary"`
		Warnings        []string               `json:"warnings,omitempty"`
	}{
		VLLMInformation: report.VLLMInformation,
		Summary:         report.AnalysisSummary,
		Warnings:        report.Warnings,
	})
	if err != nil {
		return nil, "llm enhancement skipped: failed to marshal analysis summary"
	}
	return enhance(ctx, "analysis", string(payload))
}

func EnhanceRecommendationReport(ctx context.Context, report *model.RecommendationReport) (*model.LLMEnhancedOutput, string) {
	payload, err := json.Marshal(struct {
		Objective          string                      `json:"objective"`
		MatchedCorpus      *model.MatchedCorpusProfile `json:"matched_corpus_profile,omitempty"`
		BaselinePrediction *model.Prediction           `json:"baseline_prediction,omitempty"`
		Recommendations    []model.RecommendationItem  `json:"recommendations,omitempty"`
		ScenarioPrediction *model.Prediction           `json:"scenario_prediction,omitempty"`
		Warnings           []string                    `json:"warnings,omitempty"`
	}{
		Objective:          report.Objective,
		MatchedCorpus:      report.MatchedCorpusProfile,
		BaselinePrediction: report.BaselinePrediction,
		Recommendations:    report.Recommendations,
		ScenarioPrediction: report.ScenarioPrediction,
		Warnings:           report.Warnings,
	})
	if err != nil {
		return nil, "llm enhancement skipped: failed to marshal recommendation summary"
	}
	return enhance(ctx, "recommendation", string(payload))
}

func enhance(ctx context.Context, kind, canonicalJSON string) (*model.LLMEnhancedOutput, string) {
	baseURL := strings.TrimSpace(os.Getenv(envBaseURL))
	apiKey := strings.TrimSpace(os.Getenv(envAPIKey))
	modelName := strings.TrimSpace(os.Getenv(envModel))
	if baseURL == "" || apiKey == "" || modelName == "" {
		return nil, ""
	}
	if ctx == nil {
		ctx = context.Background()
	}

	reqBody := chatCompletionRequest{
		Model: modelName,
		Messages: []chatMessage{
			{
				Role: "system",
				Content: "You rewrite deterministic infrastructure diagnostics into concise operator-facing JSON. " +
					"Do not invent numbers or alter canonical conclusions. Respond with JSON only using keys: summary, explanation, action_highlights.",
			},
			{
				Role:    "user",
				Content: fmt.Sprintf("Kind: %s\nCanonical JSON:\n%s", kind, canonicalJSON),
			},
		},
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, "llm enhancement skipped: failed to marshal request"
	}

	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, "llm enhancement skipped: failed to build request"
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "llm enhancement skipped: " + err.Error()
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "llm enhancement skipped: failed to read response"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Sprintf("llm enhancement skipped: endpoint returned %s", resp.Status)
	}

	var completion chatCompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return nil, "llm enhancement skipped: invalid completion payload"
	}
	if len(completion.Choices) == 0 {
		return nil, "llm enhancement skipped: empty completion response"
	}

	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return nil, "llm enhancement skipped: empty completion content"
	}

	var enhanced model.LLMEnhancedOutput
	if err := json.Unmarshal([]byte(content), &enhanced); err != nil {
		return &model.LLMEnhancedOutput{Explanation: content}, ""
	}
	if enhanced.Summary == "" && enhanced.Explanation == "" && len(enhanced.ActionHighlights) == 0 {
		return nil, "llm enhancement skipped: completion produced no enhancement fields"
	}
	return &enhanced, ""
}
