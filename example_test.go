package oen_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/howardsun-tw/oen-go"
	"github.com/howardsun-tw/oen-go/oentest"
)

// Example shows the whole token flow against the fake server: open a hosted
// binding page, read the token off the callback, charge it, and resolve an
// answer that never arrived.
func Example() {
	server := oentest.New()
	defer server.Close()

	client, err := oen.New(oen.Config{
		// Point at Oen with Environment: oen.Testing or oen.Production. This
		// example points at the fake instead.
		BaseURL:           server.URL,
		CheckoutBaseURL:   "https://oentech.testing.oen.tw",
		MerchantID:        oentest.DefaultMerchantID,
		AuthToken:         oentest.DefaultAuthToken,
		Timeout:           10 * time.Second,
		DefaultSuccessURL: "https://merchant.example/paid",
		DefaultFailureURL: "https://merchant.example/failed",
	})
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	// 1. Send the payer to a hosted page that binds their card.
	binding, err := client.CreateTokenCheckout(ctx, oen.TokenCheckoutRequest{CustomID: "binding-1"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("send the payer to:", binding.RedirectURL)

	// 2. Oen calls the webhook with the token. Parsing is not authentication:
	// the token is a claim until the charge or a query confirms it.
	event, err := client.ParseWebhook([]byte(
		`{"merchantId":"oentech","purpose":"token","success":true,` +
			`"id":"` + binding.ID + `","customId":"binding-1",` +
			`"token":"2etM3aQSCMWv7OGYQ6gDWtcOJaR","paymentInfo":"4242"}`))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("token bound:", event.TokenBound(), "card:", event.PaymentInfo.CardLast4, "verified:", event.Verified)

	// 3. Charge the token. The order reference is the caller's own and is what
	// makes step 4 possible.
	result, err := client.ChargeToken(ctx, oen.TokenChargeRequest{
		OrderID: "ORDER00001",
		Token:   event.Token,
		Amount:  1000,
		Items: []oen.LineItem{
			{ProductionCode: "P0001", Description: "monthly plan", Quantity: 1, Unit: "個", UnitPrice: 1000},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("charged:", result.TransactionHID)

	// 4. The next charge times out. The SDK never resends it; the caller asks
	// Oen what actually happened.
	server.ChargeThenFail("/token/transactions", http.StatusGatewayTimeout)
	_, err = client.ChargeToken(ctx, oen.TokenChargeRequest{
		OrderID: "ORDER00002",
		Token:   event.Token,
		Amount:  1000,
		Items: []oen.LineItem{
			{ProductionCode: "P0001", Description: "monthly plan", Quantity: 1, Unit: "個", UnitPrice: 1000},
		},
	})
	switch {
	case errors.Is(err, oen.ErrDeclined):
		fmt.Println("declined, nothing to reconcile")
	case errors.Is(err, oen.ErrUnknownOutcome):
		found, queryErr := client.ListOrderTransactions(ctx, "ORDER00002")
		if queryErr != nil {
			log.Fatal(queryErr)
		}
		fmt.Println("uncertain; provider holds", len(found), "transaction, paid:", found[0].Status.IsPaid())
	case err != nil:
		log.Fatal(err)
	}

	// Output:
	// send the payer to: https://oentech.testing.oen.tw/checkout/subscription/create/2HhndgEquCbDzC5OyVxWSGZmd2l
	// token bound: true card: 4242 verified: false
	// charged: P20240412QJMAZNML
	// uncertain; provider holds 1 transaction, paid: true
}

// ExampleIsDeclined shows how a caller tells the three answers apart. Only the
// first one is a settled "the money did not move".
func ExampleIsDeclined() {
	server := oentest.New()
	defer server.Close()
	server.Reject("/token/transactions", oen.CodeInsufficientFunds, "額度不足")

	client, err := oen.New(oen.Config{
		BaseURL:    server.URL,
		MerchantID: oentest.DefaultMerchantID,
		AuthToken:  oentest.DefaultAuthToken,
	})
	if err != nil {
		log.Fatal(err)
	}

	_, err = client.ChargeToken(context.Background(), oen.TokenChargeRequest{
		OrderID: "ORDER00001",
		Token:   "2etM3aQSCMWv7OGYQ6gDWtcOJaR",
		Amount:  1000,
		Items: []oen.LineItem{
			{ProductionCode: "P0001", Description: "monthly plan", Quantity: 1, Unit: "個", UnitPrice: 1000},
		},
	})

	var apiErr *oen.Error
	if errors.As(err, &apiErr) {
		fmt.Println("declined:", oen.IsDeclined(err))
		fmt.Println("reason:", apiErr.DeclineReason())
		fmt.Println("code:", apiErr.Code)
		fmt.Println("must query before retrying:", oen.IsUnknownOutcome(err))
	}

	// Output:
	// declined: true
	// reason: insufficient_funds
	// code: T0004
	// must query before retrying: false
}
