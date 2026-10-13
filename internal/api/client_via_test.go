package api

import (
	"context"
	"testing"

	"chitchatbot/internal/model"
)

type fakeExec struct{ got FetchRequest }

func (f *fakeExec) Fetch(_ context.Context, req FetchRequest) (FetchResponse, error) {
	f.got = req
	return FetchResponse{OK: true, Status: 200, Text: "via"}, nil
}

func TestClientViaExecutor(t *testing.T) {
	exec := &fakeExec{}
	// no token on purpose: the Go path would fail, the via path must not care
	c := NewVia(exec, model.LoginInfo{}, "https://app.chitchat.gg/")
	if !c.Via() {
		t.Fatal("Via() must be true")
	}
	body := `{"a":1}`
	resp, err := c.Fetch(context.Background(), FetchRequest{Method: "POST", URL: "https://api.chitchat.gg/match", Body: &body})
	if err != nil || resp.Text != "via" || resp.Status != 200 {
		t.Fatalf("via not used: %v %+v", err, resp)
	}
	if exec.got.URL != "https://api.chitchat.gg/match" || exec.got.Body == nil || *exec.got.Body != body {
		t.Fatalf("request not forwarded: %+v", exec.got)
	}
	plain := New(model.LoginInfo{}, "https://app.chitchat.gg/", nil)
	if plain.Via() {
		t.Fatal("plain client must not report Via")
	}
	if _, err := plain.Fetch(context.Background(), FetchRequest{Method: "GET", URL: "https://x"}); err == nil {
		t.Fatal("plain client without token must fail as before")
	}
}
