// Command livewalldemo previews the Live Wall page with fake traffic, without
// running the bot. Useful to tune themes / backgrounds:
//
//	go run ./cmd/livewalldemo            -> http://127.0.0.1:47321/
//	go run ./cmd/livewalldemo -bg data/logwall -open
//
// -listen 0.0.0.0:8080 additionally exposes a reverse proxy (for remote
// previews); the wall itself always stays on 127.0.0.1.
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"chitchatbot/internal/logwall"
	"chitchatbot/internal/model"
)

func main() {
	port := flag.Int("port", logwall.DefaultPort, "live wall port (127.0.0.1 only)")
	bgDir := flag.String("bg", "data/logwall", "folder with background photos")
	listen := flag.String("listen", "", "optional extra listen address for a reverse proxy, e.g. 0.0.0.0:8080")
	open := flag.Bool("open", false, "open the default browser")
	flag.Parse()

	stats := model.HomeStats{BotAccounts: 12, RunningAccounts: 9, UserSendCount: 340, DeadAccounts: 2, SkippedAccounts: 1}
	var history []model.LogEntry
	now := func() string { return time.Now().UTC().Format(time.RFC3339) }

	srv := logwall.New(logwall.Options{
		Port:          *port,
		BackgroundDir: *bgDir,
		Stats:         func() (model.HomeStats, error) { return stats, nil },
		History: func(limit int) ([]model.LogEntry, error) {
			if len(history) > limit {
				return history[len(history)-limit:], nil
			}
			return history, nil
		},
	})
	u, err := srv.Start()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Live Wall demo:", u)
	srv.SetRunning(true)

	push := func(level, msg string) {
		e := model.LogEntry{Time: now(), Level: level, Message: msg}
		history = append(history, e)
		if len(history) > 400 {
			history = history[len(history)-400:]
		}
		srv.Publish(e)
	}
	push("info", "Wails UI ready.")
	push("activity", "bot started wait to process")
	push("info", "Starting 9 worker(s) across all discovered profiles.")

	go func() {
		bots := []string{"bot lily_rose (u1182)", "bot kaylaxo (u2211)", "bot emma.j (u9031)"}
		users := []string{"user jake_22 (501)", "user mike.d (777)", "user tyler_h (1042)", "user chris99 (88)", "user danny (3391)"}
		in := []string{"hey", "hi whats up", "u got snap?", "ur cute", "where u from", "asl?", "wyd rn", "send pic", "lol ok", "u there?", "nothing much hbu", "add me"}
		out := []string{"hey u", "nm just bored lol, u?", "f 22, u?", "haha ur funny", "maybe if ur nice", "im in texas, u", "ok ok my snap is snapqueen add me there", "lol what", "ya im here", "im not on here much, snap me: snapqueen", "ugh class in the morning", "tell me smth interesting"}
		for {
			b := bots[rand.Intn(len(bots))]
			us := users[rand.Intn(len(users))]
			time.Sleep(time.Duration(900+rand.Intn(2600)) * time.Millisecond)
			push("activity", fmt.Sprintf("RECEIVE | %s -> %s | message: %s", us, b, in[rand.Intn(len(in))]))
			time.Sleep(time.Duration(1200+rand.Intn(2500)) * time.Millisecond)
			reply := out[rand.Intn(len(out))]
			push("activity", fmt.Sprintf("SEND | %s -> %s | message: %s", b, us, reply))
			stats.MessagesSent++
			if rand.Intn(4) == 0 {
				stats.TrafficUsers++
			}
			if strings.Contains(reply, "snap") {
				stats.SnapTgSentCount++
			}
			if rand.Intn(25) == 0 {
				stats.DeadAccounts++
				push("error", fmt.Sprintf("[%s] account flagged as dead, worker stopped", b))
			}
			if rand.Intn(9) == 0 {
				push("info", fmt.Sprintf("[%s] polling inbox (%d new)", b, rand.Intn(3)))
			}
		}
	}()

	if *open {
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	}

	if *listen != "" {
		target, _ := url.Parse(u)
		proxy := httputil.NewSingleHostReverseProxy(target)
		director := proxy.Director
		proxy.Director = func(r *http.Request) {
			director(r)
			r.Host = target.Host // satisfy the loopback host guard
		}
		proxy.FlushInterval = -1 // stream SSE immediately
		fmt.Println("proxy:", *listen, "->", u)
		log.Fatal(http.ListenAndServe(*listen, proxy))
	}
	select {}
}
