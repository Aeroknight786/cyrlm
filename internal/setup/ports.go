package setup

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/aeroknight786/trialm/internal/setup/proc"
)

// Effective addresses belong to the journal. The approved argv stays intact.
func (j *Journal) effective(value string) string {
	for before, after := range j.Addresses {
		value = strings.ReplaceAll(value, before, after)
	}
	return value
}
func (j *Journal) effectiveArgs(argv []string) []string {
	out := append([]string(nil), argv...)
	for i := range out {
		out[i] = j.effective(out[i])
		if i > 0 && (out[i-1] == "--port" || out[i-1] == "-p") {
			for before, after := range j.Addresses {
				oldURL, _ := url.Parse(before)
				newURL, _ := url.Parse(after)
				if out[i] == oldURL.Port() {
					out[i] = newURL.Port()
				}
			}
		}
	}
	return out
}
func (e *Executor) pickPort(ctx context.Context, j *Journal, s Step, pidFile string) error {
	if _, alive := proc.Status(ctx, pidFile); alive {
		return nil
	}
	effective := j.effective(s.Health)
	u, err := url.Parse(effective)
	if err != nil || u.Hostname() != "127.0.0.1" {
		return fmt.Errorf("start health must use 127.0.0.1: %s", effective)
	}
	listener, err := net.Listen("tcp", u.Host)
	if err == nil {
		return listener.Close()
	}
	listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		return err
	}
	original, _ := url.Parse(s.Health)
	oldBase := original.Scheme + "://" + original.Host
	newBase := u.Scheme + "://127.0.0.1:" + strconv.Itoa(port)
	if j.Addresses == nil {
		j.Addresses = map[string]string{}
	}
	j.Addresses[oldBase] = newBase
	e.emit("setup_log", Update{Kind: Start, Title: s.Title, Lines: []string{fmt.Sprintf("Port %s is in use. Using %d within this approved plan.", original.Port(), port)}})
	return nil
}
