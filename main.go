package main

import (
	"fmt"
	"os"

	cli "github.com/enrichman/kubectl-rancher_migrate/pkg/cli"
)

func main() {
	rootCmd, err := cli.NewRootCmd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
