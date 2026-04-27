package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestIsCodexUsageLimitExceeded(t *testing.T) {
	channelError := types.ChannelError{
		ChannelType: constant.ChannelTypeCodex,
	}
	limitErr := types.NewErrorWithStatusCode(
		errors.New("The usage limit has been reached"),
		types.ErrorCodeBadResponseStatusCode,
		429,
	)

	require.True(t, IsCodexUsageLimitExceeded(channelError, limitErr))

	otherChannel := channelError
	otherChannel.ChannelType = constant.ChannelTypeOpenAI
	require.False(t, IsCodexUsageLimitExceeded(otherChannel, limitErr))

	otherMessage := types.NewErrorWithStatusCode(
		errors.New("rate limit exceeded"),
		types.ErrorCodeBadResponseStatusCode,
		429,
	)
	require.False(t, IsCodexUsageLimitExceeded(channelError, otherMessage))
}

func TestFindUsingKeyIndex(t *testing.T) {
	keys := []string{"key-a", "key-b", "key-c"}

	require.Equal(t, 1, findUsingKeyIndex(keys, "key-b"))
	require.Equal(t, -1, findUsingKeyIndex(keys, "key-x"))
	require.Equal(t, -1, findUsingKeyIndex(keys, ""))
}

func TestClearChannelAffinityCacheByChannelID(t *testing.T) {
	cache := getChannelAffinityCache()
	seed := time.Now().UnixNano()
	key1 := fmt.Sprintf("clear-by-channel:%d:1", seed)
	key2 := fmt.Sprintf("clear-by-channel:%d:2", seed)
	key3 := fmt.Sprintf("clear-by-channel:%d:3", seed)

	require.NoError(t, cache.SetWithTTL(key1, 101, time.Minute))
	require.NoError(t, cache.SetWithTTL(key2, 101, time.Minute))
	require.NoError(t, cache.SetWithTTL(key3, 202, time.Minute))

	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{key1, key2, key3})
	})

	deleted := ClearChannelAffinityCacheByChannelID(101)
	require.Equal(t, 2, deleted)

	_, found, err := cache.Get(key1)
	require.NoError(t, err)
	require.False(t, found)

	_, found, err = cache.Get(key2)
	require.NoError(t, err)
	require.False(t, found)

	value, found, err := cache.Get(key3)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 202, value)
}
