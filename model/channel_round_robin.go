package model

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var codexRoundRobinModels = map[string]struct{}{
	"gpt-5.5":       {},
	"gpt-5.4":       {},
	"gpt-5.3-codex": {},
}

var channelRoundRobinCounters sync.Map

func normalizeCodexRoundRobinModelName(modelName string) string {
	normalized := strings.TrimSpace(modelName)
	if strings.HasSuffix(normalized, ratio_setting.CompactModelSuffix) {
		normalized = strings.TrimSuffix(normalized, ratio_setting.CompactModelSuffix)
	}
	return ratio_setting.FormatMatchingModelName(normalized)
}

func IsCodexRoundRobinModel(modelName string) bool {
	_, ok := codexRoundRobinModels[normalizeCodexRoundRobinModelName(modelName)]
	return ok
}

func getChannelSelectionModelCandidates(modelName string) []string {
	candidates := make([]string, 0, 4)
	addCandidate := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range candidates {
			if existing == value {
				return
			}
		}
		candidates = append(candidates, value)
	}

	normalized := strings.TrimSpace(modelName)
	addCandidate(normalized)
	addCandidate(ratio_setting.FormatMatchingModelName(normalized))

	if IsCodexRoundRobinModel(normalized) && strings.HasSuffix(normalized, ratio_setting.CompactModelSuffix) {
		baseModel := strings.TrimSuffix(normalized, ratio_setting.CompactModelSuffix)
		addCandidate(baseModel)
		addCandidate(ratio_setting.FormatMatchingModelName(baseModel))
	}

	return candidates
}

func getNextChannelRoundRobinStart(group string, modelName string, total int) int {
	if total <= 1 {
		return 0
	}

	key := group + ":" + normalizeCodexRoundRobinModelName(modelName)
	counterAny, _ := channelRoundRobinCounters.LoadOrStore(key, &atomic.Uint64{})
	counter := counterAny.(*atomic.Uint64)
	return int((counter.Add(1) - 1) % uint64(total))
}
