package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
)

// NacosConfig 是 Nacos 注册中心的配置。
//
// The exported alias NacosConfig is declared in api.go, which collects the
// public API surface of the package.
type NacosConfig struct {
	// Servers 是 Nacos 服务端地址列表，例如 "127.0.0.1:8848"。
	Servers []string
	// Namespace 是命名空间 ID。
	Namespace string
	// Group 是服务分组，默认 DEFAULT_GROUP。
	Group string
	// Username 是登录用户名，为空时不登录。
	Username string
	// Password 是登录密码。
	Password string
	// Interval 是实例列表的轮询间隔，默认 5 秒。
	Interval time.Duration
	// Service 是注册中心侧的服务名，默认与 gRPC 服务名相同。
	Service string
}

// WithNacos 让客户端通过 Nacos 发现服务提供者。
//
// The exported wrapper WithNacos is declared in api.go, which collects the
// public API surface of the package.
func withNacos(cfg NacosConfig) ClientOption {
	return func(o *clientOptions) {
		o.registryService = cfg.Service
		o.nacos = &cfg
	}
}

// nacosRegistry 通过 Nacos 的开放 HTTP API 发现服务提供者。
type nacosRegistry struct {
	// cfg 是注册中心的配置。
	cfg NacosConfig
	// client 是访问 Nacos 的 HTTP 客户端。
	client *http.Client
	// token 是登录得到的访问令牌。
	token string
	// tokenExpiry 是访问令牌的过期时刻。
	tokenExpiry time.Time
}

// NewNacosRegistry 创建一个 Nacos 注册中心。
//
// The exported wrapper NewNacosRegistry is declared in api.go, which collects
// the public API surface of the package.
func newNacosRegistry(cfg NacosConfig) (Registry, error) {
	if len(cfg.Servers) == 0 {
		return nil, errs.New("The Nacos registry has no server")
	}
	if cfg.Group == "" {
		cfg.Group = "DEFAULT_GROUP"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	return &nacosRegistry{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

// Discover 订阅实例列表，按间隔轮询并在实例集合变化时推送。
func (r *nacosRegistry) Discover(ctx context.Context, service string) (<-chan []Instance, error) {
	ch := make(chan []Instance, 1)
	go r.poll(ctx, service, ch)
	return ch, nil
}

// poll 轮询实例列表。
func (r *nacosRegistry) poll(ctx context.Context, service string, ch chan<- []Instance) {
	defer close(ch)
	var last []Instance
	for {
		list, err := r.instances(ctx, service)
		if err != nil {
			log.WarnErrF("Failed to discover the instances of the service [{0}] from Nacos", err, service)
		} else if !sameInstances(last, list) {
			last = list
			select {
			case ch <- list:
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.cfg.Interval):
		}
	}
}

// instances 查询一次实例列表。
func (r *nacosRegistry) instances(ctx context.Context, service string) ([]Instance, error) {
	token, err := r.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("serviceName", service)
	q.Set("groupName", r.cfg.Group)
	q.Set("healthyOnly", "false")
	if r.cfg.Namespace != "" {
		q.Set("namespaceId", r.cfg.Namespace)
	}
	if token != "" {
		q.Set("accessToken", token)
	}
	body, err := r.get(ctx, "/nacos/v1/ns/instance/list", q)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Hosts []struct {
			IP      string            `json:"ip"`
			Port    int               `json:"port"`
			Healthy bool              `json:"healthy"`
			Enabled bool              `json:"enabled"`
			Weight  float64           `json:"weight"`
			Meta    map[string]string `json:"metadata"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, errs.Wrap(err, "Cannot parse the instance list of the service [{0}]", service)
	}
	list := make([]Instance, 0, len(resp.Hosts))
	for _, h := range resp.Hosts {
		list = append(list, Instance{
			Addr:     net.JoinHostPort(h.IP, strconv.Itoa(h.Port)),
			Healthy:  h.Healthy && h.Enabled,
			Weight:   h.Weight,
			Metadata: h.Meta,
		})
	}
	return list, nil
}

// accessToken 返回访问令牌，过期时重新登录。
func (r *nacosRegistry) accessToken(ctx context.Context) (string, error) {
	if r.cfg.Username == "" {
		return "", nil
	}
	if r.token != "" && time.Now().Before(r.tokenExpiry) {
		return r.token, nil
	}
	form := url.Values{}
	form.Set("username", r.cfg.Username)
	form.Set("password", r.cfg.Password)
	body, err := r.post(ctx, "/nacos/v1/auth/login", form)
	if err != nil {
		return "", err
	}
	var resp struct {
		AccessToken string `json:"accessToken"`
		TokenTTL    int64  `json:"tokenTtl"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", errs.Wrap(err, "Cannot parse the login response of Nacos")
	}
	ttl := resp.TokenTTL
	if ttl <= 0 {
		ttl = 18000
	}
	r.token = resp.AccessToken
	// 提前一分钟刷新，避免令牌在请求途中过期。
	r.tokenExpiry = time.Now().Add(time.Duration(ttl-60) * time.Second)
	return r.token, nil
}

// get 发起一次 GET 请求。
func (r *nacosRegistry) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL()+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	return r.do(req)
}

// post 发起一次 POST 请求。
func (r *nacosRegistry) post(ctx context.Context, path string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r.do(req)
}

// do 执行请求并读取响应体。
func (r *nacosRegistry) do(req *http.Request) ([]byte, error) {
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errs.New("The Nacos request [{0}] failed with the status [{1}]", req.URL.Path, resp.StatusCode)
	}
	return body, nil
}

// baseURL 返回 Nacos 的基地址。
func (r *nacosRegistry) baseURL() string {
	addr := r.cfg.Servers[0]
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return strings.TrimSuffix(addr, "/")
}

// sameInstances 判断两个实例列表是否相同。
func sameInstances(a, b []Instance) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Addr != b[i].Addr || a[i].Healthy != b[i].Healthy {
			return false
		}
	}
	return true
}
