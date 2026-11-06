// Package dockerproxy exposes a small, read-only, redacted Docker metrics API over Unix.
package dockerproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var versioned = regexp.MustCompile(`^/v[0-9]+\.[0-9]+/(.*)$`)
var idPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)
var containerPath = regexp.MustCompile(`^containers/([a-f0-9]{12,64})/(json|stats)$`)

type Proxy struct{ Client *http.Client }

func New(socket string) *Proxy {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, MaxConnsPerHost: 8, MaxIdleConnsPerHost: 8}
	return &Proxy{Client: &http.Client{Transport: tr, Timeout: 15 * time.Second}}
}
func allowedProject(labels map[string]string) bool {
	p := labels["com.docker.compose.project"]
	return p == "ruk" || p == "talk" || p == "monitoring"
}
func labelsOnly(labels map[string]string) map[string]string {
	return map[string]string{"com.docker.compose.project": labels["com.docker.compose.project"], "com.docker.compose.service": labels["com.docker.compose.service"]}
}
func (p *Proxy) get(ctx context.Context, path, query string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://docker"+path+"?"+query, nil)
	if err != nil {
		return errors.New("invalid Docker query")
	}
	response, err := p.Client.Do(req)
	if err != nil {
		return errors.New("Docker unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("Docker read failed")
	}
	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(out)
}

type inspect struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		Running    bool `json:"Running"`
		Restarting bool `json:"Restarting"`
	} `json:"State"`
	HostConfig struct {
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
	} `json:"HostConfig"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Read-only API", 403)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	fail := func() { http.Error(w, "Docker metrics unavailable", 502) }
	emit := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	if r.URL.Path == "/version" && r.URL.RawQuery == "" {
		var v struct {
			API string `json:"ApiVersion"`
		}
		if p.get(ctx, "/version", "", &v) != nil {
			fail()
			return
		}
		emit(v)
		return
	}
	match := versioned.FindStringSubmatch(r.URL.Path)
	if match == nil {
		http.Error(w, "Endpoint denied", 403)
		return
	}
	suffix := match[1]
	prefix := strings.TrimSuffix(r.URL.Path, suffix)
	if suffix == "containers/json" {
		for key := range r.URL.Query() {
			if key != "all" && key != "filters" {
				http.Error(w, "Query denied", 403)
				return
			}
		}
		filters, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project"}})
		query := url.Values{"all": {"true"}, "filters": {string(filters)}}
		var rows []struct {
			ID     string            `json:"Id"`
			Labels map[string]string `json:"Labels"`
		}
		if p.get(ctx, r.URL.Path, query.Encode(), &rows) != nil {
			fail()
			return
		}
		out := rows[:0]
		for _, row := range rows {
			if allowedProject(row.Labels) {
				row.Labels = labelsOnly(row.Labels)
				out = append(out, row)
			}
		}
		emit(out)
		return
	}
	if m := containerPath.FindStringSubmatch(suffix); m != nil {
		var item inspect
		if p.get(ctx, prefix+"containers/"+m[1]+"/json", "", &item) != nil {
			fail()
			return
		}
		if !allowedProject(item.Config.Labels) {
			http.Error(w, "Container denied", 403)
			return
		}
		item.Config.Labels = labelsOnly(item.Config.Labels)
		if m[2] == "json" {
			if r.URL.RawQuery != "" {
				http.Error(w, "Query denied", 403)
				return
			}
			emit(item)
			return
		}
		for key := range r.URL.Query() {
			if key != "stream" && key != "one-shot" {
				http.Error(w, "Query denied", 403)
				return
			}
		}
		var stats struct {
			Read time.Time `json:"read"`
			CPU  struct {
				Usage struct {
					Total uint64 `json:"total_usage"`
				} `json:"cpu_usage"`
			} `json:"cpu_stats"`
			Memory struct {
				Usage uint64            `json:"usage"`
				Stats map[string]uint64 `json:"stats"`
			} `json:"memory_stats"`
		}
		if p.get(ctx, r.URL.Path, "stream=false&one-shot=true", &stats) != nil {
			fail()
			return
		}
		subset := map[string]uint64{}
		for _, key := range []string{"inactive_file", "total_inactive_file"} {
			if value, ok := stats.Memory.Stats[key]; ok {
				subset[key] = value
			}
		}
		stats.Memory.Stats = subset
		emit(stats)
		return
	}
	if suffix == "events" {
		q := r.URL.Query()
		for key := range q {
			if key != "since" && key != "until" && key != "filters" {
				http.Error(w, "Query denied", 403)
				return
			}
		}
		since, e1 := strconv.ParseInt(q.Get("since"), 10, 64)
		until, e2 := strconv.ParseInt(q.Get("until"), 10, 64)
		now := time.Now().Unix()
		if e1 != nil || e2 != nil || since < 0 || until > now+2 || since > until {
			http.Error(w, "Bounded event range required", 403)
			return
		}
		if since < now-86400 {
			since = now - 86400
		}
		filters, _ := json.Marshal(map[string][]string{"event": {"oom"}})
		query := url.Values{"since": {strconv.FormatInt(since, 10)}, "until": {strconv.FormatInt(until, 10)}, "filters": {string(filters)}}
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker"+r.URL.Path+"?"+query.Encode(), nil)
		res, err := p.Client.Do(req)
		if err != nil {
			fail()
			return
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			fail()
			return
		}
		decoder := json.NewDecoder(io.LimitReader(res.Body, 16<<20))
		out := []map[string]any{}
		for {
			var event struct {
				Nano  int64  `json:"timeNano"`
				ID    string `json:"id"`
				Actor struct {
					ID    string            `json:"ID"`
					Attrs map[string]string `json:"Attributes"`
				} `json:"Actor"`
			}
			err := decoder.Decode(&event)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				fail()
				return
			}
			// Only Compose projects in our inventory; never forward arbitrary event attributes.
			allowed := allowedProject(event.Actor.Attrs)
			if !allowed && event.Actor.Attrs["com.docker.compose.project"] == "" {
				id := event.ID
				if id == "" {
					id = event.Actor.ID
				}
				if idPattern.MatchString(id) {
					var current inspect
					if p.get(ctx, prefix+"containers/"+id+"/json", "", &current) == nil {
						allowed = allowedProject(current.Config.Labels)
					}
				}
			}
			if allowed {
				out = append(out, map[string]any{"timeNano": event.Nano, "id": event.ID, "Actor": map[string]any{"ID": event.Actor.ID, "Attributes": map[string]string{"name": event.Actor.Attrs["name"]}}})
			}
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		encoder := json.NewEncoder(w)
		for _, event := range out {
			encoder.Encode(event)
		}
		return
	}
	http.Error(w, "Endpoint denied", 403)
}
