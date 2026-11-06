package serverbot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPayload = `{"alerts":[{"labels":{"project":"cottontoys","service":"homepage","instance":"homepage","severity":"critical","alertname":"HttpUnavailable"},"fingerprint":"abc123","status":"firing","startsAt":"2026-11-06T12:00:00Z"}]}`

func queue(t *testing.T, capacity int) *Queue {
	t.Helper()
	q, err := OpenQueue(filepath.Join(t.TempDir(), "inbox.sqlite3"), capacity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	return q
}
func TestLegacyPythonIdentity(t *testing.T) {
	id := PythonID([]string{"grafana", "abc123", "firing", "2026-11-06T12:00:00Z", ""})
	// Fixture produced by hashlib.sha256(json.dumps(parts).encode()).
	const expected = "5c8e98399514217332e88c77cce277e540b8c55d7348f9f345a8de521aabf26f"
	if id != expected {
		t.Fatalf("Python-compatible identity mismatch: %s", id)
	}
	events, err := Normalize("grafana", []byte(testPayload))
	if err != nil {
		t.Fatal(err)
	}
	if events[0].ID != id {
		t.Fatal("normalized identity differs")
	}
	if !strings.Contains(events[0].Text, "Проект: cottontoys") || !strings.Contains(events[0].Text, "Сервис: homepage") {
		t.Fatal("affected project/service missing")
	}
}
func TestLegacySchemaDedupAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.sqlite3")
	q, err := OpenQueue(path, 5)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := Normalize("grafana", []byte(testPayload))
	now := time.Now()
	// This is the same row/schema used by the Python implementation.
	if _, err = q.db.Exec("INSERT INTO events(id,text,delivered,created) VALUES (?,?,1,?)", events[0].ID, "legacy delivery", float64(now.Unix())); err != nil {
		t.Fatal(err)
	}
	if err = q.Accept(context.Background(), events, now); err != nil {
		t.Fatal(err)
	}
	var n int
	q.db.QueryRow("SELECT count(*) FROM events").Scan(&n)
	if n != 1 {
		t.Fatal("legacy delivery was duplicated")
	}
	if err = q.SetOffset(context.Background(), 123); err != nil {
		t.Fatal(err)
	}
	q.Close()
	q, err = OpenQueue(path, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if q.Offset() != 123 {
		t.Fatal("poll offset did not persist")
	}
	mode, _ := os.Stat(path)
	if mode.Mode().Perm() != 0600 {
		t.Fatal("inbox permissions")
	}
}

type fakeSender struct {
	calls int
	fail  bool
	block bool
}

func (f *fakeSender) SendMessage(ctx context.Context, _ int64, _ string, _ bool) error {
	f.calls++
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if f.fail {
		return errors.New("offline")
	}
	return nil
}
func TestDurableBoundedRetry(t *testing.T) {
	q := queue(t, 1)
	ctx := context.Background()
	event := Event{"one", "message"}
	if err := q.Accept(ctx, []Event{event, event}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := q.Accept(ctx, []Event{{"two", "different"}}, time.Now()); !errors.Is(err, ErrFull) {
		t.Fatal("queue not bounded")
	}
	sender := &fakeSender{fail: true}
	if _, err := q.DeliverOnce(ctx, sender, 42); err != nil {
		t.Fatal(err)
	}
	var delivered, attempts int
	var retry float64
	q.db.QueryRow("SELECT delivered,attempts,retry FROM events").Scan(&delivered, &attempts, &retry)
	if delivered != 0 || attempts != 1 || retry < float64(time.Now().Unix()) {
		t.Fatal("failed delivery was lost or not delayed")
	}
	q.db.Exec("UPDATE events SET retry=0")
	sender.fail = false
	if _, err := q.DeliverOnce(ctx, sender, 42); err != nil {
		t.Fatal(err)
	}
	q.DeliverOnce(ctx, sender, 42)
	if sender.calls != 2 {
		t.Fatal("delivered event resent")
	}
}
func TestCancelledDeliveryRemainsPending(t *testing.T) {
	q := queue(t, 5)
	q.Accept(context.Background(), []Event{{"one", "message"}}, time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	q.DeliverOnce(ctx, &fakeSender{block: true}, 42)
	var delivered int
	q.db.QueryRow("SELECT delivered FROM events").Scan(&delivered)
	if delivered != 0 {
		t.Fatal("cancelled send acknowledged")
	}
}
func TestWebhookValidation(t *testing.T) {
	q := queue(t, 1)
	handler := Webhook(q, strings.Repeat("g", 32), strings.Repeat("k", 32))
	tests := []struct {
		body, auth, media string
		want              int
	}{{testPayload, "", "application/json", 401}, {testPayload, "Bearer " + strings.Repeat("g", 32), "text/plain", 415}, {"{", "Bearer " + strings.Repeat("g", 32), "application/json", 400}, {strings.Repeat("x", 65537), "Bearer " + strings.Repeat("g", 32), "application/json", 413}, {testPayload, "Bearer " + strings.Repeat("g", 32), "application/json", 202}, {testPayload, "Bearer " + strings.Repeat("g", 32), "application/json", 202}}
	for _, test := range tests {
		req := httptest.NewRequest("POST", "/grafana", strings.NewReader(test.body))
		req.Header.Set("Authorization", test.auth)
		req.Header.Set("Content-Type", test.media)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != test.want {
			t.Fatalf("HTTP got %d want %d", rr.Code, test.want)
		}
		if rr.Code == 202 && !json.Valid(rr.Body.Bytes()) {
			t.Fatal("invalid acknowledgement JSON")
		}
	}
	req := httptest.NewRequest("POST", "/grafana", strings.NewReader(strings.ReplaceAll(testPayload, "abc123", "different")))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("g", 32))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 503 || rr.Header().Get("Retry-After") != "60" {
		t.Fatal("full queue was acknowledged")
	}
}
func TestRecoveryKumaAndDatasource(t *testing.T) {
	body := strings.ReplaceAll(testPayload, `"status":"firing"`, `"status":"resolved"`)
	event, err := Normalize("grafana", []byte(body))
	if err != nil || !strings.Contains(event[0].Text, "Восстановление") {
		t.Fatal("recovery not formatted")
	}
	body = `{"project":"talk","service":"ready","heartbeat":{"status":0,"time":"2026-11-06 12:00:00"}}`
	if _, err = Normalize("kuma", []byte(body)); err != nil {
		t.Fatal(err)
	}
	if _, err = Normalize("kuma", []byte(strings.ReplaceAll(body, `"status":0`, `"status":2`))); err == nil {
		t.Fatal("intermediate status accepted")
	}
	body = strings.ReplaceAll(testPayload, `"project":"cottontoys",`, "")
	if _, err = Normalize("grafana", []byte(body)); err == nil {
		t.Fatal("missing project accepted")
	}
	body = strings.ReplaceAll(body, "HttpUnavailable", "DatasourceError")
	event, err = Normalize("grafana", []byte(body))
	if err != nil || !strings.Contains(event[0].Text, "infrastructure") {
		t.Fatal("datasource fallback")
	}
}
func TestOwnerPrivateHandlers(t *testing.T) {
	var mu sync.Mutex
	methods := []string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		methods = append(methods, r.URL.Path)
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if r.URL.Path == "/sendMessage" && payload["chat_id"] != float64(42) {
			t.Error("metrics recipient changed")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer ts.Close()
	tg := &Telegram{Client: ts.Client(), BaseURL: ts.URL}
	ctx := context.Background()
	message := func(id int64, kind string) *Message { m := &Message{From: &User{ID: id}}; m.Chat.Type = kind; return m }
	for _, m := range []*Message{message(43, "private"), message(42, "group")} {
		if err := tg.Handle(ctx, Update{Message: m}, 42, func() string { return "METRICS" }); err != nil {
			t.Fatal(err)
		}
	}
	if len(methods) != 0 {
		t.Fatal("unauthorized metrics response")
	}
	tg.Handle(ctx, Update{Message: message(42, "private")}, 42, func() string { return "METRICS" })
	tg.Handle(ctx, Update{Callback: &Callback{ID: "foreign", From: User{43}, Message: message(43, "private")}}, 42, func() string { return "METRICS" })
	tg.Handle(ctx, Update{Callback: &Callback{ID: "owner", From: User{42}, Message: message(42, "private")}}, 42, func() string { return "METRICS" })
	if strings.Join(methods, ",") != "/sendMessage,/answerCallbackQuery,/answerCallbackQuery,/sendMessage" {
		t.Fatal("handler behavior changed")
	}
}
func TestLockAndStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.lock")
	lock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if next, err := Lock(path); err == nil {
		next.Close()
		t.Fatal("second poller lock succeeded")
	}
	status := Status()
	if !strings.Contains(status, "Домашний сервер работает") || strings.Contains(status, "NaN") {
		t.Fatal("invalid server status")
	}
}
func TestHTTPErrorDoesNotExposeToken(t *testing.T) {
	tg, err := NewTelegram("sensitive-test-token", "socks5://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err = tg.SendMessage(ctx, 42, "test", false)
	if err == nil || strings.Contains(err.Error(), "sensitive-test-token") {
		t.Fatal("unsafe transport error")
	}
}

func TestSinglePollerPersistsAcknowledgedOffset(t *testing.T) {
	q := queue(t, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var mu sync.Mutex
	offsets := []int64{}
	sent := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/sendMessage" {
			sent++
			io.WriteString(w, `{"ok":true,"result":true}`)
			return
		}
		if r.URL.Path != "/getUpdates" {
			t.Error("unexpected Telegram method")
			return
		}
		offsets = append(offsets, int64(body["offset"].(float64)))
		switch len(offsets) {
		case 1:
			io.WriteString(w, `{"ok":true,"result":[{"update_id":100,"message":{"from":{"id":43},"chat":{"id":43,"type":"private"}}}]}`)
		case 2:
			io.WriteString(w, `{"ok":true,"result":[{"update_id":101,"message":{"from":{"id":42},"chat":{"id":42,"type":"private"}}}]}`)
		default:
			cancel()
			io.WriteString(w, `{"ok":true,"result":[]}`)
		}
	}))
	defer ts.Close()
	tg := &Telegram{Client: ts.Client(), BaseURL: ts.URL}
	tg.Poll(ctx, q, 42)
	if q.Offset() != 102 {
		t.Fatal("acknowledged update offset not persisted")
	}
	mu.Lock()
	defer mu.Unlock()
	if sent != 1 || len(offsets) != 3 || offsets[1] != 101 || offsets[2] != 102 {
		t.Fatal("polling authorization/offset behavior incorrect")
	}
}
func TestConfigurationPrecedenceAndPrivateBinding(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".env"), []byte("TG_ALLOWED_ID=99\n"), 0600)
	t.Setenv("TG_BOT_TOKEN", "test-token")
	t.Setenv("TG_ALLOWED_ID", "42")
	t.Setenv("TG_PROXY", "")
	t.Setenv("MONITORING_WEBHOOKS_ENABLED", "0")
	t.Setenv("MONITORING_WEBHOOK_HOST", "127.0.0.1")
	t.Setenv("MONITORING_WEBHOOK_PORT", "9088")
	cfg, err := LoadConfig(root)
	if err != nil || cfg.Owner != 42 || cfg.Proxy != "" {
		t.Fatal("existing environment precedence changed")
	}
	t.Setenv("MONITORING_WEBHOOKS_ENABLED", "1")
	t.Setenv("MONITORING_WEBHOOK_HOST", "0.0.0.0")
	if _, err = LoadConfig(root); err == nil {
		t.Fatal("public wildcard webhook binding accepted")
	}
}
