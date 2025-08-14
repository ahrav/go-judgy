package scoring

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ahrav/go-judgy/internal/domain"
)

// Constants for threshold testing
const (
	TestThreshold       = domain.DefaultBlobThresholdBytes
	TestThresholdMinus1 = TestThreshold - 1
	TestThresholdPlus1  = TestThreshold + 1
)

// ScoreBuilder provides a fluent interface for building test scores.
type ScoreBuilder struct {
	score domain.Score
}

// NewScoreBuilder creates a new score builder with sensible defaults.
func NewScoreBuilder() *ScoreBuilder {
	return &ScoreBuilder{
		score: domain.Score{
			ID:         "550e8400-e29b-41d4-a716-446655440000", // Valid UUID
			AnswerID:   "550e8400-e29b-41d4-a716-446655440001", // Valid UUID
			Value:      0.75,
			Confidence: 0.85,
			ScoreEvidence: domain.ScoreEvidence{
				InlineReasoning: "Default reasoning for test",
			},
			ScoreProvenance: domain.ScoreProvenance{
				JudgeID:  "test-judge",
				Provider: "test-provider",
				Model:    "test-model",
				ScoredAt: time.Now(),
			},
			ScoreUsage: domain.ScoreUsage{
				TokensUsed: 50,
				CallsUsed:  1,
				LatencyMs:  100,
			},
			ScoreValidity: domain.ScoreValidity{
				Valid: true,
			},
		},
	}
}

// WithID sets the score ID, converting to UUID if needed.
func (b *ScoreBuilder) WithID(id string) *ScoreBuilder {
	// Try to parse as UUID, if it fails, use a deterministic UUID
	if _, err := uuid.Parse(id); err != nil {
		id = uuid.NewSHA1(uuid.NameSpaceURL, []byte(id)).String()
	}
	b.score.ID = id
	return b
}

// WithAnswerID sets the answer ID, converting to UUID if needed.
func (b *ScoreBuilder) WithAnswerID(answerID string) *ScoreBuilder {
	// Try to parse as UUID, if it fails, use a deterministic UUID
	if _, err := uuid.Parse(answerID); err != nil {
		answerID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(answerID)).String()
	}
	b.score.AnswerID = answerID
	return b
}

// WithValue sets the score value.
func (b *ScoreBuilder) WithValue(value float64) *ScoreBuilder {
	b.score.Value = value
	return b
}

// WithConfidence sets the confidence score.
func (b *ScoreBuilder) WithConfidence(confidence float64) *ScoreBuilder {
	b.score.Confidence = confidence
	return b
}

// WithInlineReasoning sets inline reasoning.
func (b *ScoreBuilder) WithInlineReasoning(reasoning string) *ScoreBuilder {
	b.score.InlineReasoning = reasoning
	b.score.ReasonRef = domain.ArtifactRef{} // Clear blob ref
	return b
}

// WithBlobReasoning sets blob reasoning reference.
func (b *ScoreBuilder) WithBlobReasoning(key string, size int64) *ScoreBuilder {
	b.score.ReasonRef = domain.ArtifactRef{
		Key:  key,
		Size: size,
		Kind: domain.ArtifactJudgeRationale,
	}
	b.score.InlineReasoning = "" // Clear inline
	return b
}

// WithLargeReasoning sets reasoning larger than threshold for blob testing.
func (b *ScoreBuilder) WithLargeReasoning() *ScoreBuilder {
	b.score.InlineReasoning = createLargeReasoning(TestThreshold)
	return b
}

// WithSmallReasoning sets reasoning smaller than threshold for inline testing.
func (b *ScoreBuilder) WithSmallReasoning() *ScoreBuilder {
	b.score.InlineReasoning = createSmallReasoning()
	return b
}

// WithThresholdReasoning sets reasoning exactly at threshold for edge testing.
func (b *ScoreBuilder) WithThresholdReasoning() *ScoreBuilder {
	b.score.InlineReasoning = createThresholdReasoning(TestThreshold)
	return b
}

// WithProvider sets the provider information.
func (b *ScoreBuilder) WithProvider(provider string) *ScoreBuilder {
	b.score.Provider = provider
	return b
}

// WithModel sets the model information.
func (b *ScoreBuilder) WithModel(model string) *ScoreBuilder {
	b.score.Model = model
	return b
}

// WithProviderRequestIDs sets provider request IDs for correlation.
func (b *ScoreBuilder) WithProviderRequestIDs(requestIDs []string) *ScoreBuilder {
	b.score.ProviderRequestIDs = requestIDs
	return b
}

// WithUsage sets the usage metrics.
func (b *ScoreBuilder) WithUsage(tokensUsed, callsUsed int64, latencyMs int64, costCents domain.Cents) *ScoreBuilder {
	b.score.ScoreUsage = domain.ScoreUsage{
		TokensUsed: tokensUsed,
		CallsUsed:  callsUsed,
		LatencyMs:  latencyMs,
	}
	b.score.CostCents = costCents
	return b
}

// AsInvalid marks the score as invalid with the given error.
func (b *ScoreBuilder) AsInvalid(errorMsg string) *ScoreBuilder {
	b.score.Valid = false
	b.score.Error = errorMsg
	// Clear values for invalid scores
	b.score.Value = 0
	b.score.Confidence = 0
	b.score.InlineReasoning = ""
	b.score.ReasonRef = domain.ArtifactRef{}
	return b
}

// Build returns the constructed score.
func (b *ScoreBuilder) Build() domain.Score {
	return b.score
}

// ScorePlanBuilder provides a fluent interface for building test score plans.
type ScorePlanBuilder struct {
	plan ScorePlan
}

// NewScorePlanBuilder creates a new score plan builder.
func NewScorePlanBuilder() *ScorePlanBuilder {
	return &ScorePlanBuilder{
		plan: ScorePlan{
			Provider: "test-provider",
			Model:    "test-model",
			Usage: domain.ScoreUsage{
				TokensUsed: 50,
				CallsUsed:  1,
				LatencyMs:  100,
			},
			CostCents: domain.Cents(25),
		},
	}
}

// ForAnswer sets the answer ID this plan applies to.
// If the ID is not a valid UUID, it generates a deterministic UUID.
func (b *ScorePlanBuilder) ForAnswer(answerID string) *ScorePlanBuilder {
	// Try to parse as UUID, if it fails, use a deterministic UUID
	if _, err := uuid.Parse(answerID); err != nil {
		answerID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(answerID)).String()
	}
	b.plan.AnswerID = answerID
	return b
}

// WithResult sets the score result to return.
func (b *ScorePlanBuilder) WithResult(score domain.Score) *ScorePlanBuilder {
	b.plan.Result = &score
	b.plan.Error = nil // Clear error
	return b
}

// WithError sets the error to return.
func (b *ScorePlanBuilder) WithError(err error, errorType string) *ScorePlanBuilder {
	b.plan.Error = err
	b.plan.ErrorType = errorType
	b.plan.Result = nil // Clear result
	return b
}

// WithRawJSON sets the raw JSON response for validator testing.
func (b *ScorePlanBuilder) WithRawJSON(rawJSON string) *ScorePlanBuilder {
	b.plan.RawJSON = rawJSON
	return b
}

// WithProviderMeta sets provider metadata.
func (b *ScorePlanBuilder) WithProviderMeta(provider, model string, requestIDs []string) *ScorePlanBuilder {
	b.plan.Provider = provider
	b.plan.Model = model
	b.plan.RequestIDs = requestIDs
	return b
}

// WithUsage sets usage metrics.
func (b *ScorePlanBuilder) WithUsage(tokensUsed, callsUsed int64, latencyMs int64, costCents domain.Cents) *ScorePlanBuilder {
	b.plan.Usage = domain.ScoreUsage{
		TokensUsed: tokensUsed,
		CallsUsed:  callsUsed,
		LatencyMs:  latencyMs,
	}
	b.plan.CostCents = costCents
	return b
}

// Build returns the constructed score plan.
func (b *ScorePlanBuilder) Build() ScorePlan {
	return b.plan
}

// InputBuilder provides a fluent interface for building test inputs.
type InputBuilder struct {
	input domain.ScoreAnswersInput
}

// NewInputBuilder creates a new input builder with sensible defaults.
func NewInputBuilder() *InputBuilder {
	return &InputBuilder{
		input: domain.ScoreAnswersInput{
			Question: "What is 2+2?",
			Config:   domain.DefaultEvalConfig(),
		},
	}
}

// WithQuestion sets the question.
func (b *InputBuilder) WithQuestion(question string) *InputBuilder {
	b.input.Question = question
	return b
}

// WithAnswers sets the answers.
func (b *InputBuilder) WithAnswers(answers ...domain.Answer) *InputBuilder {
	b.input.Answers = answers
	return b
}

// WithSingleAnswer adds a single answer with the given ID.
// If the ID is not a valid UUID, it generates a deterministic UUID.
func (b *InputBuilder) WithSingleAnswer(answerID string) *InputBuilder {
	// Try to parse as UUID, if it fails, use a deterministic UUID
	if _, err := uuid.Parse(answerID); err != nil {
		answerID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(answerID)).String()
	}
	answer := domain.Answer{
		ID: answerID,
		ContentRef: domain.ArtifactRef{
			Key:  fmt.Sprintf("answers/%s.txt", answerID),
			Size: 4,
			Kind: domain.ArtifactAnswer,
		},
	}
	b.input.Answers = []domain.Answer{answer}
	return b
}

// WithMultipleAnswers adds multiple answers with generated UUIDs.
func (b *InputBuilder) WithMultipleAnswers(count int) *InputBuilder {
	answers := make([]domain.Answer, count)
	for i := 0; i < count; i++ {
		// Generate deterministic UUIDs from answer labels for consistency
		label := fmt.Sprintf("answer-%d", i+1)
		answerID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(label)).String()
		answers[i] = domain.Answer{
			ID: answerID,
			ContentRef: domain.ArtifactRef{
				Key:  fmt.Sprintf("answers/%s.txt", answerID),
				Size: 4,
				Kind: domain.ArtifactAnswer,
			},
		}
	}
	b.input.Answers = answers
	return b
}

// WithConfig sets the evaluation configuration.
func (b *InputBuilder) WithConfig(config domain.EvalConfig) *InputBuilder {
	b.input.Config = config
	return b
}

// Build returns the constructed input.
func (b *InputBuilder) Build() domain.ScoreAnswersInput {
	return b.input
}

// TestScenarioBuilder provides a complete test scenario with activities and mock.
type TestScenarioBuilder struct {
	plans         []ScorePlan
	blobThreshold int
	eventSink     *capturingEventSink
}

// NewTestScenarioBuilder creates a new test scenario builder.
func NewTestScenarioBuilder() *TestScenarioBuilder {
	return &TestScenarioBuilder{
		blobThreshold: TestThreshold,
		plans:         make([]ScorePlan, 0),
	}
}

// WithBlobThreshold sets the blob threshold for the test.
func (b *TestScenarioBuilder) WithBlobThreshold(threshold int) *TestScenarioBuilder {
	b.blobThreshold = threshold
	return b
}

// WithScorePlan adds a score plan to the scenario.
func (b *TestScenarioBuilder) WithScorePlan(plan ScorePlan) *TestScenarioBuilder {
	b.plans = append(b.plans, plan)
	return b
}

// WithScorePlans adds multiple score plans to the scenario.
func (b *TestScenarioBuilder) WithScorePlans(plans ...ScorePlan) *TestScenarioBuilder {
	b.plans = append(b.plans, plans...)
	return b
}

// Build returns the complete test setup.
func (b *TestScenarioBuilder) Build() (*Activities, *scriptedMockClient, *capturingEventSink, *testArtifactStore) {
	// Create event capture infrastructure
	base, sink := newCapturingBaseActivities()
	b.eventSink = sink

	// Create artifact store with test capabilities
	store := newTestArtifactStore()

	// Create scripted mock client
	mock := newScriptedMockClient(b.plans...)

	// Create mock progress reporter for testing
	progressReporter, _ := NewMockProgressReporter()

	// Create activities with all dependencies
	activities := NewActivities(base.BaseActivities, mock, store, b.blobThreshold, progressReporter)

	return activities, mock, sink, store
}

// BuildWithProgressCapture returns the complete test setup with progress message capture.
// Use this when tests need to verify progress reporting behavior.
func (b *TestScenarioBuilder) BuildWithProgressCapture() (*Activities, *scriptedMockClient, *capturingEventSink, *testArtifactStore, func() []string) {
	// Create event capture infrastructure
	base, sink := newCapturingBaseActivities()
	b.eventSink = sink

	// Create artifact store with test capabilities
	store := newTestArtifactStore()

	// Create scripted mock client
	mock := newScriptedMockClient(b.plans...)

	// Create mock progress reporter with capture capability
	progressReporter, getProgressMessages := NewMockProgressReporter()

	// Create activities with all dependencies
	activities := NewActivities(base.BaseActivities, mock, store, b.blobThreshold, progressReporter)

	return activities, mock, sink, store, getProgressMessages
}

// JSON test data for validator testing

// ValidJSONScore represents a valid JSON score response.
const ValidJSONScore = `{
	"score": 0.8,
	"reasoning": "This is a good answer with clear explanation",
	"confidence": 0.9
}`

// JSONWithCodeFence represents JSON wrapped in markdown code fences.
const JSONWithCodeFence = "```json\n" + ValidJSONScore + "\n```"

// JSONWithTrailingComma represents JSON with trailing comma that needs repair.
const JSONWithTrailingComma = `{
	"score": 0.8,
	"reasoning": "This answer needs trailing comma repair",
	"confidence": 0.9,
}`

// JSONWithUnquotedKeys represents JSON with unquoted property names.
const JSONWithUnquotedKeys = `{
	score: 0.8,
	reasoning: "This answer has unquoted keys",
	confidence: 0.9
}`

// JSONWithSingleQuotes represents JSON with single quotes instead of double.
const JSONWithSingleQuotes = `{
	'score': 0.8,
	'reasoning': 'This answer uses single quotes',
	'confidence': 0.9
}`

// InvalidJSONNotRepairable represents JSON that cannot be repaired.
const InvalidJSONNotRepairable = `{
	"score": not_a_number,
	"reasoning": "This JSON is fundamentally broken"
	missing_comma: true
}`

// JSONViolatingBusinessRules represents valid JSON but violates business rules.
const JSONViolatingBusinessRules = `{
	"score": 1.5,
	"reasoning": "Short",
	"confidence": -0.1
}`

// Edge case JSON constants for comprehensive testing

// EmptyJSON represents completely empty JSON input.
const EmptyJSON = ""

// WhitespaceOnlyJSON represents JSON with only whitespace.
const WhitespaceOnlyJSON = "   \n\t  "

// EmptyObjectJSON represents valid but empty JSON object.
const EmptyObjectJSON = "{}"

// JSONWithUnicode represents JSON with various Unicode characters.
const JSONWithUnicode = `{
	"score": 0.8,
	"reasoning": "测试中文字符 🚀 العربية עברית Здравствуй мир! This reasoning contains emojis 😀🎉 and various Unicode: αβγ ∑∏∆",
	"confidence": 0.9
}`

// generateLargeJSON creates a JSON string larger than the specified size.
// Used for testing memory efficiency with extremely large responses.
func generateLargeJSON(sizeBytes int) string {
	baseReasoning := "This is a very long reasoning that will be repeated many times to create an extremely large JSON response for testing memory efficiency and large payload handling. "

	// Calculate how many repetitions we need to exceed the target size
	baseSize := len(baseReasoning)
	repetitions := (sizeBytes / baseSize) + 1

	largeReasoning := ""
	for i := 0; i < repetitions; i++ {
		largeReasoning += baseReasoning
	}

	return fmt.Sprintf(`{
	"score": 0.8,
	"reasoning": "%s",
	"confidence": 0.9
}`, largeReasoning)
}
