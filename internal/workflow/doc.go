// Package workflow implements Temporal workflow definitions for the go-judgy platform.
//
// This package contains the workflow orchestration logic that coordinates
// long-running business processes using the Temporal workflow engine.
// Workflows define the high-level business process flow, state management,
// and coordination between various activities.
//
// Key responsibilities include:
//
//   - Workflow definition and registration
//   - State management and persistence
//   - Error handling and retry policies
//   - Child workflow coordination
//   - Signal and query handling
//
// All workflows in this package follow Temporal best practices:
//
//   - Deterministic execution
//   - Proper error handling
//   - Versioning support
//   - Observability integration
//
// Workflows should not contain any non-deterministic operations
// such as random number generation, system time access, or external I/O.
// Such operations should be delegated to activities.
package workflow
