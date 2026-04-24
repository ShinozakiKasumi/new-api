package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestIsCodexRoundRobinModel(t *testing.T) {
	require.True(t, IsCodexRoundRobinModel("gpt-5.5"))
	require.True(t, IsCodexRoundRobinModel("gpt-5.4"))
	require.True(t, IsCodexRoundRobinModel("gpt-5.3-codex"))
	require.True(t, IsCodexRoundRobinModel(ratio_setting.WithCompactModelSuffix("gpt-5.5")))
	require.False(t, IsCodexRoundRobinModel("gpt-5.2"))
}

func TestGetRandomSatisfiedChannel_CodexRoundRobin(t *testing.T) {
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldGroup2Model2Channels := group2model2channels
	oldChannelsIDM := channelsIDM

	common.MemoryCacheEnabled = true
	channelRoundRobinCounters = sync.Map{}

	priorityHigh := int64(10)
	priorityLow := int64(5)

	channelSyncLock.Lock()
	group2model2channels = map[string]map[string][]int{
		"default": {
			"gpt-5.5": {3, 2, 1},
		},
	}
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Priority: &priorityHigh},
		2: {Id: 2, Priority: &priorityHigh},
		3: {Id: 3, Priority: &priorityLow},
	}
	channelSyncLock.Unlock()

	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		channelRoundRobinCounters = sync.Map{}
		channelSyncLock.Lock()
		group2model2channels = oldGroup2Model2Channels
		channelsIDM = oldChannelsIDM
		channelSyncLock.Unlock()
	})

	got := make([]int, 0, 4)
	for range 4 {
		channel, err := GetRandomSatisfiedChannel("default", "gpt-5.5", 0)
		require.NoError(t, err)
		require.NotNil(t, channel)
		got = append(got, channel.Id)
	}

	require.Equal(t, []int{1, 2, 3, 1}, got)
}

func TestGetRandomSatisfiedChannel_CodexRoundRobinCompactFallback(t *testing.T) {
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldGroup2Model2Channels := group2model2channels
	oldChannelsIDM := channelsIDM

	common.MemoryCacheEnabled = true
	channelRoundRobinCounters = sync.Map{}

	priorityHigh := int64(10)

	channelSyncLock.Lock()
	group2model2channels = map[string]map[string][]int{
		"default": {
			"gpt-5.5": {7},
		},
	}
	channelsIDM = map[int]*Channel{
		7: {Id: 7, Priority: &priorityHigh},
	}
	channelSyncLock.Unlock()

	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		channelRoundRobinCounters = sync.Map{}
		channelSyncLock.Lock()
		group2model2channels = oldGroup2Model2Channels
		channelsIDM = oldChannelsIDM
		channelSyncLock.Unlock()
	})

	channel, err := GetRandomSatisfiedChannel("default", ratio_setting.WithCompactModelSuffix("gpt-5.5"), 0)
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 7, channel.Id)
}

func TestGetChannel_CodexRoundRobin(t *testing.T) {
	truncateTables(t)

	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	channelRoundRobinCounters = sync.Map{}

	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		channelRoundRobinCounters = sync.Map{}
	})

	priorityHigh := int64(10)
	priorityLow := int64(5)

	channels := []Channel{
		{Id: 11, Name: "codex-a", Key: "key-a", Status: common.ChannelStatusEnabled, Group: "default", Models: "gpt-5.4", Priority: &priorityHigh},
		{Id: 12, Name: "codex-b", Key: "key-b", Status: common.ChannelStatusEnabled, Group: "default", Models: "gpt-5.4", Priority: &priorityHigh},
		{Id: 13, Name: "codex-c", Key: "key-c", Status: common.ChannelStatusEnabled, Group: "default", Models: "gpt-5.4", Priority: &priorityLow},
	}
	require.NoError(t, DB.Create(&channels).Error)

	abilities := []Ability{
		{Group: "default", Model: "gpt-5.4", ChannelId: 11, Enabled: true, Priority: &priorityHigh},
		{Group: "default", Model: "gpt-5.4", ChannelId: 12, Enabled: true, Priority: &priorityHigh},
		{Group: "default", Model: "gpt-5.4", ChannelId: 13, Enabled: true, Priority: &priorityLow},
	}
	require.NoError(t, DB.Create(&abilities).Error)

	got := make([]int, 0, 4)
	for retry := range 4 {
		channel, err := GetChannel("default", "gpt-5.4", retry)
		require.NoError(t, err)
		require.NotNil(t, channel)
		got = append(got, channel.Id)
	}
	require.Equal(t, []int{11, 12, 13, 11}, got)

	channelRoundRobinCounters = sync.Map{}
	channel, err := GetChannel("default", ratio_setting.WithCompactModelSuffix("gpt-5.4"), 0)
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 11, channel.Id)
}
