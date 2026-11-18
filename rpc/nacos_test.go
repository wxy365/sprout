package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNacosRegistryDiscoversTheInstances 用一个模拟的 Nacos 服务验证实例发现。
func TestNacosRegistryDiscoversTheInstances(t *testing.T) {
	hosts := []map[string]any{
		{"ip": "10.0.0.1", "port": 8080, "healthy": true, "enabled": true, "weight": 1.0},
		{"ip": "10.0.0.2", "port": 8080, "healthy": false, "enabled": true, "weight": 1.0},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/nacos/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"accessToken": "tk", "tokenTtl": 18000})
	})
	mux.HandleFunc("/nacos/v1/ns/instance/list", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("serviceName"); got != "demo-greeter" {
			t.Errorf("the serviceName = %q", got)
		}
		if got := r.URL.Query().Get("groupName"); got != "DEFAULT_GROUP" {
			t.Errorf("the groupName = %q", got)
		}
		if got := r.URL.Query().Get("accessToken"); got != "tk" {
			t.Errorf("the accessToken = %q", got)
		}
		json.NewEncoder(w).Encode(map[string]any{"hosts": hosts})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg, err := NewNacosRegistry(NacosConfig{
		Servers:  []string{srv.URL},
		Group:    "DEFAULT_GROUP",
		Username: "nacos",
		Password: "nacos",
		Interval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewNacosRegistry: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := reg.Discover(ctx, "demo-greeter")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	select {
	case list := <-ch:
		if len(list) != 2 {
			t.Fatalf("the instances = %d, want 2", len(list))
		}
		if list[0].Addr != "10.0.0.1:8080" {
			t.Errorf("the address = %q", list[0].Addr)
		}
		if !list[0].Healthy {
			t.Errorf("the first instance should be healthy")
		}
		if list[1].Healthy {
			t.Errorf("the second instance should be unhealthy")
		}
	case <-ctx.Done():
		t.Fatalf("no instance list received")
	}
}

// TestNacosRegistryPushesTheChanges 验证实例集合变化时会被推送。
func TestNacosRegistryPushesTheChanges(t *testing.T) {
	var current = []map[string]any{
		{"ip": "10.0.0.1", "port": 8080, "healthy": true, "enabled": true, "weight": 1.0},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/nacos/v1/ns/instance/list", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"hosts": current})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg, err := NewNacosRegistry(NacosConfig{
		Servers:  []string{srv.URL},
		Interval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewNacosRegistry: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := reg.Discover(ctx, "demo-greeter")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	first := <-ch
	if len(first) != 1 {
		t.Fatalf("the instances = %d, want 1", len(first))
	}

	current = []map[string]any{
		{"ip": "10.0.0.1", "port": 8080, "healthy": true, "enabled": true, "weight": 1.0},
		{"ip": "10.0.0.2", "port": 8080, "healthy": true, "enabled": true, "weight": 1.0},
	}
	select {
	case second := <-ch:
		if len(second) != 2 {
			t.Fatalf("the instances = %d, want 2", len(second))
		}
	case <-ctx.Done():
		t.Fatalf("the change was not pushed")
	}
}

func TestNacosRegistryNeedsAServer(t *testing.T) {
	if _, err := NewNacosRegistry(NacosConfig{}); err == nil {
		t.Errorf("a Nacos registry without a server should be rejected")
	}
}

// TestClientResolvesTheAddressesFromTheRegistry 验证客户端通过注册中心寻址。
func TestClientResolvesTheAddressesFromTheRegistry(t *testing.T) {
	addr := startTestServer(t, helloService())

	reg := &fakeRegistry{ch: make(chan []Instance, 1)}
	reg.ch <- []Instance{{Addr: addr, Healthy: true}}

	client, err := NewClient("demo.Greeter", WithRegistry(reg))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	hello := Doer[svcHelloReq, svcHelloResp](client, "Hello")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := hello(ctx, &svcHelloReq{Name: "wxy"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Greeting != "hello wxy" {
		t.Errorf("the greeting = %q", resp.Greeting)
	}
}

// fakeRegistry 是一个可控的注册中心。
type fakeRegistry struct {
	// ch 是实例列表的更新通道。
	ch chan []Instance
}

// Discover 返回实例列表的更新通道。
func (r *fakeRegistry) Discover(ctx context.Context, service string) (<-chan []Instance, error) {
	return r.ch, nil
}
