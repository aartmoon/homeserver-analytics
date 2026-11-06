package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompatibleStateAndDockerReadAPI(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "data/container-collector"), 0700)
	os.Mkdir(filepath.Join(root, "textfile"), 0755)
	legacy := `{"cursor":1793966400,"containers":{"ruk-app-1":{"project":"cottontoys","service":"app","id":"abc","restart_snapshot":2,"restarts":5,"oom":3,"up":1}},"seen":[]}`
	statePath := filepath.Join(root, "data/container-collector/state.json")
	os.WriteFile(statePath, []byte(legacy), 0600)
	count := 3
	cpu := uint64(1000000000)
	read := time.Now()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("Docker API mutation attempted")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.52"}`)
		case "/v1.52/containers/json":
			fmt.Fprint(w, `[{"Id":"abc","Labels":{"com.docker.compose.project":"ruk"}}]`)
		case "/v1.52/events":
			fmt.Fprint(w, `{"timeNano":123,"id":"abc","Actor":{"Attributes":{"name":"ruk-app-1"}}}`)
		case "/v1.52/containers/abc/json":
			fmt.Fprintf(w, `{"Id":"abc","Name":"/ruk-app-1","RestartCount":%d,"State":{"Running":true},"HostConfig":{"RestartPolicy":{"Name":"unless-stopped"}},"Config":{"Labels":{"com.docker.compose.project":"ruk","com.docker.compose.service":"app"}}}`, count)
		case "/v1.52/containers/abc/stats":
			if r.URL.Query().Get("one-shot") != "true" {
				t.Error("streaming stats used")
			}
			fmt.Fprintf(w, `{"read":%q,"cpu_stats":{"cpu_usage":{"total_usage":%d}},"memory_stats":{"usage":1024,"stats":{"inactive_file":256}}}`, read.Format(time.RFC3339Nano), cpu)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	api := &API{Client: ts.Client(), Base: ts.URL}
	if err := Collect(context.Background(), api, root); err != nil {
		t.Fatal(err)
	}
	count = 4
	cpu += 2000000000
	read = read.Add(time.Minute)
	if err := Collect(context.Background(), api, root); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(statePath)
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	rec := state.Containers["ruk-app-1"]
	if rec.Restarts != 7 || rec.OOM != 4 {
		t.Fatalf("state counters/dedup not preserved: %d/%d", rec.Restarts, rec.OOM)
	}
	data, _ = os.ReadFile(filepath.Join(root, "textfile/containers.prom"))
	text := string(data)
	if !strings.Contains(text, `project="cottontoys"`) || !strings.Contains(text, " 768\n") || !strings.Contains(text, " 0.03333333333333333\n") {
		t.Fatal("incorrect working set, labels or CPU delta")
	}
	before := string(data)
	ts.Close()
	if Collect(context.Background(), api, root) == nil {
		t.Fatal("Docker failure not reported")
	}
	data, _ = os.ReadFile(filepath.Join(root, "textfile/containers.prom"))
	if string(data) != before {
		t.Fatal("failed collection replaced successful metrics")
	}
}
func TestRecreationAndSharedProjects(t *testing.T) {
	state := &State{Containers: map[string]*Record{"ruk-postgres-1": {Project: "infrastructure", Service: "postgres", ID: "old", RestartSnapshot: 5, Restarts: 8, LastCPU: 100, LastCPUAt: 1}}}
	var item Inspect
	item.ID = "new"
	item.Name = "/ruk-postgres-1"
	item.RestartCount = 1
	item.Config.Labels = map[string]string{"com.docker.compose.project": "ruk", "com.docker.compose.service": "postgres"}
	Apply(state, item, nil, time.Now())
	r := state.Containers["ruk-postgres-1"]
	if r.Restarts != 9 || r.LastCPU != 0 || Project("ruk", "postgres") != "infrastructure" || Project("talk", "backend") != "talk" {
		t.Fatal("recreation/labels incorrect")
	}
}
