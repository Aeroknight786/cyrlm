package setup

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestOccupiedPortIsInsideApproval(t *testing.T) {
	e := testExecutor(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	health := "http://" + listener.Addr().String() + "/health"
	step := Step{ID: "server", Kind: Start, Health: health}
	p := basicPlan(step)
	j := Journal{Plan: p}
	if err = e.pickPort(context.Background(), &j, step, ""); err != nil {
		t.Fatal(err)
	}
	if j.effective(health) == health || j.Plan.ID != p.ID {
		t.Fatal(j)
	}
	oldPort := strings.Split(listener.Addr().String(), ":")[1]
	argv := j.effectiveArgs([]string{"llama-server", "--port", oldPort})
	if argv[2] == oldPort {
		t.Fatal(argv)
	}
}
