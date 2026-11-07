//go:build !windows

package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aeroknight786/trialm/internal/terminal"
)

func readSecret(prompt string) (string, error) {
	if selectedMode == Plain || selectedMode == JSON || !terminal.IsTTY(os.Stdin) {
		return "", needs("trialm key --paste in a terminal", "masked input needs a terminal")
	}
	get := exec.Command("stty", "-g")
	get.Stdin = os.Stdin
	old, err := get.Output()
	if err != nil {
		return "", err
	}
	set := func(s string) error { c := exec.Command("stty", s); c.Stdin = os.Stdin; return c.Run() }
	if err = set("-echo"); err != nil {
		return "", err
	}
	defer func() { _ = set(strings.TrimSpace(string(old))); fmt.Fprintln(os.Stderr) }()
	fmt.Fprint(os.Stderr, prompt)
	type input struct {
		value string
		err   error
	}
	done := make(chan input, 1)
	go func() { value, err := bufio.NewReader(os.Stdin).ReadString('\n'); done <- input{value, err} }()
	select {
	case line := <-done:
		return strings.TrimSpace(line.value), line.err
	case <-commandContext.Done():
		return "", commandContext.Err()
	}
}
