package cli

import (
	"github.com/enrichman/kubectl-rancher_migrate/pkg/client"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

func NewRootCmd() (*cobra.Command, error) {
	rootCmd := &cobra.Command{
		Use:          "kubectl-rancher_migrate",
		Short:        "kubectl-rancher_migrate",
		Long:         `Rancher migration tool.`,
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
