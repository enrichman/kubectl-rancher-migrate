package cli

import (
	"github.com/enrichman/kubectl-rancher-migration/pkg/client"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

func NewRootCmd() (*cobra.Command, error) {
	rootCmd := &cobra.Command{
		Use:   "kubectl-rancher-migration",
		Short: "kubectl-rancher-migration",
		Long: `
A very simple cli.`,
		SilenceUsage: true,
	}

	config, err := genericclioptions.NewConfigFlags(true).ToRESTConfig()
	if err != nil {
		return nil, err
	}

	c, err := client.NewRancherClient(config)
	if err != nil {
		return nil, err
	}

	v1_10_0Cmd, err := NewV1_10_0_Cmd(c)
	if err != nil {
		return nil, err
	}

	rootCmd.AddCommand(
		v1_10_0Cmd,
	)

	return rootCmd, nil
}
