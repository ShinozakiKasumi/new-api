package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

func formatNotifyType(channelId int, status int) string {
	return fmt.Sprintf("%s_%d_%d", dto.NotifyTypeChannelUpdate, channelId, status)
}

const codexUsageLimitReachedFragment = "the usage limit has been reached"

func disableChannel(channelError types.ChannelError, reason string, force bool) bool {
	common.SysLog(fmt.Sprintf("通道「%s」（#%d）发生错误，准备禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, reason))

	if !force && !channelError.AutoBan {
		common.SysLog(fmt.Sprintf("通道「%s」（#%d）未启用自动禁用功能，跳过禁用操作", channelError.ChannelName, channelError.ChannelId))
		return false
	}

	success := model.UpdateChannelStatus(channelError.ChannelId, channelError.UsingKey, common.ChannelStatusAutoDisabled, reason)
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）已被禁用", channelError.ChannelName, channelError.ChannelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, reason)
		NotifyRootUser(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), subject, content)
	}
	return success
}

// disable & notify
func DisableChannel(channelError types.ChannelError, reason string) {
	disableChannel(channelError, reason, false)
}

func ForceDisableChannel(channelError types.ChannelError, reason string) bool {
	return disableChannel(channelError, reason, true)
}

func EnableChannel(channelId int, usingKey string, channelName string) {
	success := model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusEnabled, "")
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		NotifyRootUser(formatNotifyType(channelId, common.ChannelStatusEnabled), subject, content)
	}
}

func ShouldDisableChannel(err *types.NewAPIError) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if operation_setting.ShouldDisableByStatusCode(err.StatusCode) {
		return true
	}

	lowerMessage := strings.ToLower(err.Error())
	search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true)
	return search
}

func IsCodexUsageLimitExceeded(channelError types.ChannelError, err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if channelError.ChannelType != constant.ChannelTypeCodex {
		return false
	}
	if err.StatusCode != 429 {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), codexUsageLimitReachedFragment)
}

func HandleCodexUsageLimitExceeded(channelError types.ChannelError, err *types.NewAPIError) int {
	if !IsCodexUsageLimitExceeded(channelError, err) {
		return 0
	}

	channel, getErr := model.CacheGetChannel(channelError.ChannelId)
	if getErr != nil || channel == nil {
		channel, _ = model.GetChannelById(channelError.ChannelId, true)
	}
	if channel == nil {
		return disableCodexUsageLimitedChannel(channelError, err)
	}

	if !channel.GetOtherSettings().CodexUsageLimitAutoDelete {
		return disableCodexUsageLimitedChannel(channelError, err)
	}

	deleted, handled := autoDeleteCodexUsageLimitedChannelOrKey(channel, channelError, err.ErrorWithStatusCode())
	if !handled {
		return disableCodexUsageLimitedChannel(channelError, err)
	}
	return deleted
}

func disableCodexUsageLimitedChannel(channelError types.ChannelError, err *types.NewAPIError) int {
	reason := err.ErrorWithStatusCode()
	ForceDisableChannel(channelError, reason)

	channel, getErr := model.CacheGetChannel(channelError.ChannelId)
	if getErr != nil || channel == nil {
		channel, _ = model.GetChannelById(channelError.ChannelId, true)
	}
	if channel == nil || channel.Status == common.ChannelStatusEnabled {
		return 0
	}

	deleted := ClearChannelAffinityCacheByChannelID(channelError.ChannelId)
	if deleted > 0 {
		common.SysLog(fmt.Sprintf("Codex 通道 #%d 已清理 %d 条 channel affinity 缓存", channelError.ChannelId, deleted))
	}
	return deleted
}

func autoDeleteCodexUsageLimitedChannelOrKey(channel *model.Channel, channelError types.ChannelError, reason string) (int, bool) {
	keys := channel.GetKeys()
	if !channel.ChannelInfo.IsMultiKey || len(keys) <= 1 {
		if err := model.BatchDeleteChannels([]int{channel.Id}); err != nil {
			common.SysLog(fmt.Sprintf("Codex 通道 #%d usage limit 自动删除渠道失败：%s", channel.Id, err.Error()))
			return 0, false
		}
		model.InitChannelCache()
		deleted := clearCodexUsageLimitAffinity(channel.Id)
		subject := fmt.Sprintf("Codex 通道「%s」（#%d）已被删除", channel.Name, channel.Id)
		content := fmt.Sprintf("Codex 通道「%s」（#%d）触发 usage limit，已自动删除渠道。原因：%s", channel.Name, channel.Id, reason)
		NotifyRootUser(formatCodexUsageLimitAutoDeleteNotifyType(channel.Id), subject, content)
		return deleted, true
	}

	keyIndex := findUsingKeyIndex(keys, channelError.UsingKey)
	if keyIndex < 0 {
		common.SysLog(fmt.Sprintf("Codex 通道 #%d usage limit 自动删除密钥失败：无法定位当前密钥", channel.Id))
		return 0, false
	}

	if err := channel.DeleteMultiKeyByIndex(keyIndex); err != nil {
		common.SysLog(fmt.Sprintf("Codex 通道 #%d usage limit 自动删除密钥失败：%s", channel.Id, err.Error()))
		return 0, false
	}
	model.InitChannelCache()
	deleted := clearCodexUsageLimitAffinity(channel.Id)
	subject := fmt.Sprintf("Codex 通道「%s」（#%d）已删除限额密钥", channel.Name, channel.Id)
	content := fmt.Sprintf("Codex 通道「%s」（#%d）第 %d 个密钥触发 usage limit，已自动删除该密钥。原因：%s", channel.Name, channel.Id, keyIndex+1, reason)
	NotifyRootUser(formatCodexUsageLimitAutoDeleteNotifyType(channel.Id), subject, content)
	return deleted, true
}

func findUsingKeyIndex(keys []string, usingKey string) int {
	if usingKey == "" {
		return -1
	}
	for i, key := range keys {
		if key == usingKey {
			return i
		}
	}
	return -1
}

func clearCodexUsageLimitAffinity(channelID int) int {
	deleted := ClearChannelAffinityCacheByChannelID(channelID)
	if deleted > 0 {
		common.SysLog(fmt.Sprintf("Codex 通道 #%d 已清理 %d 条 channel affinity 缓存", channelID, deleted))
	}
	return deleted
}

func formatCodexUsageLimitAutoDeleteNotifyType(channelID int) string {
	return fmt.Sprintf("%s_%d_codex_usage_limit_auto_delete", dto.NotifyTypeChannelUpdate, channelID)
}

func ShouldEnableChannel(newAPIError *types.NewAPIError, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if newAPIError != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}
