package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCloseBoundsAnOpenEditorRequest(t *testing.T) {
	s := New(Config{})
	entered := make(chan struct{})
	ended := make(chan struct{})
	s.mux.HandleFunc("/hold", func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(ended) })
	port, err := s.Listen("127.0.0.1", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve() }()
	request := make(chan struct{})
	go func() {
		r, _ := http.Get(fmt.Sprintf("http://127.0.0.1:%d/hold", port))
		if r != nil {
			r.Body.Close()
		}
		close(request)
	}()
	<-entered
	started := time.Now()
	_ = s.Close()
	if time.Since(started) > 3*time.Second {
		t.Fatal("unbounded bridge shutdown")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("editor request was not cancelled")
	}
	<-request
}
