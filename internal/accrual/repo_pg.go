package accrual

import (
	"context"

	"github.com/shatrunoff/yap_gofermart/internal/storage"
)

// pgRepo адаптирует storage.DB к интерфейсу OrdersRepo.
type pgRepo struct{ db *storage.DB }

func NewPGRepo(db *storage.DB) OrdersRepo { return &pgRepo{db: db} }

func (r *pgRepo) GetOrdersNeedingAccrual(ctx context.Context, limit int) ([]Order, error) {
	rows, err := r.db.GetOrdersNeedingAccrual(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Order, 0, len(rows))
	for _, o := range rows {
		out = append(out, Order{Number: o.Number})
	}
	return out, nil
}

func (r *pgRepo) SetStatus(ctx context.Context, number string, status OrderStatus, accrualAmount *float64) error {
	return r.db.SetOrderStatus(ctx, number, string(status), accrualAmount)
}
