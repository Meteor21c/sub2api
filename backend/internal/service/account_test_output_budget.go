package service

import "github.com/gin-gonic/gin"

const (
	accountTestOutputBudgetContextKey = "account_test_output_budget"
	accountTestDebugOutputBudget      = 8192
	accountTestTruncatedOutputError   = "Upstream output was truncated (stop_reason=max_tokens); partial response preserved"
)

// scopeAccountTestOutputBudget applies only to the dedicated debug probe. A
// restored nil value has the same meaning as an absent budget to the reader.
func scopeAccountTestOutputBudget(c *gin.Context, budget int) func() {
	if c == nil {
		return func() {}
	}
	previous, _ := c.Get(accountTestOutputBudgetContextKey)
	c.Set(accountTestOutputBudgetContextKey, budget)
	return func() { c.Set(accountTestOutputBudgetContextKey, previous) }
}

func applyAccountTestOutputBudget(c *gin.Context, payload map[string]any) {
	if c == nil {
		return
	}
	value, _ := c.Get(accountTestOutputBudgetContextKey)
	if budget, ok := value.(int); ok && budget > 0 {
		payload["max_tokens"] = budget
	}
}
