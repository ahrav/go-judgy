//go:build integration
// +build integration

package ratelimit

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	redisContainer "github.com/testcontainers/testcontainers-go/modules/redis"
)

// setupRedisContainer creates and configures a real Redis container for integration testing.
// It returns the container instance and a connected Redis client.
// The container is automatically terminated when the test completes.
func setupRedisContainer(t testing.TB) (*redisContainer.RedisContainer, *redis.Client) {
	ctx := context.Background()

	// Start Redis container
	container, err := redisContainer.Run(ctx, "redis:7-alpine")
	require.NoError(t, err)

	// Clean up container when test finishes
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("failed to terminate Redis container: %v", err)
		}
	})

	// Get connection endpoint
	endpoint, err := container.Endpoint(ctx, "")
	require.NoError(t, err)

	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr: endpoint,
		DB:   1, // Use test database
	})

	// Verify connection
	_, err = client.Ping(ctx).Result()
	require.NoError(t, err)

	return container, client
}
