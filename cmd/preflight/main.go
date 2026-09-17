package main

import (
	"aioverlay/internal/releaseguard"
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".", "project root")
	cfg := flag.String("config", "", "public release template")
	runtimes := flag.Bool("runtimes", false, "verify LFS binaries against pinned checksums")
	staged := flag.Bool("staged", false, "check staged config and local artifacts")
	flag.Parse()
	var err error
	if *cfg != "" {
		err = releaseguard.SafeConfig(*cfg)
	}
	if err == nil && *staged {
		err = releaseguard.Staged(*root)
	}
	if err == nil && *runtimes {
		err = releaseguard.Runtimes(*root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Preflight:", err)
		os.Exit(1)
	}
	fmt.Println("Preflight PASS")
}
