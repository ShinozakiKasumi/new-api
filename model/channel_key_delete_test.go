package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestRebuildMultiKeyMetadataAfterDeleteReindexesStatusMaps(t *testing.T) {
	keys := []string{"key-a", "key-b", "key-c", "key-d"}
	info := ChannelInfo{
		MultiKeyStatusList: map[int]int{
			1: common.ChannelStatusAutoDisabled,
			3: common.ChannelStatusManuallyDisabled,
		},
		MultiKeyDisabledTime: map[int]int64{
			1: 111,
			3: 333,
		},
		MultiKeyDisabledReason: map[int]string{
			1: "usage limit",
			3: "manual",
		},
	}

	remainingKeys, statusList, disabledTime, disabledReason, err := rebuildMultiKeyMetadataAfterDelete(keys, info, 1)

	require.NoError(t, err)
	require.Equal(t, []string{"key-a", "key-c", "key-d"}, remainingKeys)
	require.Equal(t, map[int]int{2: common.ChannelStatusManuallyDisabled}, statusList)
	require.Equal(t, map[int]int64{2: 333}, disabledTime)
	require.Equal(t, map[int]string{2: "manual"}, disabledReason)
}

func TestRebuildMultiKeyMetadataAfterDeleteRejectsLastKey(t *testing.T) {
	_, _, _, _, err := rebuildMultiKeyMetadataAfterDelete([]string{"key-a"}, ChannelInfo{}, 0)

	require.Error(t, err)
}
