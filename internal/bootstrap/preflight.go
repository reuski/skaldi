package bootstrap

import (
	"fmt"
	"os/exec"
)

func CheckPrerequisites() error {
	for _, tool := range []string{"mpv", "ffmpeg"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("required tool '%s' not found in PATH", tool)
		}
	}
	return nil
}
