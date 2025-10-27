package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"io"

	"github.com/shatrunoff/yap_gofermart/internal/auth"
	"github.com/shatrunoff/yap_gofermart/internal/storage"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type Server struct {
	log    *zap.Logger
	db     *storage.DB
	signer *auth.Signer
}

func New(log *zap.Logger, db *storage.DB, signer *auth.Signer) *Server {
	return &Server{log: log, db: db, signer: signer}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/register", s.handleRegister)
	mux.HandleFunc("/api/user/login", s.handleLogin)
	mux.HandleFunc("/api/user/orders", s.authn(s.handleOrders))
	mux.HandleFunc("/api/user/balance", s.authn(s.handleBalance))
	mux.HandleFunc("/api/user/balance/withdraw", s.authn(s.handleWithdraw))
	mux.HandleFunc("/api/user/withdrawals", s.authn(s.handleWithdrawals))
	return WithGzip(mux)
}

func (s *Server) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.routes()}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		// graceful shutdown
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(cctx)
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) setAuth(w http.ResponseWriter, userID int64) {
	tok, _ := s.signer.Encode(auth.Token{UserID: userID, Exp: time.Now().Add(24 * time.Hour)})
	c := &http.Cookie{Name: "Authorization", Value: tok, Path: "/", HttpOnly: true}
	http.SetCookie(w, c)
}

func (s *Server) currentUserID(r *http.Request) (int64, bool) {
	// куки
	if c, err := r.Cookie("Authorization"); err == nil {
		if t, err := s.signer.Decode(c.Value); err == nil {
			return t.UserID, true
		}
	}
	// header авторизации: Bearer <token>
	if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		if t, err := s.signer.Decode(h[7:]); err == nil {
			return t.UserID, true
		}
	}
	return 0, false
}

func (s *Server) authn(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentUserID(r); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	}
}

type creds struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var c creds
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || c.Login == "" || c.Password == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	id, err := s.db.CreateUser(r.Context(), c.Login, string(hash))
	if err != nil {
		if err == storage.ErrLoginExists {
			http.Error(w, "conflict", http.StatusConflict)
			return
		}
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	s.setAuth(w, id)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var c creds
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || c.Login == "" || c.Password == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	u, err := s.db.GetUserByLogin(r.Context(), c.Login)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(c.Password)) != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.setAuth(w, u.ID)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	uid, _ := s.currentUserID(r)
	switch r.Method {
	case http.MethodPost:
		if ct := r.Header.Get("Content-Type"); ct == "" || !strings.HasPrefix(ct, "text/plain") {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		number := string(body)
		if !isLuhnValid(number) {
			http.Error(w, "invalid number", http.StatusUnprocessableEntity)
			return
		}
		err = s.db.InsertOrder(r.Context(), uid, number)
		if err == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		switch err {
		case storage.ErrOrderExistsSameUser:
			w.WriteHeader(http.StatusOK)
		case storage.ErrOrderExistsAnotherUser:
			http.Error(w, "conflict", http.StatusConflict)
		default:
			http.Error(w, "server error", http.StatusInternalServerError)
		}
	case http.MethodGet:
		items, err := s.db.ListOrdersByUser(r.Context(), uid)
		if err != nil {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		if len(items) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Приводим к нужному JSON
		type respItem struct {
			Number     string   `json:"number"`
			Status     string   `json:"status"`
			Accrual    *float64 `json:"accrual,omitempty"`
			UploadedAt string   `json:"uploaded_at"`
		}
		out := make([]respItem, 0, len(items))
		for _, it := range items {
			out = append(out, respItem{Number: it.Number, Status: it.Status, Accrual: it.Accrual, UploadedAt: it.UploadedAt.Format(time.RFC3339)})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (s *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	uid, _ := s.currentUserID(r)
	bal, err := s.db.GetBalance(r.Context(), uid)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Current   float64 `json:"current"`
		Withdrawn float64 `json:"withdrawn"`
	}{Current: bal.Current, Withdrawn: bal.Withdrawn})
}

func (s *Server) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	uid, _ := s.currentUserID(r)
	var req struct {
		Order string  `json:"order"`
		Sum   float64 `json:"sum"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !isLuhnValid(req.Order) {
		http.Error(w, "invalid number", http.StatusUnprocessableEntity)
		return
	}
	if req.Sum <= 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	err := s.db.CreateWithdrawal(r.Context(), uid, req.Order, req.Sum)
	if err != nil {
		if err == storage.ErrInsufficientFunds {
			http.Error(w, "payment required", http.StatusPaymentRequired)
			return
		}
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleWithdrawals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	uid, _ := s.currentUserID(r)
	items, err := s.db.ListWithdrawals(r.Context(), uid)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if len(items) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	type resp struct {
		Order       string  `json:"order"`
		Sum         float64 `json:"sum"`
		ProcessedAt string  `json:"processed_at"`
	}
	out := make([]resp, 0, len(items))
	for _, it := range items {
		out = append(out, resp{Order: it.Order, Sum: it.Amount, ProcessedAt: it.ProcessedAt.Format(time.RFC3339)})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
