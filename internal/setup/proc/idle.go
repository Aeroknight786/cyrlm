package proc

import (
	"os"
	"os/exec"
)

// Timer detaches a self-contained idle-stop worker without waiting for health.
func Timer(o Options) error {
	log, err := os.OpenFile(o.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	c := exec.Command(o.Argv[0], o.Argv[1:]...)
	c.Env = append(os.Environ(), o.Env...)
	c.Stdout, c.Stderr = log, log
	DetachAttrs(c)
	if err = c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}
