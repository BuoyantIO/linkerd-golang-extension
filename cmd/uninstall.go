package main

import (
	"context"
	"fmt"
	"os"

	pkgCmd "github.com/linkerd/linkerd2/pkg/cmd"
	"github.com/linkerd/linkerd2/pkg/k8s"
	"github.com/spf13/cobra"
)

func newCmdUninstall() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Args:  cobra.NoArgs,
		Short: fmt.Sprintf("Output Kubernetes resources to uninstall the %s extension", extensionName),
		Long: fmt.Sprintf(`Output Kubernetes resources to uninstall the %s extension.

This command provides all Kubernetes namespace-scoped and cluster-scoped resources (e.g services, deployments, RBACs, etc.) necessary to uninstall the %s extension.`,
			extensionName, extensionName),
		Example: `linkerd uninstall | kubectl delete -f -`,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := uninstallRunE(cmd.Context())
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return nil
		},
	}

	return cmd
}

func uninstallRunE(ctx context.Context) error {
	k8sAPI, err := k8s.NewAPI(kubeconfigPath, kubeContext, impersonate, impersonateGroup, 0)
	if err != nil {
		return err
	}

	return pkgCmd.Uninstall(ctx, k8sAPI, fmt.Sprintf("%s=%s", k8s.LinkerdExtensionLabel, lcExtensionName))
}
