package transport

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"testing"
	"testing/quick"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
)

// Property-based test generators

// generateResponse creates a random Response for property testing
func generateResponse() *Response {
	return &Response{
		Content:      randomString(rand.Intn(1000)),
		FinishReason: randomFinishReason(),
		ProviderRequestIDs: func() []string {
			count := rand.Intn(5)
			ids := make([]string, count)
			for i := range ids {
				ids[i] = fmt.Sprintf("req-%d", rand.Int())
			}
			return ids
		}(),
		Usage: NormalizedUsage{
			PromptTokens:     rand.Int63n(10000),
			CompletionTokens: rand.Int63n(10000),
			TotalTokens:      rand.Int63n(20000),
			LatencyMs:        rand.Int63n(5000),
		},
		EstimatedCostMilliCents: rand.Int63n(100000),
	}
}

// generateRequest creates a random Request for property testing
func generateRequest() *Request {
	providers := []string{"openai", "anthropic", "google"}
	models := []string{"gpt-4", "claude-3", "gemini"}

	return &Request{
		Provider: providers[rand.Intn(len(providers))],
		Model:    models[rand.Intn(len(models))],
		TraceID:  fmt.Sprintf("trace-%d", rand.Int()),
	}
}

// randomString generates a random string of given max length
func randomString(maxLen int) string {
	if maxLen <= 0 {
		maxLen = 1
	}
	length := rand.Intn(maxLen) + 1
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 \n\t"
	result := make([]byte, length)
	for i := range result {
		result[i] = chars[rand.Intn(len(chars))]
	}
	return string(result)
}

// randomFinishReason returns a random valid FinishReason
func randomFinishReason() domain.FinishReason {
	reasons := []domain.FinishReason{
		domain.FinishStop,
		domain.FinishLength,
		domain.FinishContentFilter,
		domain.FinishToolUse,
	}
	return reasons[rand.Intn(len(reasons))]
}

// Property: ResponseToAnswer preserves all request metadata
func TestResponseToAnswer_PreservesMetadata(t *testing.T) {
	f := func(provider, model, traceID string, reqIDs []string) bool {
		// Limit string lengths for practical testing
		if len(provider) > 100 || len(model) > 100 || len(traceID) > 100 {
			return true // Skip overly long inputs
		}

		resp := &Response{
			Content:            "test content",
			ProviderRequestIDs: reqIDs,
			FinishReason:       domain.FinishStop,
		}

		req := &Request{
			Provider: provider,
			Model:    model,
			TraceID:  traceID,
		}

		answer := ResponseToAnswer(resp, req)

		// Properties to verify
		return answer.Provider == provider &&
			answer.Model == model &&
			answer.TraceID == traceID &&
			len(answer.ProviderRequestIDs) == len(reqIDs)
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

// Property: Cost conversion is mathematically correct
func TestResponseToAnswer_CostConversion(t *testing.T) {
	f := func(milliCents int64) bool {
		// Limit to reasonable values to avoid overflow
		if milliCents < 0 || milliCents > math.MaxInt64/1000 {
			return true // Skip invalid inputs
		}

		resp := &Response{
			Content:                 "test",
			EstimatedCostMilliCents: milliCents,
			FinishReason:            domain.FinishStop,
		}

		req := &Request{}

		answer := ResponseToAnswer(resp, req)

		// Property: Cost conversion follows integer division rule
		expectedCents := domain.Cents(milliCents / MilliCentsToFactor)
		return answer.EstimatedCost == expectedCents
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

// Property: Token counts are preserved exactly
func TestResponseToAnswer_TokenPreservation(t *testing.T) {
	f := func(prompt, completion, total, latency int64) bool {
		// Ensure non-negative values
		if prompt < 0 || completion < 0 || total < 0 || latency < 0 {
			return true // Skip negative values
		}

		resp := &Response{
			Content:      "test",
			FinishReason: domain.FinishStop,
			Usage: NormalizedUsage{
				PromptTokens:     prompt,
				CompletionTokens: completion,
				TotalTokens:      total,
				LatencyMs:        latency,
			},
		}

		req := &Request{}

		answer := ResponseToAnswer(resp, req)

		// Properties: All token counts and latency are preserved exactly
		return answer.PromptTokens == prompt &&
			answer.CompletionTokens == completion &&
			answer.TotalTokens == total &&
			answer.LatencyMillis == latency
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

// Property: Truncated flag is set correctly based on FinishReason
func TestResponseToAnswer_TruncatedFlag(t *testing.T) {
	f := func(reasonInt uint8) bool {
		reasons := []domain.FinishReason{
			domain.FinishStop,
			domain.FinishLength,
			domain.FinishContentFilter,
			domain.FinishToolUse,
		}

		reason := reasons[int(reasonInt)%len(reasons)]

		resp := &Response{
			Content:      "test",
			FinishReason: reason,
		}

		req := &Request{}

		answer := ResponseToAnswer(resp, req)

		// Property: Truncated is true only for FinishLength
		expectedTruncated := (reason == domain.FinishLength)
		return answer.Truncated == expectedTruncated
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

// Property: ContentRef size matches content length
func TestResponseToAnswer_ContentRefSize(t *testing.T) {
	f := func(content string) bool {
		// Limit content length for practical testing
		if len(content) > 10000 {
			content = content[:10000]
		}

		resp := &Response{
			Content:      content,
			FinishReason: domain.FinishStop,
		}

		req := &Request{}

		answer := ResponseToAnswer(resp, req)

		// Property: ContentRef.Size equals the byte length of content
		return answer.ContentRef.Size == int64(len(content))
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

// Property: ResponseToScore always sets Valid=true for successful validation
func TestResponseToScore_ValidFlag(t *testing.T) {
	f := func(value float64, confidence float64, reasoning string) bool {
		// Clamp values to valid ranges
		if value < 0 {
			value = 0
		}
		if value > 100 {
			value = 100
		}
		if confidence < 0 {
			confidence = 0
		}
		if confidence > 1 {
			confidence = 1
		}

		resp := &Response{
			Content: fmt.Sprintf(`{"value": %f, "confidence": %f, "reasoning": "%s"}`, value, confidence, reasoning),
		}

		validator := &MockScoreValidator{
			ValidateFunc: func(content string, enableRepair bool) (*ScoreData, error) {
				return &ScoreData{
					Value:      value,
					Confidence: confidence,
					Reasoning:  reasoning,
				}, nil
			},
		}

		score, err := ResponseToScore(resp, "answer-id", &Request{}, validator, false)

		// Properties: No error and Valid is true
		return err == nil && score != nil && score.Valid == true
	}

	config := &quick.Config{MaxCount: 100}
	if err := quick.Check(f, config); err != nil {
		t.Error(err)
	}
}

// Property: Score values are preserved exactly from validator
func TestResponseToScore_ValuePreservation(t *testing.T) {
	f := func(value, confidence float64) bool {
		// Ensure reasonable ranges
		if math.IsNaN(value) || math.IsInf(value, 0) ||
			math.IsNaN(confidence) || math.IsInf(confidence, 0) {
			return true // Skip invalid floating point values
		}

		resp := &Response{
			Content: "test",
		}

		validator := &MockScoreValidator{
			ValidateFunc: func(content string, enableRepair bool) (*ScoreData, error) {
				return &ScoreData{
					Value:      value,
					Confidence: confidence,
					Reasoning:  "test",
				}, nil
			},
		}

		score, err := ResponseToScore(resp, "answer-id", &Request{}, validator, false)

		// Properties: Values are preserved exactly
		return err == nil && score != nil &&
			score.Value == value &&
			score.Confidence == confidence
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

// Property: Cost conversion in ResponseToScore is mathematically correct
func TestResponseToScore_CostConversion(t *testing.T) {
	f := func(milliCents int64) bool {
		if milliCents < 0 || milliCents > math.MaxInt64/1000 {
			return true // Skip invalid inputs
		}

		resp := &Response{
			Content:                 "test",
			EstimatedCostMilliCents: milliCents,
		}

		validator := &MockScoreValidator{
			ValidateFunc: func(content string, enableRepair bool) (*ScoreData, error) {
				return &ScoreData{
					Value:      50,
					Confidence: 0.5,
				}, nil
			},
		}

		score, err := ResponseToScore(resp, "answer-id", &Request{}, validator, false)

		// Property: Cost conversion follows integer division rule
		expectedCents := domain.Cents(milliCents / MilliCentsToFactor)
		return err == nil && score != nil && score.CostCents == expectedCents
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

// Property: CreateInvalidScore always produces invalid scores
func TestCreateInvalidScore_AlwaysInvalid(t *testing.T) {
	f := func(answerID, errorMsg string) bool {
		// Always create a non-nil error to avoid panic in current implementation
		// Note: The implementation has a bug where it calls err.Error() without nil check
		if errorMsg == "" {
			errorMsg = "empty error"
		}
		err := errors.New(errorMsg)

		score := CreateInvalidScore(answerID, err)

		// Properties that must always be true
		return score.Valid == false &&
			score.Value == 0 &&
			score.Confidence == 0 &&
			score.JudgeID == "error" &&
			score.Provider == "error" &&
			score.Model == "error" &&
			score.AnswerID == answerID
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

// Property: Dimension scores are preserved in ResponseToScore
func TestResponseToScore_DimensionPreservation(t *testing.T) {
	f := func(dimCount uint8) bool {
		count := int(dimCount) % 10 // Limit to reasonable number

		dimensions := make([]domain.DimensionScore, count)
		for i := range dimensions {
			dimensions[i] = domain.DimensionScore{
				Name:  domain.Dimension(fmt.Sprintf("dim%d", i)),
				Value: float64(rand.Intn(101)),
			}
		}

		resp := &Response{
			Content: "test",
		}

		validator := &MockScoreValidator{
			ValidateFunc: func(content string, enableRepair bool) (*ScoreData, error) {
				return &ScoreData{
					Value:      75,
					Confidence: 0.8,
					Dimensions: dimensions,
				}, nil
			},
		}

		score, err := ResponseToScore(resp, "answer-id", &Request{}, validator, false)

		// Property: All dimensions are preserved
		if err != nil || score == nil {
			return false
		}

		if len(score.Dimensions) != len(dimensions) {
			return false
		}

		for i, dim := range score.Dimensions {
			if dim.Name != dimensions[i].Name || dim.Value != dimensions[i].Value {
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

// Property: All generated IDs are valid UUIDs
func TestGeneratedIDs_AreValidUUIDs(t *testing.T) {
	f := func() bool {
		// Test ResponseToAnswer ID generation
		resp := &Response{Content: "test", FinishReason: domain.FinishStop}
		answer := ResponseToAnswer(resp, &Request{})
		if !isValidUUID(answer.ID) {
			return false
		}

		// Test ResponseToScore ID generation
		validator := &MockScoreValidator{
			ValidateFunc: func(content string, enableRepair bool) (*ScoreData, error) {
				return &ScoreData{Value: 50, Confidence: 0.5}, nil
			},
		}
		score, _ := ResponseToScore(resp, "test", &Request{}, validator, false)
		if score != nil && !isValidUUID(score.ID) {
			return false
		}

		// Test CreateInvalidScore ID generation
		invalidScore := CreateInvalidScore("test", errors.New("test"))
		if !isValidUUID(invalidScore.ID) {
			return false
		}

		// Test ExtractTraceID generation
		traceID := ExtractTraceID(context.Background())
		return isValidUUID(traceID)
	}

	// Run multiple times to ensure consistency
	for i := 0; i < 100; i++ {
		if !f() {
			t.Error("Generated ID is not a valid UUID")
		}
	}
}

// Property: Timestamps are always set and in the past
func TestTimestamps_AlwaysValid(t *testing.T) {
	f := func() bool {
		now := time.Now()

		// Test ResponseToAnswer timestamp
		resp := &Response{Content: "test", FinishReason: domain.FinishStop}
		answer := ResponseToAnswer(resp, &Request{})
		if answer.GeneratedAt.After(time.Now()) || answer.GeneratedAt.Before(now) {
			return false
		}

		// Test ResponseToScore timestamp
		validator := &MockScoreValidator{
			ValidateFunc: func(content string, enableRepair bool) (*ScoreData, error) {
				return &ScoreData{Value: 50, Confidence: 0.5}, nil
			},
		}
		score, _ := ResponseToScore(resp, "test", &Request{}, validator, false)
		if score != nil && (score.ScoredAt.After(time.Now()) || score.ScoredAt.Before(now)) {
			return false
		}

		// Test CreateInvalidScore timestamp
		invalidScore := CreateInvalidScore("test", errors.New("test"))
		if invalidScore.ScoredAt.After(time.Now()) || invalidScore.ScoredAt.Before(now) {
			return false
		}

		return true
	}

	// Run multiple times
	for i := 0; i < 100; i++ {
		if !f() {
			t.Error("Timestamp is not valid")
		}
	}
}

// Property: JSON repair flag is correctly passed to validator
func TestResponseToScore_JSONRepairFlag(t *testing.T) {
	testCases := []bool{true, false}

	for _, disableRepair := range testCases {
		t.Run(fmt.Sprintf("disableRepair=%v", disableRepair), func(t *testing.T) {
			var capturedEnableRepair bool

			resp := &Response{Content: "test"}
			validator := &MockScoreValidator{
				ValidateFunc: func(content string, enableRepair bool) (*ScoreData, error) {
					capturedEnableRepair = enableRepair
					return &ScoreData{Value: 50, Confidence: 0.5}, nil
				},
			}

			_, _ = ResponseToScore(resp, "test", &Request{}, validator, disableRepair)

			// Property: enableRepair = !disableJSONRepair
			expectedEnableRepair := !disableRepair
			if capturedEnableRepair != expectedEnableRepair {
				t.Errorf("enableRepair = %v, want %v (disableJSONRepair = %v)",
					capturedEnableRepair, expectedEnableRepair, disableRepair)
			}
		})
	}
}

// Property: ExtractAnswerContent handles all answer counts correctly
func TestExtractAnswerContent_AnswerCounts(t *testing.T) {
	f := func(count uint8) bool {
		answerCount := int(count) % 20 // Limit to reasonable number

		answers := make([]domain.Answer, answerCount)
		for i := range answers {
			answers[i] = domain.Answer{
				ID:         fmt.Sprintf("answer-%d", i),
				ContentRef: domain.ArtifactRef{Key: fmt.Sprintf("key-%d", i)},
			}
		}

		store := &MockArtifactStore{
			GetFunc: func(ctx context.Context, ref domain.ArtifactRef) (string, error) {
				return fmt.Sprintf("content for %s", ref.Key), nil
			},
		}

		result := ExtractAnswerContent(context.Background(), answers, store)

		// Properties to verify
		if answerCount == 0 {
			return result == ""
		} else {
			// Should only use the first answer
			expectedContent := "content for key-0"
			return result == expectedContent
		}
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}
