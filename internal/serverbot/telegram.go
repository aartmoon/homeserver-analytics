package serverbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

type APIError struct{ Code, RetryAfter int }

func (e *APIError) Error() string { return "Telegram API rejected request" }

type Telegram struct {
	Client  *http.Client
	BaseURL string
}

func NewTelegram(token, proxy string) (*Telegram, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || u.Host == "" {
			return nil, errors.New("invalid TG_PROXY")
		}
		if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h" {
			return nil, errors.New("unsupported TG_PROXY scheme")
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &Telegram{Client: &http.Client{Transport: transport, Timeout: 180 * time.Second}, BaseURL: "https://api.telegram.org/bot" + token}, nil
}
func (t *Telegram) call(ctx context.Context, method string, payload any, result any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return errors.New("cannot encode Telegram request")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", t.BaseURL+"/"+method, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid Telegram request")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := t.Client.Do(req)
	if err != nil {
		return errors.New("Telegram transport unavailable")
	}
	defer response.Body.Close()
	var envelope struct {
		OK         bool            `json:"ok"`
		Code       int             `json:"error_code"`
		Result     json.RawMessage `json:"result"`
		Parameters struct {
			Retry int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&envelope); err != nil {
		return errors.New("invalid Telegram response")
	}
	if response.StatusCode != 200 || !envelope.OK {
		return &APIError{Code: envelope.Code, RetryAfter: envelope.Parameters.Retry}
	}
	if result != nil && json.Unmarshal(envelope.Result, result) != nil {
		return errors.New("invalid Telegram result")
	}
	return nil
}
func (t *Telegram) SendMessage(ctx context.Context, owner int64, text string, keyboard bool) error {
	payload := map[string]any{"chat_id": owner, "text": text}
	if keyboard {
		payload["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "📊 Состояние сервера", "callback_data": "status"}}}}
	}
	return t.call(ctx, "sendMessage", payload, nil)
}
func (t *Telegram) Answer(ctx context.Context, id, text string, alert bool) error {
	return t.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text, "show_alert": alert}, nil)
}
func (t *Telegram) Commands(ctx context.Context) error {
	return t.call(ctx, "setMyCommands", map[string]any{"commands": []map[string]string{{"command": "status", "description": "Состояние сервера"}}}, nil)
}

type User struct {
	ID int64 `json:"id"`
}
type Message struct {
	From *User `json:"from"`
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
}
type Callback struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
}
type Update struct {
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}

func (t *Telegram) Handle(ctx context.Context, u Update, owner int64, status func() string) error {
	if m := u.Message; m != nil && m.From != nil && m.From.ID == owner && m.Chat.Type == "private" {
		return t.SendMessage(ctx, owner, status(), true)
	}
	if cb := u.Callback; cb != nil {
		if cb.From.ID != owner || cb.Message == nil || cb.Message.Chat.Type != "private" {
			return t.Answer(ctx, cb.ID, "Нет доступа", true)
		}
		if err := t.Answer(ctx, cb.ID, "", false); err != nil {
			return err
		}
		return t.SendMessage(ctx, owner, status(), true)
	}
	return nil
}
func wait(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
func (t *Telegram) Poll(ctx context.Context, q *Queue, owner int64) {
	offset := q.Offset()
	backoff := time.Second
	for ctx.Err() == nil {
		var updates []Update
		requestCtx, cancel := context.WithTimeout(ctx, 65*time.Second)
		err := t.call(requestCtx, "getUpdates", map[string]any{"offset": offset, "timeout": 50, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		cancel()
		if err != nil {
			delay := backoff
			var api *APIError
			if errors.As(err, &api) && time.Duration(api.RetryAfter)*time.Second > delay {
				delay = time.Duration(api.RetryAfter) * time.Second
			}
			if !wait(ctx, delay) {
				return
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			actionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = t.Handle(actionCtx, u, owner, Status)
			cancel()
			if err != nil {
				wait(ctx, backoff)
				break
			}
			if u.ID >= offset {
				offset = u.ID + 1
			}
			if q.SetOffset(ctx, offset) != nil {
				wait(ctx, time.Second)
				break
			}
		}
	}
}
func (t *Telegram) Close() {
	if transport, ok := t.Client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

// No SDK or HTTP error is logged: URLs include the bot token.
