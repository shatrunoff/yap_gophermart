package accrual

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// OrdersRepo описывает операции хранилища, необходимые поллеру.
// Реализация должна обеспечивать идемпотентность и атомарность где применимо.
//
// Требуемая семантика:
//   - GetOrdersNeedingAccrual должен возвращать заказы в статусах NEW или PROCESSING,
//     возможно с фильтром по политике повторных попыток (например, next_poll_at <= now).
//   - SetStatus должен обновлять статус заказа и, если status == PROCESSED и accrual != nil,
//     начислять баллы на счёт пользователя в рамках одной транзакции.
type OrdersRepo interface {
	GetOrdersNeedingAccrual(ctx context.Context, limit int) ([]Order, error)
	SetStatus(ctx context.Context, number string, status OrderStatus, accrual *float64) error
}

// Options управляет поведением поллера.
type Options struct {
	// BatchSize — лимит заказов за одну итерацию. По умолчанию 100.
	BatchSize int
	// Interval — пауза между итерациями опроса при отсутствии глобального бэкоффа. По умолчанию 2s.
	Interval time.Duration
	// DefaultRetryAfter — используется, если при 429 отсутствует Retry-After. По умолчанию 60s.
	DefaultRetryAfter time.Duration
}

// Poller периодически опрашивает систему начислений по заказам
// и обновляет статусы/балансы через OrdersRepo.
type Poller struct {
	client  *Client
	repo    OrdersRepo
	log     *zap.Logger
	options Options
}

// NewPoller создаёт Poller.
func NewPoller(client *Client, repo OrdersRepo, logger *zap.Logger, opts Options) *Poller {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.Interval <= 0 {
		opts.Interval = 2 * time.Second
	}
	if opts.DefaultRetryAfter <= 0 {
		opts.DefaultRetryAfter = 60 * time.Second
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Poller{client: client, repo: repo, log: logger, options: opts}
}

// Start запускает цикл опроса до отмены контекста.
func (p *Poller) Start(ctx context.Context) error {
	ticker := time.NewTicker(p.options.Interval)
	defer ticker.Stop()

	for {
		if err := p.iteration(ctx); err != nil {
			// Не критично; логируем и продолжаем, если контекст не отменён
			if ctx.Err() != nil {
				return ctx.Err()
			}
			p.log.Warn("iteration error", zap.Error(err))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// iteration выполняет один проход по ожидающим заказам и обновляет их состояние.
func (p *Poller) iteration(ctx context.Context) error {
	orders, err := p.repo.GetOrdersNeedingAccrual(ctx, p.options.BatchSize)
	if err != nil {
		return err
	}
	if len(orders) == 0 {
		return nil
	}

	for _, o := range orders {
		// Проверяем отмену контекста между запросами
		if ctx.Err() != nil {
			return ctx.Err()
		}

		res, err := p.client.GetOrder(ctx, o.Number)
		if err != nil {
			p.log.Warn("request error", zap.String("order", o.Number), zap.Error(err))
			continue
		}

		switch res.StatusCode {
		case 200:
			if res.Body == nil {
				p.log.Warn("empty body on 200", zap.String("order", o.Number))
				continue
			}
			switch res.Body.Status {
			case StatusProcessed:
				// Финальный: установить PROCESSED и начислить
				p.applyStatus(ctx, o.Number, OrderProcessed, res.Body.Accrual)
			case StatusInvalid:
				// Финальный: установить INVALID
				p.applyStatus(ctx, o.Number, OrderInvalid, nil)
			case StatusRegistered, StatusProcessing:
				// Не финальный: зафиксировать как PROCESSING
				p.applyStatus(ctx, o.Number, OrderProcessing, nil)
			default:
				p.log.Warn("unknown accrual status", zap.String("order", o.Number), zap.String("status", string(res.Body.Status)))
			}

		case 204:
			// Заказ ещё не зарегистрирован — оставляем NEW
			p.applyStatus(ctx, o.Number, OrderNew, nil)

		case 429:
			// Глобальный бэкофф по требованию сервиса
			retry := res.RetryAfter
			if retry <= 0 {
				retry = p.options.DefaultRetryAfter
			}
			p.log.Info("rate limited by accrual service", zap.Duration("retry_after", retry))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retry):
			}

		default:
			// 4xx/5xx — временные или клиентские ошибки; пропускаем
			p.log.Info("accrual responded with non-200/204/429", zap.String("order", o.Number), zap.Int("status_code", res.StatusCode))
		}
	}

	return nil
}

func (p *Poller) applyStatus(ctx context.Context, number string, status OrderStatus, accrual *float64) {
	if err := p.repo.SetStatus(ctx, number, status, accrual); err != nil {
		p.log.Warn("failed to set status", zap.String("order", number), zap.String("status", string(status)), zap.Error(err))
	}
}
