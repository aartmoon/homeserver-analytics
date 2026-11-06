package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aartmoon/homeserver-analytics/internal/localfile"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Record struct {
	Project         string `json:"project"`
	Service         string `json:"service"`
	ID              string `json:"id"`
	RestartSnapshot int    `json:"restart_snapshot"`
	Restarts        int    `json:"restarts"`
	OOM             int    `json:"oom"`
	Up              int    `json:"up"`
	LastCPU         uint64 `json:"last_cpu_total,omitempty"`
	LastCPUAt       int64  `json:"last_cpu_at,omitempty"`
}
type State struct {
	Cursor     float64            `json:"cursor"`
	Containers map[string]*Record `json:"containers"`
	Seen       []string           `json:"seen"`
}
type Inspect struct {
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
type Stats struct {
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
type Event struct {
	TimeNano int64  `json:"timeNano"`
	ID       string `json:"id"`
	Actor    struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}
type API struct {
	Client *http.Client
	Base   string
}

func NewAPI(socket string) *API {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, MaxConnsPerHost: 6, MaxIdleConnsPerHost: 6}
	return &API{Client: &http.Client{Transport: transport, Timeout: 15 * time.Second}, Base: "http://docker"}
}
func (a *API) get(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", a.Base+path, nil)
	if err != nil {
		return errors.New("invalid Docker query")
	}
	response, err := a.Client.Do(req)
	if err != nil {
		return errors.New("Docker read API unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return os.ErrNotExist
	}
	if response.StatusCode != 200 {
		return errors.New("Docker read API rejected request")
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(result); err != nil {
		return errors.New("invalid Docker read response")
	}
	return nil
}
func (a *API) events(ctx context.Context, path string) ([]Event, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.Base+path, nil)
	if err != nil {
		return nil, err
	}
	response, err := a.Client.Do(req)
	if err != nil {
		return nil, errors.New("Docker event API unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("Docker event API rejected request")
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<20))
	events := []Event{}
	for {
		var event Event
		err = decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			return events, nil
		}
		if err != nil {
			return nil, errors.New("invalid Docker events")
		}
		events = append(events, event)
	}
}
func Project(project, service string) string {
	if project == "ruk" {
		if service == "postgres" || service == "traefik" {
			return "infrastructure"
		}
		return "cottontoys"
	}
	if project == "monitoring" {
		return "infrastructure"
	}
	return project
}
func labels(r *Record, name string) string {
	b, _ := json.Marshal(r.Project)
	s, _ := json.Marshal(r.Service)
	n, _ := json.Marshal(name)
	return fmt.Sprintf(`{project=%s,service=%s,environment="production",instance=%s}`, b, s, n)
}
func Apply(state *State, item Inspect, stats *Stats, now time.Time) (float64, uint64) {
	name := strings.TrimPrefix(item.Name, "/")
	pr := item.Config.Labels["com.docker.compose.project"]
	service := item.Config.Labels["com.docker.compose.service"]
	rec := state.Containers[name]
	if rec == nil {
		rec = &Record{Project: Project(pr, service), Service: service}
		state.Containers[name] = rec
	}
	delta := item.RestartCount - rec.RestartSnapshot
	if rec.ID != item.ID {
		delta = item.RestartCount
		rec.LastCPU = 0
		rec.LastCPUAt = 0
	}
	if delta > 0 {
		rec.Restarts += delta
	}
	rec.RestartSnapshot = item.RestartCount
	rec.ID = item.ID
	rec.Up = 0
	if item.State.Running && !item.State.Restarting {
		rec.Up = 1
	}
	cores := 0.0
	var memory uint64
	if stats != nil {
		at := stats.Read.UnixNano()
		if stats.Read.IsZero() {
			at = now.UnixNano()
		}
		cpu := stats.CPU.Usage.Total
		if rec.LastCPUAt > 0 && at > rec.LastCPUAt && cpu >= rec.LastCPU {
			cores = float64(cpu-rec.LastCPU) / float64(at-rec.LastCPUAt)
		}
		rec.LastCPU = cpu
		rec.LastCPUAt = at
		memory = stats.Memory.Usage
		cache, exists := stats.Memory.Stats["inactive_file"]
		if !exists {
			cache = stats.Memory.Stats["total_inactive_file"]
		}
		if cache < memory {
			memory -= cache
		}
	}
	return cores, memory
}
func ApplyEvents(state *State, events []Event) {
	seen := map[string]bool{}
	for _, key := range state.Seen {
		seen[key] = true
	}
	for _, event := range events {
		id := event.ID
		if id == "" {
			id = event.Actor.ID
		}
		key := strconv.FormatInt(event.TimeNano, 10) + ":" + id
		if seen[key] {
			continue
		}
		seen[key] = true
		if rec := state.Containers[event.Actor.Attributes["name"]]; rec != nil {
			rec.OOM++
		}
	}
	state.Seen = state.Seen[:0]
	for key := range seen {
		state.Seen = append(state.Seen, key)
	}
	sort.Strings(state.Seen)
	if len(state.Seen) > 10000 {
		state.Seen = state.Seen[len(state.Seen)-10000:]
	}
}
func Collect(ctx context.Context, api *API, root string) error {
	stateDir := filepath.Join(root, "data/container-collector")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	previous := State{Cursor: float64(time.Now().Unix()) - 1, Containers: map[string]*Record{}}
	statePath := filepath.Join(stateDir, "state.json")
	if data, err := os.ReadFile(statePath); err == nil {
		if json.Unmarshal(data, &previous) != nil {
			return errors.New("invalid collector state")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if previous.Containers == nil {
		previous.Containers = map[string]*Record{}
	}
	for _, rec := range previous.Containers {
		rec.Up = 0
	}
	var version struct {
		API string `json:"ApiVersion"`
	}
	if err := api.get(ctx, "/version", &version); err != nil {
		return err
	}
	if version.API == "" {
		return errors.New("missing Docker API version")
	}
	prefix := "/v" + version.API
	filters, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project"}})
	var containers []struct {
		ID     string            `json:"Id"`
		Labels map[string]string `json:"Labels"`
	}
	if err := api.get(ctx, prefix+"/containers/json?all=true&filters="+url.QueryEscape(string(filters)), &containers); err != nil {
		return err
	}
	now := time.Now()
	eventFilters, _ := json.Marshal(map[string][]string{"event": {"oom"}})
	eventPath := prefix + "/events?since=" + strconv.FormatInt(int64(previous.Cursor)-1, 10) + "&until=" + strconv.FormatInt(now.Unix(), 10) + "&filters=" + url.QueryEscape(string(eventFilters))
	events, err := api.events(ctx, eventPath)
	if err != nil {
		return err
	}
	type sample struct {
		item  Inspect
		stats *Stats
		err   error
	}
	samples := make([]sample, len(containers))
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for i, item := range containers {
		pr := item.Labels["com.docker.compose.project"]
		if pr != "ruk" && pr != "talk" && pr != "monitoring" {
			continue
		}
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
			case <-ctx.Done():
				samples[i].err = ctx.Err()
				return
			}
			defer func() { <-limit }()
			var inspect Inspect
			if err := api.get(ctx, prefix+"/containers/"+id+"/json", &inspect); err != nil {
				if !os.IsNotExist(err) {
					samples[i].err = err
				}
				return
			}
			policy := inspect.HostConfig.RestartPolicy.Name
			if policy != "always" && policy != "unless-stopped" && policy != "on-failure" {
				return
			}
			samples[i].item = inspect
			if inspect.State.Running {
				var stats Stats
				if err := api.get(ctx, prefix+"/containers/"+id+"/stats?stream=false&one-shot=true", &stats); err != nil {
					if !os.IsNotExist(err) {
						samples[i].err = err
					}
					return
				}
				samples[i].stats = &stats
			}
		}(i, item.ID)
	}
	wg.Wait()
	type usage struct {
		CPU    float64
		Memory uint64
	}
	usages := map[string]usage{}
	for _, sample := range samples {
		if sample.err != nil {
			return sample.err
		}
		if sample.item.ID == "" {
			continue
		}
		cpu, mem := Apply(&previous, sample.item, sample.stats, now)
		if sample.stats != nil {
			usages[strings.TrimPrefix(sample.item.Name, "/")] = usage{cpu, mem}
		}
	}
	ApplyEvents(&previous, events)
	var output strings.Builder
	writer := bufio.NewWriter(&output)
	for _, metric := range []struct{ name, kind string }{{"container_up", "gauge"}, {"container_restarts_total", "counter"}, {"container_oom_events_total", "counter"}, {"container_memory_working_set_bytes", "gauge"}, {"container_cpu_usage_cores", "gauge"}} {
		help := map[string]string{
			"container_up":                       "Whether the expected long-running container is running.",
			"container_restarts_total":           "Observed automatic Docker restarts, preserved across recreation.",
			"container_oom_events_total":         "Observed deduplicated Docker OOM events.",
			"container_memory_working_set_bytes": "Container memory usage minus inactive file cache in bytes.",
			"container_cpu_usage_cores":          "CPU cores averaged since the previous successful stats sample.",
		}
		fmt.Fprintf(writer, "# HELP %s %s\n# TYPE %s %s\n", metric.name, help[metric.name], metric.name, metric.kind)
	}
	names := make([]string, 0, len(previous.Containers))
	for name := range previous.Containers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := previous.Containers[name]
		lab := labels(r, name)
		fmt.Fprintf(writer, "container_up%s %d\ncontainer_restarts_total%s %d\ncontainer_oom_events_total%s %d\n", lab, r.Up, lab, r.Restarts, lab, r.OOM)
		if u, exists := usages[name]; exists {
			fmt.Fprintf(writer, "container_memory_working_set_bytes%s %d\ncontainer_cpu_usage_cores%s %g\n", lab, u.Memory, lab, u.CPU)
		}
	}
	fmt.Fprintln(writer, "# HELP container_collector_last_success_timestamp_seconds Last complete successful container collection Unix timestamp.\n# TYPE container_collector_last_success_timestamp_seconds gauge")
	fmt.Fprintf(writer, "container_collector_last_success_timestamp_seconds%s %.3f\n", labels(&Record{Project: "infrastructure", Service: "container-collector"}, "homeserver"), float64(now.UnixNano())/1e9)
	writer.Flush()
	previous.Cursor = float64(now.UnixNano()) / 1e9
	data, err := json.Marshal(previous)
	if err != nil {
		return err
	}
	if err = localfile.Atomic(statePath, data, 0600); err != nil {
		return err
	}
	return localfile.Atomic(filepath.Join(root, "textfile/containers.prom"), []byte(output.String()), 0644)
}
