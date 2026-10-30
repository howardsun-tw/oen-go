package subscription

import (
	"net/http"
	"strings"
)

// ErrnoInfo describes one error number from Oen's Subscription API error
// table.
type ErrnoInfo struct {
	// Errno is the error number in upper case, such as "SA002".
	Errno string
	// HTTPStatus is the status Oen documents for the error number.
	HTTPStatus int
	// Kind is how the SDK classifies an [Error] that carries the error
	// number at its documented status. A 429 response with the same errno
	// has another kind; decide on the returned error with [Error.Kind] and
	// the helpers, not on this field.
	Kind Kind
	// Description says what the error number means, in English.
	Description string
	// OfficialDescription is Oen's own meaning (意思), in Traditional
	// Chinese. Quote it when contacting Oen.
	OfficialDescription string
}

// LookupErrno describes an error number from a Subscription API error
// response. It ignores case and surrounding spaces, and reports false for
// error numbers outside Oen's table.
func LookupErrno(errno string) (ErrnoInfo, bool) {
	info, ok := errnoCatalog[strings.ToUpper(strings.TrimSpace(errno))]
	return info, ok
}

// Description returns the English meaning of the error's errno, or an empty
// string when the error has no errno or the errno is not in Oen's table.
func (e *Error) Description() string {
	info, _ := LookupErrno(e.Errno)
	return info.Description
}

// errnoCatalog is the Subscription API error table
// (https://developer.oen.tw/products/subscription-api/, checked 2026-10-30).
// OfficialDescription is copied from its 意思 column; rows that Oen groups are
// listed one error number at a time.
var errnoCatalog = func() map[string]ErrnoInfo {
	entries := []ErrnoInfo{
		// Common errors.
		{
			Errno: ErrnoValidation, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "Field validation failed. Message lists the fields and reasons.",
			OfficialDescription: "欄位驗證失敗，message 會列出欄位與原因",
		},
		{
			Errno: ErrnoPageNotFound, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "The page token does not belong to this list.",
			OfficialDescription: "page 對不到這個列表的資料",
		},
		{
			Errno: ErrnoPageUnreadable, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "The page token cannot be parsed.",
			OfficialDescription: "page 無法解析",
		},
		{
			Errno: ErrnoRecoveryLinkState, HTTPStatus: http.StatusBadRequest, Kind: KindConflict,
			Description:         "The subscription's state does not allow a recovery link. Only a subscription with a failed charge and a scheduled retry allows one.",
			OfficialDescription: "目前狀態不能建立補繳連結（只有扣款失敗且排定重試中的訂閱可以）",
		},
		{
			Errno: ErrnoInvalidKey, HTTPStatus: http.StatusUnauthorized, Kind: KindUnauthorized,
			Description:         "The API key is missing, malformed or invalid.",
			OfficialDescription: "金鑰缺少、格式錯誤或無效",
		},
		{
			Errno: ErrnoMissingScope, HTTPStatus: http.StatusForbidden, Kind: KindForbidden,
			Description:         "The API key lacks the scope this operation needs.",
			OfficialDescription: "金鑰缺少這個操作需要的 scope",
		},
		{
			Errno: ErrnoRouteNotFound, HTTPStatus: http.StatusNotFound, Kind: KindNotFound,
			Description:         "The path does not exist.",
			OfficialDescription: "路徑不存在",
		},
		{
			Errno: ErrnoDomainDisabled, HTTPStatus: http.StatusForbidden, Kind: KindForbidden,
			Description:         "The domain is disabled. Keys that were valid are refused too.",
			OfficialDescription: "網域已停用。原本有效的金鑰也會被拒",
		},
		{
			Errno: ErrnoPaymentSuspended, HTTPStatus: http.StatusUnprocessableEntity, Kind: KindForbidden,
			Description:         "The domain's payment service is suspended or paused. Keys that were valid are refused too.",
			OfficialDescription: "網域的金流服務已停權或暫停。原本有效的金鑰也會被拒",
		},

		// Subscriptions and plans.
		{
			Errno: ErrnoSubscriptionNotFound, HTTPStatus: http.StatusNotFound, Kind: KindNotFound,
			Description:         "The subscription does not exist or does not belong to this merchant.",
			OfficialDescription: "找不到訂閱，或不屬於這個商家",
		},
		{
			Errno: ErrnoCannotCancel, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The subscription's state does not allow cancellation.",
			OfficialDescription: "目前狀態不能取消、終止、恢復或變更期間",
		},
		{
			Errno: ErrnoCannotTerminate, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The subscription's state does not allow termination.",
			OfficialDescription: "目前狀態不能取消、終止、恢復或變更期間",
		},
		{
			Errno: ErrnoCannotResume, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The subscription's state does not allow resuming.",
			OfficialDescription: "目前狀態不能取消、終止、恢復或變更期間",
		},
		{
			Errno: ErrnoCannotChangePlan, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The subscription's state does not allow a plan change.",
			OfficialDescription: "目前狀態不能變更方案",
		},
		{
			Errno: ErrnoCannotChangePeriod, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The subscription's state does not allow a period change.",
			OfficialDescription: "目前狀態不能取消、終止、恢復或變更期間",
		},
		{
			Errno: ErrnoStateChanged, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The state changed after it was read. Query again before acting.",
			OfficialDescription: "讀取後狀態已改變，請重新查詢再操作",
		},
		{
			Errno: ErrnoUseRecoveryLink, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "A failed charge paused the subscription. Use a recovery link instead.",
			OfficialDescription: "因扣款失敗暫停的訂閱，請改用補繳連結",
		},
		{
			Errno: ErrnoPaymentDataUnavailable, HTTPStatus: http.StatusServiceUnavailable, Kind: KindUnknownOutcome,
			Description:         "Payment data is temporarily unavailable. Retry with the same key.",
			OfficialDescription: "暫時讀不到付款資料，可以用同一把 key 重試",
		},
		{
			Errno: ErrnoPlanNotFound, HTTPStatus: http.StatusNotFound, Kind: KindNotFound,
			Description:         "The plan does not exist or does not belong to this product.",
			OfficialDescription: "找不到方案，或不屬於這個產品",
		},
		{
			Errno: ErrnoSamePlan, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "The subscription is already on this plan.",
			OfficialDescription: "已經是這個方案",
		},
		{
			Errno: ErrnoPlanChangedToday, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "A subscription can change plan successfully once per day (Taipei time).",
			OfficialDescription: "同一筆訂閱每天（台北時間）只能成功變更方案一次",
		},
		{
			Errno: ErrnoPlanChangeInProgress, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "A plan change is already in progress.",
			OfficialDescription: "已有進行中的方案變更",
		},
		{
			Errno: ErrnoChargeInProgress, HTTPStatus: http.StatusConflict, Kind: KindInProgress,
			Description:         "A charge is still processing or its result is undetermined. This is not a failure: query later, do not resend.",
			OfficialDescription: "扣款仍在處理或結果未定。不代表失敗，請稍後查詢，不要重送",
		},
		{
			Errno: ErrnoChangeCompleted, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "This change is already complete.",
			OfficialDescription: "這筆變更已經完成",
		},
		{
			Errno: ErrnoPeriodEndMalformed, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "The period end date is malformed.",
			OfficialDescription: "期間結束日格式錯誤",
		},
		{
			Errno: ErrnoPeriodEndNotFuture, HTTPStatus: http.StatusUnprocessableEntity, Kind: KindInvalidRequest,
			Description:         "The period end date must be later than today.",
			OfficialDescription: "期間結束日必須晚於今天",
		},
		{
			Errno: ErrnoProductNotFound, HTTPStatus: http.StatusNotFound, Kind: KindNotFound,
			Description:         "The product does not exist.",
			OfficialDescription: "找不到產品",
		},
		{
			Errno: ErrnoCustomerNotFound, HTTPStatus: http.StatusNotFound, Kind: KindNotFound,
			Description:         "The customer does not exist.",
			OfficialDescription: "找不到客戶",
		},
		{
			Errno: ErrnoAcquirerUnsupported, HTTPStatus: http.StatusUnprocessableEntity, Kind: KindInvalidRequest,
			Description:         "The acquirer of the first charge does not support the Subscription API. Contact Oen sales.",
			OfficialDescription: "首期扣款的收單機構不支援 Subscription API，請洽業務人員",
		},

		// Refunds.
		{
			Errno: ErrnoNotTerminated, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The subscription is not terminated, so it cannot be refunded.",
			OfficialDescription: "訂閱尚未終止，不能退款",
		},
		{
			Errno: ErrnoNoRefundableAmount, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "Termination produced no refundable amount.",
			OfficialDescription: "終止時沒有算出可退金額",
		},
		{
			Errno: ErrnoRefundExceedsRemaining, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The amount is over the remaining refundable amount.",
			OfficialDescription: "金額超過剩餘可退額度",
		},
		{
			Errno: ErrnoTransactionBalance, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The transaction's refundable balance is not enough.",
			OfficialDescription: "交易本身的可退餘額不足",
		},
		{
			Errno: ErrnoTransactionBusy, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The transaction has another charge in progress.",
			OfficialDescription: "這筆交易另有處理中的扣款",
		},
		{
			Errno: ErrnoRefundInProgress, HTTPStatus: http.StatusConflict, Kind: KindInProgress,
			Description:         "The same refund is still processing.",
			OfficialDescription: "同一筆退款仍在處理中",
		},
		{
			Errno: ErrnoRefundKeyFailed, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "An earlier refund with this key failed. Use a new key to refund again.",
			OfficialDescription: "這把 key 先前的退款已失敗，要再退請換新的 key",
		},
		{
			Errno: ErrnoRefundRejected, HTTPStatus: http.StatusBadGateway, Kind: KindDeclined,
			Description:         "The payment processor refused the refund. Check the transaction, then use a new key to refund again.",
			OfficialDescription: "金流端拒絕這筆退款。先確認交易狀態，要再退請換新的 key",
		},
		{
			Errno: ErrnoRefundUndetermined, HTTPStatus: http.StatusServiceUnavailable, Kind: KindUnknownOutcome,
			Description:         "The refund result is undetermined. Retry with the same key.",
			OfficialDescription: "退款結果未定，請用同一把 key 重試",
		},

		// Idempotency and webhooks.
		{
			Errno: ErrnoMissingIdempotencyKey, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "The request has no Idempotency-Key.",
			OfficialDescription: "沒有帶 Idempotency-Key",
		},
		{
			Errno: ErrnoIdempotencyKeyExpired, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The Idempotency-Key has expired. Use a new one.",
			OfficialDescription: "Idempotency-Key 已逾期，請換新的",
		},
		{
			Errno: ErrnoIdempotencyKeyMismatch, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The key was already used with a request that has different content.",
			OfficialDescription: "同一把 key 用在內容不同的請求",
		},
		{
			Errno: ErrnoDeliveryNotFound, HTTPStatus: http.StatusNotFound, Kind: KindNotFound,
			Description:         "The delivery record does not exist.",
			OfficialDescription: "找不到投遞紀錄",
		},
		{
			Errno: ErrnoDeliveryInProgress, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "This delivery is in progress.",
			OfficialDescription: "這筆投遞正在處理中",
		},
		{
			Errno: ErrnoWebhookDisabled, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The webhook for this delivery is not enabled.",
			OfficialDescription: "對應的 webhook 未啟用",
		},
		{
			Errno: ErrnoEventUnavailable, HTTPStatus: http.StatusConflict, Kind: KindConflict,
			Description:         "The event content is no longer available, so it cannot be redelivered.",
			OfficialDescription: "事件內容已無法取得，不能重送",
		},
		{
			Errno: ErrnoCardReconfirmRequired, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "This operation needs the card details to be confirmed again.",
			OfficialDescription: "這次操作需要重新確認卡片資訊",
		},
		{
			Errno: ErrnoPaymentMethodUnsupported, HTTPStatus: http.StatusBadRequest, Kind: KindInvalidRequest,
			Description:         "The current payment method does not support this operation.",
			OfficialDescription: "目前的付款方式不支援這次操作",
		},
	}
	catalog := make(map[string]ErrnoInfo, len(entries))
	for _, entry := range entries {
		catalog[entry.Errno] = entry
	}
	return catalog
}()
