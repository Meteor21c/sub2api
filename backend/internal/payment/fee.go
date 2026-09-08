package payment

import (
	"github.com/shopspring/decimal"
)

func CalculatePayAmount(rechargeAmount float64, feeRate float64) string {
	return CalculatePayAmountForCurrency(rechargeAmount, feeRate, DefaultPaymentCurrency)
}

// CalculatePayAmountForCurrency 按币种精度计算应付金额，手续费向上取整到该币种最小支付单位。
func CalculatePayAmountForCurrency(rechargeAmount float64, feeRate float64, currency string) string {
	fractionDigits := int32(CurrencyMaxFractionDigits(currency))
	amount := decimal.NewFromFloat(rechargeAmount)
	if feeRate == 0 {
		return amount.StringFixed(fractionDigits)
	}
	rate := decimal.NewFromFloat(feeRate)
	fee := amount.Mul(rate).Div(decimal.NewFromInt(100))
	if feeRate > 0 {
		// Preserve the existing surcharge behavior: the fee itself is rounded
		// upward before it is added to the credited amount.
		return amount.Add(fee.RoundUp(fractionDigits)).StringFixed(fractionDigits)
	}
	// Discounts are represented by negative rates. Round the final amount
	// upward to the currency's smallest unit so a discount never makes the
	// gateway charge less than the configured percentage after rounding.
	return amount.Add(fee).RoundUp(fractionDigits).StringFixed(fractionDigits)
}
