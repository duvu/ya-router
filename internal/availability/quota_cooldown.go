package availability

// CooldownQuotaExhausted indicates a provider/model target exhausted its
// usable quota and must remain unavailable until its long retry window expires.
// The value is bounded and never contains provider-supplied text.
const CooldownQuotaExhausted CooldownReason = "quota_exhausted"
