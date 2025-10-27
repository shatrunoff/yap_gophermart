package accrual

import "time"

// AccrualStatus — статус, возвращаемый внешней системой начислений.
// Возможные значения:
//   - REGISTERED — заказ зарегистрирован, начисление ещё не рассчитано
//   - INVALID    — заказ отклонён, начисление не будет выполнено
//   - PROCESSING — начисление рассчитывается
//   - PROCESSED  — начисление завершено.
type AccrualStatus string

const (
	StatusRegistered AccrualStatus = "REGISTERED"
	StatusInvalid    AccrualStatus = "INVALID"
	StatusProcessing AccrualStatus = "PROCESSING"
	StatusProcessed  AccrualStatus = "PROCESSED"
)

// OrderStatus — внутренний статус заказа в Gophermart.
// Возможные значения: NEW, PROCESSING, INVALID, PROCESSED.
type OrderStatus string

const (
	OrderNew        OrderStatus = "NEW"
	OrderProcessing OrderStatus = "PROCESSING"
	OrderInvalid    OrderStatus = "INVALID"
	OrderProcessed  OrderStatus = "PROCESSED"
)

// AccrualOrder — тело ответа системы начислений.
type AccrualOrder struct {
	Order   string        `json:"order"`
	Status  AccrualStatus `json:"status"`
	Accrual *float64      `json:"accrual,omitempty"`
}

// Result — результат запроса к системе начислений.
// При StatusCode == 200 заполнено поле Body.
// При StatusCode == 429 может быть заполнено RetryAfter.
type Result struct {
	StatusCode int
	RetryAfter time.Duration
	Body       *AccrualOrder
}

// Order — минимальная проекция заказа, необходимая поллеру.
type Order struct {
	Number string
}
