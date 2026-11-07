package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/upstream"
)

func TestChatConversation(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c := newChat(context.Background(), &upstream.OpenAICompat{BaseURL: srv.URL, Model: "fake"})
	c.input.SetValue("hi")
	cmd := c.update(key("enter"))
	if cmd == nil || !c.busy {
		t.Fatal("no request")
	}
	v := cmd().(chatResponseMsg)
	c.receive(v)
	if v.err != nil || len(c.messages) != 2 || c.messages[1].Content != "hello" || calls != 1 {
		t.Fatal(c.messages, v, calls)
	}
	if !strings.Contains(c.view(), "hello") {
		t.Fatal(c.view())
	}
}

func TestChatFailureKeepsRawDetailsHidden(t *testing.T) {
	m := fixture(t)
	m.stack = []Screen{Chat}
	m.chat = newChat(context.Background(), nil)
	m.Update(chatResponseMsg{err: fmt.Errorf("fixture raw chat failure")})
	if strings.Contains(m.View().Content, "fixture raw chat failure") || !strings.Contains(m.View().Content, "The reply stopped") {
		t.Fatal(m.View().Content)
	}
	m.Update(key("d"))
	if !strings.Contains(m.View().Content, "fixture raw chat failure") {
		t.Fatal(m.View().Content)
	}
}
