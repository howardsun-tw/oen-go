package oen

import "net/http"

// CodeInfo describes one non-success code from Oen's official Payment API
// error table.
type CodeInfo struct {
	// Code is the code in upper case, such as "T0004".
	Code string
	// HTTPStatus is the status Oen documents for the code.
	HTTPStatus int
	// Kind is how the SDK classifies an [Error] that carries the code at its
	// documented status. The HTTP status takes precedence, so a 429 or 5xx
	// response with the same code has another kind; decide on the returned
	// error with [Error.Kind] and the helpers, not on this field.
	Kind Kind
	// Description says what the code means, in English.
	Description string
	// OfficialDescription is Oen's own meaning of the code (意思), in
	// Traditional Chinese. Quote it when contacting Oen.
	OfficialDescription string
	// Action is what the caller should do next, following this SDK's
	// recovery guidance.
	Action string
}

// LookupCode describes a response code from the "code" field of a Payment
// API response. It accepts the same spellings the SDK classifies, ignoring
// case and surrounding spaces, and reports false for S0000 and for codes
// outside Oen's table.
//
// It does not describe the payment_error parameter that Oen adds to a
// checkout failureUrl: there V0003 means the result is still pending, not an
// amount over the limit. The card-update page codes (C0000 to C0013) are not
// included, because no SDK method calls that endpoint.
func LookupCode(code string) (CodeInfo, bool) {
	info, ok := codeCatalog[normalizeCode(code)]
	return info, ok
}

// Description returns the English meaning of the error's provider code, or
// an empty string when the error has no code or the code is not in Oen's
// table.
func (e *Error) Description() string {
	info, _ := LookupCode(e.Code)
	return info.Description
}

// codeCatalog is Oen's error table (https://developer.oen.tw/api/error-codes/,
// checked 2026-10-30). OfficialDescription is copied from its 意思 column;
// Action follows the README where the SDK's guidance is stricter than Oen's.
var codeCatalog = func() map[string]CodeInfo {
	entries := []CodeInfo{
		{
			Code: CodeUnauthorized, HTTPStatus: http.StatusUnauthorized, Kind: KindUnauthorized,
			Description:         "Unauthorized: the token is missing, malformed or not the domain's current token, or API access is off for the domain.",
			OfficialDescription: "未授權：token 缺少、格式錯誤、不是這個網域目前的 token，或網域沒有開啟 API 串接",
			Action:              "Send Authorization: Bearer <token> with the token of the same environment (testing or production).",
		},
		{
			Code: CodeTokenExpired, HTTPStatus: http.StatusUnauthorized, Kind: KindUnauthorized,
			Description:         "The token has expired, or it is an old token past its grace period after regeneration.",
			OfficialDescription: "token 已過期：超過到期日（新產生的 token 預設一年），或重新產生後已超過保留期限的舊 token",
			Action:              "Regenerate the token in the CRM (總設定 → 開發者) and update the server configuration.",
		},
		{
			Code: CodeInvalidRequest, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "The request parameters are not valid.",
			OfficialDescription: "請求參數錯誤",
			Action:              "Correct the request from Message, which does not name the field path. If the request carried an Idempotency-Key, send the corrected request with a new key.",
		},
		{
			Code: CodeInvalidTransactionState, HTTPStatus: http.StatusBadRequest, Kind: KindTransactionState,
			Description:         "The operation is not allowed: payments are not activated, or the transaction or subscription is not in a state that allows it.",
			OfficialDescription: "狀態不允許：金流尚未開通，或交易、定期定額目前的狀態不能做這個動作",
			Action:              "Query the current state before deciding.",
		},
		{
			Code: CodeAmountOverLimit, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "The amount is over the single-payment limit. 200,000 TWD or more needs high-value payments activated.",
			OfficialDescription: "超過單筆最高金額（200,000 元以上需要開通高額交易）",
			Action:              "Split the payment, or ask Oen to activate high-value payments.",
		},
		{
			Code: CodeMissingPaymentMethods, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "strictPaymentMethods is true but the request has no payment method list.",
			OfficialDescription: "只開指定付款方式（strictPaymentMethods: true）時沒有帶付款方式清單",
			Action:              "Send a non-empty allowedPaymentMethods.",
		},
		{
			Code: CodePaymentMethodInactive, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "A payment method in the strict list is not activated for the domain.",
			OfficialDescription: "只開指定付款方式時，清單裡有網域還沒開通的方式",
			Action:              "Message names the method. Activate it, or remove it from the list.",
		},
		{
			Code: CodeNoPaymentMethodForAmount, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "No payment method in the strict list accepts the amount.",
			OfficialDescription: "只開指定付款方式時，清單裡沒有任何方式接受這個金額",
			Action:              "Add credit card to the list, or change the amount.",
		},
		{
			Code: CodeCurrencyNotSupported, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "The currency is not TWD, which strict payment methods require.",
			OfficialDescription: "只開指定付款方式時，幣別不是新台幣",
			Action:              "Use TWD.",
		},
		{
			Code: CodeTransactionFailed, HTTPStatus: http.StatusBadRequest, Kind: KindDeclined,
			Description:         "The payment failed for a general reason.",
			OfficialDescription: "交易失敗（一般原因）",
			Action:              "Ask the customer to use another card or to try again later.",
		},
		{
			Code: CodeInvalidCVV, HTTPStatus: http.StatusBadRequest, Kind: KindDeclined,
			Description:         "The card security code (CVV) is wrong.",
			OfficialDescription: "安全碼錯誤",
			Action:              "Ask the customer to check the card details.",
		},
		{
			Code: CodeCardExpired, HTTPStatus: http.StatusBadRequest, Kind: KindDeclined,
			Description:         "The card has expired.",
			OfficialDescription: "卡片過期",
			Action:              "Ask the customer to use another card.",
		},
		{
			Code: CodeInsufficientFunds, HTTPStatus: http.StatusBadRequest, Kind: KindDeclined,
			Description:         "The card has insufficient funds or credit.",
			OfficialDescription: "額度不足",
			Action:              "Ask the customer to use another card or to contact the issuing bank.",
		},
		{
			Code: CodeCardDeclined, HTTPStatus: http.StatusBadRequest, Kind: KindDeclined,
			Description:         "The issuing bank declined the authorization.",
			OfficialDescription: "發卡銀行拒絕授權",
			Action:              "Ask the customer to contact the issuing bank or to use another card.",
		},
		{
			Code: CodeNon3DChargeUnavailable, HTTPStatus: http.StatusBadRequest, Kind: KindDeclined,
			Description:         "The domain's card settings cannot charge without 3-D Secure, because the merchant number is missing.",
			OfficialDescription: "網域的信用卡設定還不能做免 3D 扣款（缺少特店代號）",
			Action:              "Contact Oen.",
		},
		{
			Code: CodeTooManyDeclines, HTTPStatus: http.StatusBadRequest, Kind: KindDeclined,
			Description:         "The token was declined too many times in a short period, so Oen did not send this charge.",
			OfficialDescription: "同一個 token 短時間內被明確拒絕太多次，這次沒有送出扣款",
			Action:              "Stop retrying with this token. Ask the customer to check the card or to bind it again; the token recovers after a while.",
		},
		{
			Code: CodeRefundFailed, HTTPStatus: http.StatusBadRequest, Kind: KindUnknownOutcome,
			Description:         "The refund failed.",
			OfficialDescription: "退款失敗",
			Action:              "Query the transaction first. If later attempts all return V0002, the refund needs manual review; contact Oen.",
		},
		{
			Code: CodeRiskRejected, HTTPStatus: http.StatusBadRequest, Kind: KindDeclined,
			Description:         "Risk control refused the payment: an individual merchant's monthly total or single amount is over the threshold.",
			OfficialDescription: "風控拒絕：個人身分商家的當月累計或單筆金額超過門檻",
			Action:              "Do not retry. Contact Oen.",
		},
		{
			Code: CodePaymentUnknownOutcome, HTTPStatus: http.StatusConflict, Kind: KindUnknownOutcome,
			Description:         "The payment result is unknown. The charge may already have happened.",
			OfficialDescription: "付款結果不明，回應帶 \"retryable\": false",
			Action:              "Do not retry, and never charge again with a new key or order. Query the transaction by order ID; contact Oen if it stays charging.",
		},
		{
			Code: CodeIdempotencyConflict, HTTPStatus: http.StatusUnprocessableEntity, Kind: KindInvalidRequest,
			Description:         "The Idempotency-Key was already used with a different request (method, path or body).",
			OfficialDescription: "同一把 Idempotency-Key 用在不同的請求（method、路徑或 body 不同）",
			Action:              "Reconcile the original operation, which may already have applied. Use a new key only for a genuinely new request.",
		},
		{
			Code: CodeIdempotencyInProgress, HTTPStatus: http.StatusConflict, Kind: KindUnknownOutcome,
			Description:         "The first request with this Idempotency-Key is still processing.",
			OfficialDescription: "同一把 Idempotency-Key 的第一次請求還在處理",
			Action:              "Wait, then resend the identical request with the same key, or query the result.",
		},
		{
			Code: CodeIdempotencyUnknownOutcome, HTTPStatus: http.StatusConflict, Kind: KindUnknownOutcome,
			Description:         "The first request with this Idempotency-Key started a charge or refund and stopped without a result.",
			OfficialDescription: "同一把 Idempotency-Key 的第一次請求已開始扣款或退款，之後中斷、沒有結果。回應帶 detail.resourceId（交易或定期定額編號）",
			Action:              "Never resend with a new key. Query with Error.ResourceID or the order ID; contact Oen if it stays undetermined.",
		},
		{
			Code: CodeSystemError, HTTPStatus: http.StatusInternalServerError, Kind: KindUnknownOutcome,
			Description:         "A system error, or an error Oen does not classify.",
			OfficialDescription: "系統錯誤或未分類的錯誤",
			Action:              "Read Message: \"Invalid date\" and \"Pagination parse failed\" are bad query parameters, and \"Current charged amount too low\" means the domain's unsettled amount is below 200 TWD during a refund; contact Oen. Otherwise retry a query later, and query before resending a charge or refund.",
		},
	}
	catalog := make(map[string]CodeInfo, len(entries))
	for _, entry := range entries {
		catalog[entry.Code] = entry
	}
	return catalog
}()
