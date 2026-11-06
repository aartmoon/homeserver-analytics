package serverbot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"net/url"
	"os"
	"sync"
	"time"
)

var ErrFull = errors.New("inbox capacity exceeded")

type Event struct{ ID, Text string }
type Queue struct {
	db       *sql.DB
	mu       sync.Mutex
	Capacity int
}

func OpenQueue(path string, capacity int) (*Queue, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite3", u.String()+"?_journal_mode=WAL&_synchronous=FULL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{`CREATE TABLE IF NOT EXISTS events (id TEXT PRIMARY KEY,text TEXT NOT NULL,delivered INTEGER DEFAULT 0,created REAL NOT NULL,attempts INTEGER DEFAULT 0,retry REAL DEFAULT 0)`, `CREATE TABLE IF NOT EXISTS bot_state (key TEXT PRIMARY KEY,value TEXT NOT NULL)`} {
		if _, err = db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Queue{db: db, Capacity: capacity}, nil
}
func (q *Queue) Close() error { return q.db.Close() }
func (q *Queue) Accept(ctx context.Context, events []Event, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM events WHERE delivered=1 AND created < ?", float64(now.Unix())-7*86400); err != nil {
		return err
	}
	fresh := map[string]string{}
	for _, e := range events {
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE id=?", e.ID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			fresh[e.ID] = e.Text
		}
	}
	var pending, total int
	if err = tx.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(CASE WHEN delivered=0 THEN 1 ELSE 0 END),0) FROM events").Scan(&total, &pending); err != nil {
		return err
	}
	if pending+len(fresh) > q.Capacity || total+len(fresh) > 10000 {
		return ErrFull
	}
	for id, text := range fresh {
		if _, err = tx.ExecContext(ctx, "INSERT INTO events(id,text,created) VALUES (?,?,?)", id, text, float64(now.UnixNano())/1e9); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (q *Queue) Offset() int64 {
	var offset int64
	_ = q.db.QueryRow("SELECT value FROM bot_state WHERE key='update_offset'").Scan(&offset)
	return offset
}
func (q *Queue) SetOffset(ctx context.Context, offset int64) error {
	_, err := q.db.ExecContext(ctx, "INSERT INTO bot_state(key,value) VALUES ('update_offset',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", fmt.Sprint(offset))
	return err
}

type Sender interface {
	SendMessage(context.Context, int64, string, bool) error
}

func (q *Queue) DeliverOnce(ctx context.Context, sender Sender, owner int64) (bool, error) {
	var id, text string
	var attempts int
	err := q.db.QueryRowContext(ctx, "SELECT id,text,attempts FROM events WHERE delivered=0 AND retry<=? ORDER BY created LIMIT 1", float64(time.Now().UnixNano())/1e9).Scan(&id, &text, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = sender.SendMessage(sendCtx, owner, text, false)
	cancel()
	if err != nil {
		// Cancellation leaves the original pending row untouched for a clean restart.
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		delay := time.Duration(1<<min(attempts+1, 12)) * time.Second
		if delay > time.Hour {
			delay = time.Hour
		}
		var retry *APIError
		if errors.As(err, &retry) && time.Duration(retry.RetryAfter)*time.Second > delay {
			delay = time.Duration(retry.RetryAfter) * time.Second
		}
		_, err = q.db.ExecContext(ctx, "UPDATE events SET attempts=attempts+1,retry=? WHERE id=?", float64(time.Now().Add(delay).UnixNano())/1e9, id)
	} else {
		_, err = q.db.ExecContext(ctx, "UPDATE events SET delivered=1 WHERE id=?", id)
	}
	return true, err
}
func (q *Queue) Worker(ctx context.Context, sender Sender, owner int64) {
	for ctx.Err() == nil {
		done, err := q.DeliverOnce(ctx, sender, owner)
		wait := time.Second
		if err != nil {
			wait = 5 * time.Second
		} else if !done {
			wait = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
