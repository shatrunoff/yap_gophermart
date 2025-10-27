package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "github.com/lib/pq"
)

var (
	ErrLoginExists            = errors.New("login already exists")
	ErrInvalidCredentials     = errors.New("invalid credentials")
	ErrOrderExistsSameUser    = errors.New("order already uploaded by this user")
	ErrOrderExistsAnotherUser = errors.New("order already uploaded by another user")
	ErrInsufficientFunds      = errors.New("insufficient funds")
)

type DB struct {
	SQL *sql.DB
}

func New(ctx context.Context, dsn string) (*DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	d := &DB{SQL: db}
	if err := d.migrate(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

// миграция БД
func (d *DB) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
            id SERIAL PRIMARY KEY,
            login TEXT UNIQUE NOT NULL,
            password_hash TEXT NOT NULL,
            created_at TIMESTAMP NOT NULL DEFAULT NOW()
        );`,
		`CREATE TABLE IF NOT EXISTS orders (
            id SERIAL PRIMARY KEY,
            user_id INTEGER NOT NULL REFERENCES users(id),
            number TEXT NOT NULL UNIQUE,
            status TEXT NOT NULL,
            accrual DOUBLE PRECISION,
            uploaded_at TIMESTAMP NOT NULL DEFAULT NOW()
        );`,
		`CREATE INDEX IF NOT EXISTS idx_orders_user_uploaded ON orders(user_id, uploaded_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);`,
		`CREATE TABLE IF NOT EXISTS withdrawals (
            id SERIAL PRIMARY KEY,
            user_id INTEGER NOT NULL REFERENCES users(id),
            order_number TEXT NOT NULL,
            amount DOUBLE PRECISION NOT NULL,
            processed_at TIMESTAMP NOT NULL DEFAULT NOW()
        );`,
		`CREATE INDEX IF NOT EXISTS idx_withdrawals_user_processed ON withdrawals(user_id, processed_at DESC);`,
	}
	for _, s := range stmts {
		if _, err := d.SQL.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// User
type User struct {
	ID           int64
	Login        string
	PasswordHash string
}

// создание юзера
func (d *DB) CreateUser(ctx context.Context, login, passwordHash string) (int64, error) {
	var id int64
	err := d.SQL.QueryRowContext(ctx, `INSERT INTO users(login, password_hash) VALUES($1,$2) RETURNING id`, login, passwordHash).Scan(&id)
	if err != nil {
		if stringsContainInsensitive(err.Error(), "duplicate") || stringsContainInsensitive(err.Error(), "unique") {
			return 0, ErrLoginExists
		}
		return 0, err
	}
	return id, nil
}

func stringsContainInsensitive(s, sub string) bool {
	S := s
	if len(S) < 1 || len(sub) < 1 {
		return false
	}
	// примитивная проверка без зависимостей
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	data := make([]byte, len(S))
	for i := range S {
		data[i] = lower(S[i])
	}
	needle := make([]byte, len(sub))
	for i := range sub {
		needle[i] = lower(sub[i])
	}
	return containsBytes(data, needle)
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func (d *DB) GetUserByLogin(ctx context.Context, login string) (*User, error) {
	row := d.SQL.QueryRowContext(ctx, `SELECT id, login, password_hash FROM users WHERE login=$1`, login)
	var u User
	if err := row.Scan(&u.ID, &u.Login, &u.PasswordHash); err != nil {
		return nil, err
	}
	return &u, nil
}

// Orders
type Order struct {
	Number     string
	Status     string
	Accrual    *float64
	UploadedAt time.Time
}

func (d *DB) InsertOrder(ctx context.Context, userID int64, number string) error {
	_, err := d.SQL.ExecContext(ctx, `INSERT INTO orders(user_id, number, status) VALUES($1,$2,'NEW')`, userID, number)
	if err != nil {
		// выясним, кому принадлежит заказ
		var ownerID int64
		qerr := d.SQL.QueryRowContext(ctx, `SELECT user_id FROM orders WHERE number=$1`, number).Scan(&ownerID)
		if qerr == nil {
			if ownerID == userID {
				return ErrOrderExistsSameUser
			}
			return ErrOrderExistsAnotherUser
		}
		return err
	}
	return nil
}

func (d *DB) ListOrdersByUser(ctx context.Context, userID int64) ([]Order, error) {
	rows, err := d.SQL.QueryContext(ctx, `SELECT number, status, accrual, uploaded_at FROM orders WHERE user_id=$1 ORDER BY uploaded_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.Number, &o.Status, &o.Accrual, &o.UploadedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// нужен расчет
func (d *DB) GetOrdersNeedingAccrual(ctx context.Context, limit int) ([]Order, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := d.SQL.QueryContext(ctx, `SELECT number, status, accrual, uploaded_at FROM orders WHERE status IN ('NEW','PROCESSING') ORDER BY uploaded_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.Number, &o.Status, &o.Accrual, &o.UploadedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (d *DB) SetOrderStatus(ctx context.Context, number string, status string, accrual *float64) error {
	if status == "PROCESSED" && accrual != nil {
		_, err := d.SQL.ExecContext(ctx, `UPDATE orders SET status=$2, accrual=$3 WHERE number=$1`, number, status, accrual)
		return err
	}
	_, err := d.SQL.ExecContext(ctx, `UPDATE orders SET status=$2 WHERE number=$1`, number, status)
	return err
}

// Balance
type Balance struct {
	Current   float64
	Withdrawn float64
}

func (d *DB) GetBalance(ctx context.Context, userID int64) (*Balance, error) {
	var accrued sql.NullFloat64
	var withdrawn sql.NullFloat64
	if err := d.SQL.QueryRowContext(ctx, `SELECT COALESCE(SUM(accrual),0) FROM orders WHERE user_id=$1 AND status='PROCESSED'`, userID).Scan(&accrued); err != nil {
		return nil, err
	}
	if err := d.SQL.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount),0) FROM withdrawals WHERE user_id=$1`, userID).Scan(&withdrawn); err != nil {
		return nil, err
	}
	return &Balance{Current: accrued.Float64 - withdrawn.Float64, Withdrawn: withdrawn.Float64}, nil
}

// Withdrawal
type Withdrawal struct {
	Order       string
	Amount      float64
	ProcessedAt time.Time
}

func (d *DB) CreateWithdrawal(ctx context.Context, userID int64, order string, amount float64) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var accrued sql.NullFloat64
	var withdrawn sql.NullFloat64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(accrual),0) FROM orders WHERE user_id=$1 AND status='PROCESSED'`, userID).Scan(&accrued); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount),0) FROM withdrawals WHERE user_id=$1`, userID).Scan(&withdrawn); err != nil {
		return err
	}
	current := accrued.Float64 - withdrawn.Float64
	if amount > current+1e-9 {
		return ErrInsufficientFunds
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO withdrawals(user_id, order_number, amount) VALUES($1,$2,$3)`, userID, order, amount); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) ListWithdrawals(ctx context.Context, userID int64) ([]Withdrawal, error) {
	rows, err := d.SQL.QueryContext(ctx, `SELECT order_number, amount, processed_at FROM withdrawals WHERE user_id=$1 ORDER BY processed_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Withdrawal
	for rows.Next() {
		var w Withdrawal
		if err := rows.Scan(&w.Order, &w.Amount, &w.ProcessedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
