package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/linkerd/linkerd2/pkg/healthcheck"
	"github.com/spf13/cobra"
)

var (
	gammaNamespace string

	// linkerdGammaExtensionCheck adds checks related to the Gamma extension
	linkerdGammaExtensionCheck healthcheck.CategoryID = healthcheck.CategoryID(fullExtensionName)
)

type checkOptions struct {
	wait      time.Duration
	output    string
	proxy     bool
	namespace string
	pre       string
}

func gammaCategory(hc *healthcheck.HealthChecker) *healthcheck.Category {

	checkers := []healthcheck.Checker{}

	checkers = append(checkers,
		*healthcheck.NewChecker(fmt.Sprintf("%s extension Namespace exists", extensionName)).
			WithHintAnchor("l5d-gamma-ns-exists").
			Fatal().
			WithCheck(func(ctx context.Context) error {
				// Get extension namespace
				ns, err := hc.KubeAPIClient().GetNamespaceWithExtensionLabel(ctx, lcExtensionName)
				if err != nil {
					return err
				}
				gammaNamespace = ns.Name
				return nil
			}))

	checkers = append(checkers,
		*healthcheck.NewChecker(fmt.Sprintf("%s extension service account exists", extensionName)).
			WithHintAnchor("l5d-gamma-sc-exists").
			Fatal().
			Warning().
			WithCheck(func(ctx context.Context) error {
				// Check for Collector Service Account
				return healthcheck.CheckServiceAccounts(ctx, hc.KubeAPIClient(), []string{"gamma-adaptor"}, gammaNamespace, "")
			}))

	checkers = append(checkers,
		*healthcheck.NewChecker(fmt.Sprintf("%s extension pods are injected", extensionName)).
			WithHintAnchor("l5d-gamma-pods-injection").
			Warning().
			WithCheck(func(ctx context.Context) error {
				// Check if extension pods have been injected
				pods, err := hc.KubeAPIClient().GetPodsByNamespace(ctx, gammaNamespace)
				if err != nil {
					return err
				}
				return healthcheck.CheckIfDataPlanePodsExist(pods)
			}))

	checkers = append(checkers,
		*healthcheck.NewChecker(fmt.Sprintf("%s extension pods are running", extensionName)).
			WithHintAnchor("l5d-gamma-pods-running").
			Fatal().
			WithRetryDeadline(hc.RetryDeadline).
			SurfaceErrorOnRetry().
			WithCheck(func(ctx context.Context) error {
				pods, err := hc.KubeAPIClient().GetPodsByNamespace(ctx, gammaNamespace)
				if err != nil {
					return err
				}

				// Check for relevant pods to be present
				err = healthcheck.CheckForPods(pods, []string{"gamma-adaptor"})
				if err != nil {
					return err
				}

				return healthcheck.CheckPodsRunning(pods, "")

			}))

	checkers = append(checkers,
		*healthcheck.NewChecker(fmt.Sprintf("%s extension proxies are healthy", extensionName)).
			WithHintAnchor("l5d-gamma-proxy-healthy").
			Fatal().
			WithRetryDeadline(hc.RetryDeadline).
			SurfaceErrorOnRetry().
			WithCheck(func(ctx context.Context) error {
				return hc.CheckProxyHealth(ctx, hc.ControlPlaneNamespace, gammaNamespace)
			}))

	return healthcheck.NewCategory(linkerdGammaExtensionCheck, checkers, true)
}

func newCheckOptions() *checkOptions {
	return &checkOptions{
		wait:   300 * time.Second,
		output: healthcheck.TableOutput,
	}
}

func (options *checkOptions) validate() error {
	if options.output != healthcheck.TableOutput && options.output != healthcheck.JSONOutput {
		return fmt.Errorf("invalid output type '%s'. Supported output types are: %s, %s", options.output, healthcheck.JSONOutput, healthcheck.TableOutput)
	}
	return nil
}

// newCmdCheck generates a new cobra command for the Gamma extension.
func newCmdCheck() *cobra.Command {
	options := newCheckOptions()
	cmd := &cobra.Command{
		Use:   "check [flags]",
		Args:  cobra.NoArgs,
		Short: fmt.Sprintf("Check the %s extension for potential problems", extensionName),
		Long: fmt.Sprintf(`Check the %s extension for potential problems.

The check command will perform a series of checks to validate that the Gamma
extension is configured correctly. If the command encounters a failure it will
print additional information about the failure and exit with a non-zero exit
code.`, extensionName),
		Example: `  # Check that the Gamma extension is up and running
  linkerd gamma check`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return configureAndRunChecks(stdout, stderr, options)
		},
	}

	cmd.Flags().StringVarP(&options.output, "output", "o", options.output, "Output format. One of: basic, json")
	cmd.Flags().DurationVar(&options.wait, "wait", options.wait, "Maximum allowed time for all tests to pass")
	cmd.Flags().BoolVar(&options.proxy, "proxy", options.proxy, "Also run data-plane checks, to determine if the data plane is healthy")
	cmd.Flags().StringVarP(&options.namespace, "namespace", "n", options.namespace, "Namespace to use for --proxy checks (default: all namespaces)")
	cmd.Flags().StringVar(&options.pre, "pre", options.namespace, "Only run pre-installation checks, to determine if the extension can be installed")

	// stop marking these flags as hidden, once they are being supported
	cmd.Flags().MarkHidden("pre")
	cmd.Flags().MarkHidden("proxy")
	return cmd
}

func configureAndRunChecks(wout io.Writer, werr io.Writer, options *checkOptions) error {
	err := options.validate()
	if err != nil {
		return fmt.Errorf("Validation error when executing check command: %v", err)
	}

	hc := healthcheck.NewHealthChecker([]healthcheck.CategoryID{}, &healthcheck.Options{
		ControlPlaneNamespace: controlPlaneNamespace,
		KubeConfig:            kubeconfigPath,
		KubeContext:           kubeContext,
		Impersonate:           impersonate,
		ImpersonateGroup:      impersonateGroup,
		APIAddr:               apiAddr,
		RetryDeadline:         time.Now().Add(options.wait),
		DataPlaneNamespace:    options.namespace,
	})

	err = hc.InitializeKubeAPIClient()
	if err != nil {
		err = fmt.Errorf("Error initializing k8s API client: %s", err)
		fmt.Fprintln(werr, err)
		os.Exit(1)
	}

	hc.AppendCategories(gammaCategory(hc))

	success, _ := healthcheck.RunChecks(wout, werr, hc, options.output)

	if !success {
		os.Exit(1)
	}

	return nil
}
