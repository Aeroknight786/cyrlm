//go:build windows

package cli

import (
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
	fmt.Fprint(os.Stderr, prompt)
	c := exec.CommandContext(commandContext, "powershell", "-NoProfile", "-Command", `$s=Read-Host -AsSecureString; $p=[Runtime.InteropServices.Marshal]::SecureStringToBSTR($s); try {[Runtime.InteropServices.Marshal]::PtrToStringBSTR($p)} finally {[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($p)}`)
	c.Stdin = os.Stdin
	b, err := c.Output()
	return strings.TrimSpace(string(b)), err
}
