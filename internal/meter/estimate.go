package meter

import "unicode/utf8"

// ReserveCompletionTokens is a near-actual completion hold for short mock replies.
// Low enough that the budget-demo key ($0.00001) can admit one short request
// then reject the next after settle overshoots.
const ReserveCompletionTokens = 8

// EstimatePromptTokens approximates prompt size without a tokenizer.
func EstimatePromptTokens(contents ...string) int {
	n := 0
	for _, c := range contents {
		n += utf8.RuneCountInString(c)
	}
	tokens := n / 4
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// EstimateMicroCents reserves for prompt + a small completion cap on the resolved model.
func EstimateMicroCents(promptTokens int, completionCap int, price Price) int64 {
	if completionCap <= 0 {
		completionCap = ReserveCompletionTokens
	}
	return ToMicroCents(CostUSD(promptTokens, completionCap, price))
}
