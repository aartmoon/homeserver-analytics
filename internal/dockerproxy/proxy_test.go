package dockerproxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyAndSecretRedaction(t *testing.T) {
	calls := 0
	id := strings.Repeat("a", 64)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			t.Error("upstream mutation")
		}
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.52","secret":"do-not-forward"}`)
		case "/v1.52/containers/json":
			fmt.Fprintf(w, `[{"Id":%q,"Command":"password-secret","Labels":{"com.docker.compose.project":"ruk","com.docker.compose.service":"app","private":"label-secret"}}]`, id)
		default:
			fmt.Fprintf(w, `{"Id":%q,"Name":"/ruk-app-1","Config":{"Env":["TOKEN=credential-secret"],"Labels":{"com.docker.compose.project":"ruk","com.docker.compose.service":"app","private":"label-secret"}},"HostConfig":{"Binds":["private-secret"],"RestartPolicy":{"Name":"unless-stopped"}},"State":{"Running":true}}`, id)
		}
	}))
	defer upstream.Close()
	proxy := &Proxy{Client: upstream.Client()}
	original := proxy.Client.Transport
	proxy.Client.Transport = redirect{base: upstream.URL, next: original}
	for _, endpoint := range []string{"/version", "/v1.52/containers/json?all=true", "/v1.52/containers/" + id + "/json"} {
		rr := httptest.NewRecorder()
		proxy.ServeHTTP(rr, httptest.NewRequest("GET", endpoint, nil))
		if rr.Code != 200 {
			t.Fatalf("allowed read failed %d", rr.Code)
		}
		if strings.Contains(rr.Body.String(), "secret") || strings.Contains(rr.Body.String(), "Env") {
			t.Fatal("sensitive Docker response leaked")
		}
	}
	before := calls
	for _, entry := range []struct{ method, path string }{{"POST", "/v1.52/containers/create"}, {"DELETE", "/v1.52/containers/" + id}, {"GET", "/v1.52/containers/" + id + "/archive?path=/"}, {"GET", "/v1.52/containers/" + id + "/logs"}, {"GET", "/info"}, {"GET", "/v1.52/events?since=1&until=99999999999"}} {
		rr := httptest.NewRecorder()
		proxy.ServeHTTP(rr, httptest.NewRequest(entry.method, entry.path, nil))
		if rr.Code != 403 {
			t.Fatal("unexpected endpoint allowed")
		}
	}
	if before != calls {
		t.Fatal("denied request reached upstream")
	}
}

type redirect struct {
	base string
	next http.RoundTripper
}

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	next := req.Clone(req.Context())
	url := *req.URL
	parts := strings.SplitN(strings.TrimPrefix(r.base, "http://"), "/", 2)
	url.Host = parts[0]
	next.URL = &url
	return r.next.RoundTrip(next)
}

func TestOOMEventProjection(t *testing.T) {
	id := strings.Repeat("b", 64)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			fmt.Fprintf(w, `{"timeNano":123,"id":%q,"Actor":{"Attributes":{"name":"monitoring-serverbot-1","token":"hidden","com.docker.compose.project":"monitoring"}}}`+"\n", id)
			return
		}
		t.Error("unexpected upstream request")
	}))
	defer upstream.Close()
	p := &Proxy{Client: upstream.Client()}
	p.Client.Transport = redirect{base: upstream.URL, next: p.Client.Transport}
	now := time.Now().Unix()
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, httptest.NewRequest("GET", fmt.Sprintf("/v1.52/events?since=%d&until=%d", now-5, now), nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "monitoring-serverbot-1") || strings.Contains(rr.Body.String(), "hidden") || strings.Contains(rr.Body.String(), "token") {
		t.Fatal("OOM events not redacted")
	}
}
