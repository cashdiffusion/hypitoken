package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsInvalidConfiguredProxy(t *testing.T) {
	for name, body := range map[string]string{
		"default": "default_proxy_url: ftp://proxy.example\n",
		"api-key": "api_keys:\n  - key: test\n    proxy_url: http://proxy.example/path\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("invalid proxy was accepted")
			}
		})
	}
}

func TestLoadNormalizesConfiguredProxyWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "default_proxy_url: '  http://proxy.example:8080  '\napi_keys:\n  - key: test\n    proxy_url: '  socks5://127.0.0.1:1080  '\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProxyURL != "http://proxy.example:8080" || cfg.APIKeys[0].ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("proxy values were not normalized: default=%q api=%q", cfg.DefaultProxyURL, cfg.APIKeys[0].ProxyURL)
	}
}
