//go:build !unix

package exec

import osexec "os/exec"

func configureProcess(cmd *osexec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Kill() }
}

func freeBytes(string) (uint64, error) { return 0, errNoDiskInfo }
