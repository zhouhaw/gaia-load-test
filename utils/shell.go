package utils

import "os/exec"

func Exec_shell(s string) (string, error) {
	cmd := exec.Command("/bin/bash", "-c", s)
	output, err := cmd.Output()
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}
