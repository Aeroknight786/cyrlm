//go:build !windows

package cli

import (
	"bufio"
	"fmt"
	"github.com/aeroknight786/trialm/internal/terminal"
	"os"
	"os/exec"
	"strings"
)

func readSecret(prompt string) (string, error) {
	if !terminal.IsTTY(os.Stdin) {
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
	value, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}
