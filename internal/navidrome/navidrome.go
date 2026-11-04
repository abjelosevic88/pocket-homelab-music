// Package navidrome speaks the few parts of Navidrome's own (non-Subsonic)
// API that setting up a fresh server needs: the first administrator, signing
// in, and accounts.
package navidrome

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client reaches one Navidrome.
type Client struct {
	Base  string
	Token string // an administrator's session (from CreateAdmin or Login)
	HTTP  *http.Client
}

// New creates a client.
func New(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Error is a non-2xx answer.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("Navidrome answered HTTP %d: %s", e.Status, e.Msg)
	}
	return fmt.Sprintf("Navidrome answered HTTP %d", e.Status)
}

// ErrHasUsers: CreateAdmin on a server that already has accounts.
var ErrHasUsers = errors.New("Navidrome already has accounts")

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return err
	}
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		r.Header.Set("X-ND-Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(r)
	if err != nil {
		return fmt.Errorf("Navidrome is not reachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return &Error{Status: res.StatusCode, Msg: e.Error}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

type session struct {
	Token string `json:"token"`
}

// CreateAdmin makes the first account of a fresh server (an administrator)
// and signs in as it. ErrHasUsers when the server already has accounts.
func (c *Client) CreateAdmin(ctx context.Context, user, password string) error {
	var s session
	err := c.do(ctx, http.MethodPost, "/auth/createAdmin", map[string]string{"username": user, "password": password}, &s)
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusForbidden {
		return ErrHasUsers
	}
	if err != nil {
		return err
	}
	c.Token = s.Token
	return nil
}

// Login signs in.
func (c *Client) Login(ctx context.Context, user, password string) error {
	var s session
	if err := c.do(ctx, http.MethodPost, "/auth/login", map[string]string{"username": user, "password": password}, &s); err != nil {
		return err
	}
	c.Token = s.Token
	return nil
}

// User is an account.
type User struct {
	ID       string `json:"id"`
	UserName string `json:"userName"`
	Name     string `json:"name"`
}

// Users lists the accounts (an administrator only).
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var out []User
	return out, c.do(ctx, http.MethodGet, "/api/user", nil, &out)
}

// CreateUser adds an account that is not an administrator.
func (c *Client) CreateUser(ctx context.Context, userName, name, password string) error {
	return c.do(ctx, http.MethodPost, "/api/user", map[string]any{"userName": userName, "name": name, "password": password, "isAdmin": false}, nil)
}
