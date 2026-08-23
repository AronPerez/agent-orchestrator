package chat

import "time"

// SetNativeHistorySettleLimitForTest shrinks the native-history settle window and
// returns a restore func, so a test can reach the deadline arm of
// importNativeHistory without waiting out the production limit.
func SetNativeHistorySettleLimitForTest(d time.Duration) func() {
	previous := nativeHistorySettleLimit
	nativeHistorySettleLimit = d
	return func() { nativeHistorySettleLimit = previous }
}
