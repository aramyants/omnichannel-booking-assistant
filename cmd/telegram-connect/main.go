// telegram-connect authorizes the studio sender locally. It never reads chats,
// imports contacts or sends messages. Session files must stay outside the repo.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"image/png"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"rsc.io/qr"
)

type connection struct {
	mu                    sync.Mutex
	state, picture, nonce string
	password              chan string
}

var page = template.Must(template.New("connect").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>E-Motion studio Telegram connection</title><style>body{margin:0;background:#faf8f6;color:#252323;font:17px/1.55 system-ui}main{max-width:620px;margin:60px auto;padding:28px}h1{font-size:30px}img{width:260px;height:260px;image-rendering:pixelated;display:block;margin:24px 0}input{padding:12px;font:inherit;border:1px solid #aaa;border-radius:6px}button{padding:12px 20px;background:#740e15;color:white;border:0;border-radius:6px;font:inherit}small{color:#65605d}</style><main><h1>Connect the studio Telegram account</h1><p><strong>@emotion_concept · +374 94 768067</strong></p><p>This authorizes our booking backend to act as the studio account. Telegram user sessions can access the account; our sender uses it only for appointment notifications. You can revoke the session in Telegram → Settings → Devices.</p>{{if eq .State "qr"}}<p>On the studio phone, open Telegram → Settings → Devices → Link Desktop Device. Scan this QR code and approve the sign-in.</p><img src="data:image/png;base64,{{.Picture}}" alt="Telegram studio account sign-in QR code"><small>The QR refreshes automatically. Keep this page open.</small>{{else if eq .State "password"}}<p>Telegram requires this account’s existing two-step verification password. Enter it here; it is used for this sign-in and is not saved.</p><form method="post" action="/password"><input type="hidden" name="nonce" value="{{.Nonce}}"><input type="password" name="password" autocomplete="current-password" required aria-label="Telegram two-step verification password"><button>Connect</button></form>{{else if eq .State "connected"}}<p><strong>Studio account connected.</strong> The private session has been saved for secure cloud installation. No customer message was sent.</p>{{else if eq .State "wrong_account"}}<p>Please connect the studio account shown above. The other account was signed out and was not saved.</p>{{else if eq .State "failed"}}<p>Telegram could not complete the connection. No authorized session was saved. Please restart the connection tool.</p>{{else}}<p>Preparing a secure Telegram sign-in…</p>{{end}}</main><script>setTimeout(async()=>{const s=await fetch('/state').then(r=>r.text());if(s!=={{.State}}||s==='qr')location.reload()},5000)</script></html>`))

func main() {
	configPath := flag.String("config", "", "private application configuration path")
	output := flag.String("session", "", "private session output path outside the repository")
	flag.Parse()
	if *configPath == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "Private config and session paths are required")
		os.Exit(1)
	}
	data, err := os.ReadFile(*configPath)
	var cfg struct {
		AppID    int    `json:"app_id"`
		AppHash  string `json:"app_hash"`
		Phone    string `json:"phone"`
		Username string `json:"username"`
	}
	if err != nil || json.Unmarshal(data, &cfg) != nil || cfg.AppID == 0 || cfg.AppHash == "" || cfg.Phone == "" {
		fmt.Fprintln(os.Stderr, "Private application configuration unavailable")
		os.Exit(1)
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		panic("Local connection nonce unavailable")
	}
	c := &connection{state: "preparing", nonce: base64.RawURLEncoding.EncodeToString(secret), password: make(chan string, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1:18767" || r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'")
		c.mu.Lock()
		view := struct{ State, Picture, Nonce string }{c.state, c.picture, c.nonce}
		c.mu.Unlock()
		_ = page.Execute(w, view)
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1:18767" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		c.mu.Lock()
		state := c.state
		c.mu.Unlock()
		_, _ = fmt.Fprint(w, state)
	})
	mux.HandleFunc("/password", func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1:18767" || r.Method != http.MethodPost || r.Header.Get("Origin") != "http://127.0.0.1:18767" {
			http.Error(w, "Not allowed", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if r.ParseForm() != nil || r.Form.Get("nonce") != c.nonce || r.Form.Get("password") == "" {
			http.Error(w, "Invalid connection request", 400)
			return
		}
		c.mu.Lock()
		awaiting := c.state == "password"
		c.mu.Unlock()
		if !awaiting {
			http.Error(w, "Not awaiting a password", http.StatusConflict)
			return
		}
		select {
		case c.password <- r.Form.Get("password"):
		default:
			http.Error(w, "Already processing", http.StatusConflict)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:18767")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Local connection port unavailable")
		os.Exit(1)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	fmt.Println("Studio account connection ready: http://127.0.0.1:18767/")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	storage := new(session.StorageMemory)
	dispatcher := tg.NewUpdateDispatcher()
	loggedIn := qrlogin.OnLoginToken(dispatcher)
	client := telegram.NewClient(cfg.AppID, cfg.AppHash, telegram.Options{SessionStorage: storage, UpdateHandler: dispatcher, Device: telegram.DeviceConfig{DeviceModel: "E-Motion booking notifications", SystemVersion: "Studio connection", AppVersion: "1.0", SystemLangCode: "en", LangCode: "en"}})
	setState := func(state string) { c.mu.Lock(); c.state = state; c.picture = ""; c.mu.Unlock() }
	err = client.Run(ctx, func(ctx context.Context) error {
		_, err := client.QR().Auth(ctx, loggedIn, func(_ context.Context, token qrlogin.Token) error {
			pic, e := token.Image(qr.M)
			if e != nil {
				return e
			}
			var buffer bytes.Buffer
			if e = png.Encode(&buffer, pic); e != nil {
				return e
			}
			c.mu.Lock()
			c.state = "qr"
			c.picture = base64.StdEncoding.EncodeToString(buffer.Bytes())
			c.mu.Unlock()
			return nil
		})
		if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
			for {
				setState("password")
				select {
				case <-ctx.Done():
					return ctx.Err()
				case password := <-c.password:
					_, err = client.Auth().Password(ctx, password)
				}
				if !errors.Is(err, auth.ErrPasswordInvalid) {
					break
				}
			}
		}
		if err != nil {
			return err
		}
		own, err := client.Self(ctx)
		if err != nil {
			return err
		}
		if own.Bot || "+"+strings.TrimPrefix(own.Phone, "+") != cfg.Phone || !strings.EqualFold(own.Username, cfg.Username) {
			_, _ = client.API().AuthLogOut(ctx)
			setState("wrong_account")
			return fmt.Errorf("studio account mismatch")
		}
		if err = storage.WriteFile(*output, 0600); err != nil {
			return err
		}
		setState("connected")
		fmt.Println("Verified studio session saved privately. No contacts imported or messages sent.")
		return nil
	})
	if err != nil {
		c.mu.Lock()
		if c.state != "wrong_account" {
			c.state = "failed"
		}
		c.picture = ""
		c.mu.Unlock()
		fmt.Println("Telegram account connection did not complete")
	}
	// Keep the result visible briefly, then release the loopback listener.
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Minute):
	}
	_ = server.Close()
}
