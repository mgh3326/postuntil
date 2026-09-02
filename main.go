package main

import (
	"os"

	"github.com/mgh3326/postuntil/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, os.Environ())) }
