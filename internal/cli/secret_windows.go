//go:build windows

package cli

import (
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
	fmt.Fprint(os.Stderr, prompt)
	c := exec.Command("powershell", "-NoProfile", "-Command", `$s=Read-Host -AsSecureString; $p=[Runtime.InteropServices.Marshal]::SecureStringToBSTR($s); try {[Runtime.InteropServices.Marshal]::PtrToStringBSTR($p)} finally {[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($p)}`)
	c.Stdin = os.Stdin
	b, err := c.Output()
	return strings.TrimSpace(string(b)), err
}
