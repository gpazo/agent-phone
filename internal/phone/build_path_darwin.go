package phone

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func canonicalExistingPath(path string) (string, bool, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	var buffer [1024]byte
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_GETPATH, uintptr(unsafe.Pointer(&buffer[0])))
	if errno != 0 {
		return "", false, errno
	}
	end := strings.IndexByte(string(buffer[:]), 0)
	if end < 0 {
		return "", false, fmt.Errorf("filesystem canonical path exceeds buffer")
	}
	const pcCaseSensitive = 11
	sensitive, err := syscall.Fpathconf(int(file.Fd()), pcCaseSensitive)
	if err != nil {
		return "", false, err
	}
	if sensitive != 0 && sensitive != 1 {
		return "", false, fmt.Errorf("filesystem case sensitivity is unknown for %q", path)
	}
	return string(buffer[:end]), sensitive == 1, nil
}
