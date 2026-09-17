//go:build !windows

package atomicfile

import (
	"log"
	"os"
)

func syncDirectory(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		log.Printf("directory durability check: %v", err)
		return
	}
	defer f.Close()
	if err = f.Sync(); err != nil {
		log.Printf("file saved; directory durability could not be confirmed: %v", err)
	}
}
