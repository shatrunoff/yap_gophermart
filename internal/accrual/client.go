package accrual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client предоставляет доступ к внешней системе начислений.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient создаёт клиент.
// baseURL вида: http://localhost:8080 (завершающий слэш не обязателен).
func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	if baseURL == "" {
		return nil, errors.New("accrual baseURL is empty")
	}
	// Удаляем завершающий слэш для упрощения конкатенации путей
	baseURL = strings.TrimRight(baseURL, "/")
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: baseURL, httpClient: httpClient}, nil
}

// GetOrder запрашивает информацию по одному заказу.
// Возвращает Result со статус-кодом и распарсенным Body для ответа 200.
// Для 429 учитывает Retry-After, если заголовок присутствует.
func (c *Client) GetOrder(ctx context.Context, number string) (*Result, error) {
	url := fmt.Sprintf("%s/api/orders/%s", c.baseURL, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	res := &Result{StatusCode: resp.StatusCode}

	switch resp.StatusCode {
	case http.StatusOK: // 200
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		var ord AccrualOrder
		if err := json.Unmarshal(body, &ord); err != nil {
			return nil, err
		}
		res.Body = &ord
		return res, nil

	case http.StatusNoContent: // 204 — заказа нет в системе начислений
		return res, nil

	case http.StatusTooManyRequests: // 429
		// Считываем Retry-After (в секундах), если есть
		if v := resp.Header.Get("Retry-After"); v != "" {
			if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
				res.RetryAfter = time.Duration(secs) * time.Second
			}
		}
		return res, nil

	default:
		// 4xx/5xx — просто возвращаем код
		return res, nil
	}
}
