package service

// NormalizeCacheCreationSplit reconciles aggregate cache creation tokens with Claude's 5m/1h split.
func NormalizeCacheCreationSplit(total, fiveMinute, oneHour int) (int, int) {
	if total <= 0 {
		return fiveMinute, oneHour
	}
	if fiveMinute < 0 {
		fiveMinute = 0
	}
	if oneHour < 0 {
		oneHour = 0
	}
	splitTotal := fiveMinute + oneHour
	if splitTotal >= total {
		return fiveMinute, oneHour
	}
	return fiveMinute + (total - splitTotal), oneHour
}
