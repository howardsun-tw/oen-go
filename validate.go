package oen

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxInt64 = int64(1<<63 - 1)

// providerDateLayout is Oen's yyyy/MM/dd format for payout and start dates.
const providerDateLayout = "2006/01/02"

// validatePurchase checks everything Oen requires of an amount-bearing
// request before it is sent, and returns the currency to use.
func (c *Client) validatePurchase(op, orderID string, amount Amount, currency string, items []LineItem) (string, error) {
	if strings.TrimSpace(orderID) == "" {
		return "", newValidationError(op, "orderId", "order ID is required")
	}
	if !amount.Valid() {
		return "", newValidationError(op, "amount", "amount must be positive")
	}
	if currency == "" {
		currency = CurrencyTWD
	}
	if currency != CurrencyTWD {
		return "", newValidationError(op, "currency", fmt.Sprintf("unsupported currency %q", currency))
	}
	if err := validateItems(op, amount, items); err != nil {
		return "", err
	}
	return currency, nil
}

// validateItems rejects line items that do not add up to the amount. Oen
// answers that combination with PRODUCT_AMOUNT_NOT_MATCH, so it costs nothing
// to catch it here and it keeps a doomed request off the payment path.
func validateItems(op string, amount Amount, items []LineItem) error {
	if len(items) == 0 {
		return newValidationError(op, "productDetails", "at least one product detail is required")
	}
	total := int64(0)
	for _, item := range items {
		if item.Quantity <= 0 {
			return newValidationError(op, "productDetails", "product quantity must be positive")
		}
		if item.UnitPrice < 0 {
			return newValidationError(op, "productDetails", "product unit price must not be negative")
		}
		unitPrice, quantity := int64(item.UnitPrice), int64(item.Quantity)
		if unitPrice > maxInt64/quantity {
			return newValidationError(op, "productDetails", "product amount overflows int64")
		}
		lineTotal := unitPrice * quantity
		if total > maxInt64-lineTotal {
			return newValidationError(op, "productDetails", "product amount overflows int64")
		}
		total += lineTotal
	}
	if total != int64(amount) {
		return errProductAmountMismatch(op, amount, Amount(total))
	}
	return nil
}

func (c *Client) returnURLs(op, successURL, failureURL string) (string, string, error) {
	if successURL == "" {
		successURL = c.cfg.DefaultSuccessURL
	}
	if failureURL == "" {
		failureURL = c.cfg.DefaultFailureURL
	}
	if successURL == "" {
		return "", "", newValidationError(op, "successUrl", "success URL is required; set it on the request or in Config.DefaultSuccessURL")
	}
	if failureURL == "" {
		return "", "", newValidationError(op, "failureUrl", "failure URL is required; set it on the request or in Config.DefaultFailureURL")
	}
	return successURL, failureURL, nil
}

func validateSchedule(op string, numberOfPeriods, paymentInterval int, startDate string) error {
	if err := validatePeriods(op, numberOfPeriods); err != nil {
		return err
	}
	if paymentInterval < 0 || paymentInterval > 12 {
		return newValidationError(op, "paymentInterval", "payment interval must be between 1 and 12 months")
	}
	return validateProviderDate(op, "startDate", startDate)
}

func validatePeriods(op string, numberOfPeriods int) error {
	if numberOfPeriods < 0 {
		return newValidationError(op, "numberOfPeriods", "number of periods must not be negative")
	}
	return nil
}

func validateProviderDate(op, field, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(providerDateLayout, value); err != nil {
		return newValidationError(op, field, fmt.Sprintf("%q must be formatted yyyy/MM/dd", value))
	}
	return nil
}

func errNoField(field string) error {
	return errors.New("response is missing " + field)
}
