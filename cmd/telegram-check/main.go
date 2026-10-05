// telegram-check validates the encrypted studio session without customer data.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/telegramaccount"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/firestore"
)

func main() {
	credentials := flag.String("credentials", "", "private studio credential JSON path outside the repository")
	project := flag.String("project", "", "configured Google Cloud project")
	flag.Parse()
	if *credentials == "" || *project == "" {
		fmt.Fprintln(os.Stderr, "Private credential path and cloud project are required")
		os.Exit(1)
	}
	data, err := os.ReadFile(*credentials)
	var cfg struct {
		AppID         int    `json:"app_id"`
		AppHash       string `json:"app_hash"`
		Phone         string `json:"phone"`
		Username      string `json:"username"`
		EncryptionKey []byte `json:"encryption_key"`
	}
	if err != nil || json.Unmarshal(data, &cfg) != nil {
		fmt.Fprintln(os.Stderr, "Private studio credentials unavailable")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	store, err := firestore.New(ctx, *project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Encrypted cloud session store unavailable")
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()
	client, err := telegramaccount.New(telegramaccount.Config{AppID: cfg.AppID, AppHash: cfg.AppHash, Phone: cfg.Phone, Username: cfg.Username, EncryptionKey: cfg.EncryptionKey}, store)
	if err != nil || client.CheckConnection(ctx) != nil {
		fmt.Fprintln(os.Stderr, "Studio Telegram connection check failed")
		os.Exit(1)
	}
	fmt.Println("Studio account ready: encrypted cloud session and account identity verified; no customer phones resolved or messages sent")
}
