package meter

// EstimateMicroCents reserves for prompt + the enforced completion cap.
func EstimateMicroCents(promptTokens int, completionCap int, price Price) int64 {
	if completionCap <= 0 {
		completionCap = 1
	}
	return ToMicroCents(CostUSD(promptTokens, completionCap, price))
}
