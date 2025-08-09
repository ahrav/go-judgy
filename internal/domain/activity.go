// Package domain provides operation contracts for LLM evaluation processes.
// Defines input and output types that ensure type safety and clear interfaces
// between workflow orchestration and operation execution.
//
// Operation Contract Architecture:
//   - Input/Output types co-located with related domain types for cohesion
//   - All contracts include validation tags for runtime safety
//   - Resource tracking fields enable budget compliance and monitoring
//   - Error handling follows consistent patterns across all operations
//
// Evaluation Process Integration Patterns:
//   - Operations are stateless and idempotent where possible
//   - Large content stored as artifacts to minimize workflow state size
//   - Token and cost tracking enables resource-aware orchestration
//   - Timeouts and retries configured at the operation level
//
// Operation Lifecycle:
//  1. Workflow creates typed input with validation
//  2. Operation executor validates input before processing
//  3. External services (LLM providers) called with proper error handling
//  4. Results validated and resources tracked before returning
//  5. Failures categorized for appropriate retry/escalation behavior
package domain

// Operation Contract Locations:
//
// Answer Generation Operations (answer.go):
//   - GenerateAnswersInput: Question and configuration for LLM answer generation
//   - GenerateAnswersOutput: Generated answers with resource usage tracking
//
// Scoring Operations (score.go):
//   - ScoreAnswersInput: Answers and configuration for evaluation scoring
//   - ScoreAnswersOutput: Evaluation scores with judge attribution and metrics
//   - AggregateScoresInput: Multiple scores for statistical aggregation
//   - AggregateScoresOutput: Aggregated results with winner determination
//
// Contract Design Principles:
//   - Co-location: Operation contracts live with their related domain types
//   - Validation: All inputs and outputs include comprehensive validation rules
//   - Resource Tracking: Token usage, API calls, and latency are consistently tracked
//   - Error Transparency: Failed operations include detailed error context
//   - Audit Support: All contracts include metadata for tracing and debugging
//
// Operation Error Handling:
//   - Validation errors are non-retryable and indicate programming errors
//   - Resource limit errors may be retryable with backoff strategies
//   - Provider errors (rate limits, outages) follow exponential backoff
//   - Business logic errors (invalid responses) are logged but may continue
//
// This architecture enables reliable, observable, and cost-controlled LLM evaluation
// processes while maintaining clear separation between orchestration and execution.
