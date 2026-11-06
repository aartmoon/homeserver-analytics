package serverbot

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Token, Proxy, Root, Data, Host, GrafanaSecret, KumaSecret string
	Owner                                                     int64
	Port                                                      int
	Enabled                                                   bool
}

// EnvironmentFile never runs shell code; the service environment takes precedence.
func EnvironmentFile(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "\"") {
			end := strings.LastIndex(value, "\"")
			if end < 1 {
				return errors.New("invalid quoted environment value")
			}
			value, err = strconv.Unquote(value[:end+1])
			if err != nil {
				return errors.New("invalid quoted environment value")
			}
		} else if strings.HasPrefix(value, "'") {
			end := strings.LastIndex(value, "'")
			if end < 1 {
				return errors.New("invalid quoted environment value")
			}
			value = value[1:end]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err = os.Setenv(key, value); err != nil {
				return errors.New("invalid environment key")
			}
		}
	}
	return scanner.Err()
}
func LoadConfig(root string) (Config, error) {
	c := Config{Root: root, Data: filepath.Join(root, "data"), Port: 9088, Host: "127.0.0.1"}
	if err := EnvironmentFile(filepath.Join(root, ".env")); err != nil {
		return c, errors.New("cannot read bot configuration")
	}
	c.Token = os.Getenv("TG_BOT_TOKEN")
	c.Owner, _ = strconv.ParseInt(os.Getenv("TG_ALLOWED_ID"), 10, 64)
	var exists bool
	c.Proxy, exists = os.LookupEnv("TG_PROXY")
	if !exists {
		c.Proxy = "socks5://127.0.0.1:1080"
	}
	if c.Token == "" || c.Owner <= 0 {
		return c, errors.New("TG_BOT_TOKEN and positive TG_ALLOWED_ID are required")
	}
	c.Enabled = os.Getenv("MONITORING_WEBHOOKS_ENABLED") == "1"
	if value := os.Getenv("MONITORING_WEBHOOK_HOST"); value != "" {
		c.Host = value
	}
	if value := os.Getenv("MONITORING_WEBHOOK_PORT"); value != "" {
		var err error
		c.Port, err = strconv.Atoi(value)
		if err != nil || c.Port < 1 || c.Port > 65535 {
			return c, errors.New("invalid webhook port")
		}
	}
	if c.Enabled {
		ip := net.ParseIP(c.Host)
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || (!ip.IsLoopback() && !ip.IsPrivate()) {
			return c, errors.New("webhook must bind to loopback or a private bridge IP")
		}
		for _, item := range []struct {
			key string
			out *string
		}{{"MONITORING_GRAFANA_SECRET_FILE", &c.GrafanaSecret}, {"MONITORING_KUMA_SECRET_FILE", &c.KumaSecret}} {
			data, err := os.ReadFile(os.Getenv(item.key))
			if err != nil {
				return c, errors.New("cannot read webhook secret file")
			}
			*item.out = strings.TrimSpace(string(data))
			if len(*item.out) < 32 {
				return c, errors.New("webhook secrets must have at least 32 characters")
			}
		}
	}
	return c, nil
}
