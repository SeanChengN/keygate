package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/tabloy/keygate/internal/model"
)

// Provider errors never contain response bodies, credentials or upstream URLs.
type AIProviderError struct {
	Status    int
	Code      string
	Uncertain bool
}

func (e *AIProviderError) Error() string {
	return fmt.Sprintf("provider request failed (%d, %s)", e.Status, e.Code)
}

type AIAccountTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

type JijiKey struct {
	ID        int64      `json:"id"`
	Key       string     `json:"key"`
	Name      string     `json:"name"`
	GroupID   int64      `json:"group_id"`
	Status    string     `json:"status"`
	Quota     float64    `json:"quota"`
	QuotaUsed float64    `json:"quota_used"`
	ExpiresAt *time.Time `json:"expires_at"`
	Rate5H    float64    `json:"rate_limit_5h"`
	Rate1D    float64    `json:"rate_limit_1d"`
	Rate7D    float64    `json:"rate_limit_7d"`
	Usage5H   float64    `json:"usage_5h"`
	Usage1D   float64    `json:"usage_1d"`
	Usage7D   float64    `json:"usage_7d"`
}

func (k *JijiKey) Public() map[string]any {
	return map[string]any{"id": k.ID, "status": k.Status, "quota": k.Quota, "quota_used": k.QuotaUsed, "expires_at": k.ExpiresAt, "rate_limit_5h": k.Rate5H, "rate_limit_1d": k.Rate1D, "rate_limit_7d": k.Rate7D, "usage_5h": k.Usage5H, "usage_1d": k.Usage1D, "usage_7d": k.Usage7D}
}

type JijiClient struct {
	BaseURL string
	HTTP    *http.Client
}

func NewJijiClient() *JijiClient {
	return &JijiClient{BaseURL: "https://www.jiji.cc", HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *JijiClient) request(ctx context.Context, token, method, path string, input, output any) error {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return errors.New("invalid provider request")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/v1"+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid provider request")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return &AIProviderError{Code: "transport", Uncertain: method != "GET"}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		return &AIProviderError{Status: res.StatusCode, Code: "invalid_response", Uncertain: method != "GET"}
	}
	var envelope struct {
		Code json.RawMessage `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		code := "rejected"
		var business struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(raw, &business) == nil {
			switch business.Code {
			case "insufficient_quota", "API_KEY_DISABLED", "API_KEY_EXPIRED":
				code = business.Code
			}
		}
		return &AIProviderError{Status: res.StatusCode, Code: code, Uncertain: method != "GET" && res.StatusCode >= 500}
	}
	if json.Unmarshal(raw, &envelope) != nil || string(envelope.Code) != "0" {
		return &AIProviderError{Status: res.StatusCode, Code: "invalid_response", Uncertain: method != "GET"}
	}
	if output != nil && json.Unmarshal(envelope.Data, output) != nil {
		return &AIProviderError{Status: res.StatusCode, Code: "invalid_response", Uncertain: method != "GET"}
	}
	return nil
}
func (c *JijiClient) Get(ctx context.Context, token string, id int64) (*JijiKey, error) {
	k := new(JijiKey)
	err := c.request(ctx, token, "GET", fmt.Sprintf("/keys/%d", id), nil, k)
	return k, err
}
func (c *JijiClient) Update(ctx context.Context, token string, id int64, v map[string]any) error {
	return c.request(ctx, token, "PUT", fmt.Sprintf("/keys/%d", id), v, nil)
}
func (c *JijiClient) Create(ctx context.Context, token, name string, v model.AIPlanSettings) (*JijiKey, error) {
	k := new(JijiKey)
	err := c.request(ctx, token, "POST", "/keys", map[string]any{"name": name, "group_id": v.GroupID, "quota": v.Quota, "expires_in_days": 1, "rate_limit_5h": v.Rate5H, "rate_limit_1d": v.Rate1D, "rate_limit_7d": v.Rate7D}, k)
	if err == nil && (k.ID <= 0 || k.Key == "" || k.Name != name) {
		err = &AIProviderError{Code: "create_identity_missing", Uncertain: true}
	}
	return k, err
}
func (c *JijiClient) Find(ctx context.Context, token, name string) (*JijiKey, error) {
	var found *JijiKey
	for page := 1; page <= 100; page++ {
		var data struct {
			Items []JijiKey `json:"items"`
			Pages int       `json:"pages"`
		}
		err := c.request(ctx, token, "GET", fmt.Sprintf("/keys?page=%d&page_size=100&search=%s", page, url.QueryEscape(name)), nil, &data)
		if err != nil {
			return nil, err
		}
		if data.Pages < 1 || data.Items == nil {
			return nil, errors.New("provider search incomplete; manual resolution required")
		}
		for _, k := range data.Items {
			if k.Name == name {
				if found != nil {
					return nil, errors.New("duplicate provider key name; manual resolution required")
				}
				copy := k
				found = &copy
			}
		}
		if page >= data.Pages {
			return found, nil
		}
	}
	return nil, errors.New("provider search incomplete; manual resolution required")
}
func (c *JijiClient) Refresh(ctx context.Context, t *AIAccountTokens) (*AIAccountTokens, error) {
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	err := c.request(ctx, "", "POST", "/auth/refresh", map[string]string{"refresh_token": t.RefreshToken}, &result)
	if err != nil {
		return nil, err
	}
	if result.AccessToken == "" || result.RefreshToken == "" || result.ExpiresIn <= 0 {
		return nil, &AIProviderError{Code: "invalid_refresh", Uncertain: true}
	}
	return &AIAccountTokens{result.AccessToken, result.RefreshToken, time.Now().Unix() + result.ExpiresIn}, nil
}
