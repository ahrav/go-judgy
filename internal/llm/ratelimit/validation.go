package ratelimit

import (
	"fmt"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
)

// validateRateLimitConfig performs comprehensive validation of rate limiting configuration.
//
// This function orchestrates validation of both local and global rate limiting
// settings to prevent security vulnerabilities and ensure correct operation.
func validateRateLimitConfig(cfg *configuration.RateLimitConfig) error {
	if err := validateLocalRateLimitConfig(cfg.Local); err != nil {
		return err
	}

	if err := validateGlobalRateLimitConfig(&cfg.Global); err != nil {
		return err
	}

	return nil
}

// validateLocalRateLimitConfig validates the local rate limit configuration.
//
// This function ensures that local rate limiting parameters are non-negative
// and enforce the business rule that BurstSize must be 0 when TokensPerSecond is 0.
func validateLocalRateLimitConfig(cfg configuration.LocalRateLimitConfig) error {
	if !cfg.Enabled {
		return nil // Skip validation when local limiting is disabled
	}

	if cfg.TokensPerSecond < 0 {
		return fmt.Errorf("invalid local rate limit: TokensPerSecond cannot be negative (got %f)", cfg.TokensPerSecond)
	}
	if cfg.BurstSize < 0 {
		return fmt.Errorf("invalid local rate limit: BurstSize cannot be negative (got %d)", cfg.BurstSize)
	}
	if cfg.TokensPerSecond == 0 && cfg.BurstSize > 0 {
		return fmt.Errorf("invalid local rate limit: BurstSize must be 0 when TokensPerSecond is 0")
	}

	return nil
}

// validateGlobalRateLimitConfig validates the global rate limit configuration.
//
// This function ensures that global rate limiting parameters are non-negative.
func validateGlobalRateLimitConfig(cfg *configuration.GlobalRateLimitConfig) error {
	if !cfg.Enabled {
		return nil // Skip validation when global limiting is disabled
	}

	if cfg.RequestsPerSecond < 0 {
		return fmt.Errorf("invalid global rate limit: RequestsPerSecond cannot be negative (got %d)", cfg.RequestsPerSecond)
	}

	return nil
}
