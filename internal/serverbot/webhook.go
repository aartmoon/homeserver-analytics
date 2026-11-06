package serverbot

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var safeLabel = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,80}$`)

func valid(value string) bool { return safeLabel.MatchString(value) }
func stamp(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if r > 127 || r < 32 {
			return false
		}
	}
	return true
}

// PythonID preserves Python json.dumps(list) whitespace, keeping existing dedup keys.
func PythonID(parts []string) string {
	values := make([]string, len(parts))
	for i, s := range parts {
		b, _ := json.Marshal(s)
		values[i] = string(b)
	}
	hash := sha256.Sum256([]byte("[" + strings.Join(values, ", ") + "]"))
	return hex.EncodeToString(hash[:])
}

type grafanaPayload struct {
	Alerts []struct {
		Labels      map[string]string `json:"labels"`
		Status      string            `json:"status"`
		Fingerprint string            `json:"fingerprint"`
		StartsAt    string            `json:"startsAt"`
		EndsAt      string            `json:"endsAt"`
	} `json:"alerts"`
}

func Normalize(source string, body []byte) ([]Event, error) {
	events := []Event{}
	bad := errors.New("invalid monitoring event")
	add := func(id []string, project, name, status, service, instance, severity string) {
		header := "🔴 Проблема"
		if status == "resolved" {
			header = "🟢 Восстановление"
		}
		events = append(events, Event{PythonID(id), fmt.Sprintf("%s\nПроект: %s\nСервис: %s\nЭкземпляр: %s\nПроверка: %s\nУровень: %s", header, project, service, instance, name, severity)})
	}
	if source == "grafana" {
		var p grafanaPayload
		if err := json.Unmarshal(body, &p); err != nil || len(p.Alerts) < 1 || len(p.Alerts) > 100 {
			return nil, bad
		}
		for _, a := range p.Alerts {
			name, project := a.Labels["alertname"], a.Labels["project"]
			if project == "" && (name == "DatasourceError" || name == "DatasourceNoData") {
				project = "infrastructure"
			}
			service, instance, severity := a.Labels["service"], a.Labels["instance"], a.Labels["severity"]
			if service == "" {
				service = "monitoring"
			}
			if instance == "" {
				instance = "homeserver"
			}
			if severity == "" {
				severity = "warning"
			}
			for _, value := range []string{name, project, service, instance, severity, a.Fingerprint} {
				if !valid(value) {
					return nil, bad
				}
			}
			if a.Status != "firing" && a.Status != "resolved" {
				return nil, bad
			}
			if !stamp(a.StartsAt) {
				return nil, bad
			}
			end := ""
			if a.Status == "resolved" {
				end = a.EndsAt
				if end != "" && !stamp(end) {
					return nil, bad
				}
			}
			add([]string{"grafana", a.Fingerprint, a.Status, a.StartsAt, end}, project, name, a.Status, service, instance, severity)
		}
	} else if source == "kuma" {
		var p struct {
			Project   string `json:"project"`
			Service   string `json:"service"`
			Heartbeat struct {
				Status *int   `json:"status"`
				Time   string `json:"time"`
			} `json:"heartbeat"`
		}
		if err := json.Unmarshal(body, &p); err != nil || !valid(p.Project) || !valid(p.Service) || !stamp(p.Heartbeat.Time) || p.Heartbeat.Status == nil {
			return nil, bad
		}
		status := "firing"
		if *p.Heartbeat.Status == 1 {
			status = "resolved"
		} else if *p.Heartbeat.Status != 0 {
			return nil, bad
		}
		add([]string{"kuma", p.Project, p.Service, status, p.Heartbeat.Time}, p.Project, p.Service, status, p.Service, p.Service, "warning")
	} else {
		return nil, bad
	}
	return events, nil
}
func Webhook(q *Queue, grafanaSecret, kumaSecret string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if q.db.PingContext(ctx) != nil {
			http.Error(w, "Inbox unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	for source, secret := range map[string]string{"grafana": grafanaSecret, "kuma": kumaSecret} {
		mux.HandleFunc("/"+source, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+secret)) != 1 {
				http.Error(w, "Unauthorized", 401)
				return
			}
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/json" {
				http.Error(w, "JSON required", 415)
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
			if err != nil {
				var limit *http.MaxBytesError
				if errors.As(err, &limit) {
					http.Error(w, "Request too large", 413)
				} else {
					http.Error(w, "Invalid request", 400)
				}
				return
			}
			events, err := Normalize(source, body)
			if err != nil {
				http.Error(w, "Invalid monitoring event", 400)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if err = q.Accept(ctx, events, time.Now()); err != nil {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "Inbox unavailable", 503)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"accepted":true}`))
		})
	}
	return mux
}
