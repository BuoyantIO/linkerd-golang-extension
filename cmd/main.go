package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/fatih/color"
	pkgcmd "github.com/linkerd/linkerd2/pkg/cmd"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	_ "github.com/shurcooL/vfsgen"
)

const (
	extensionName           = "GAMMA"
	defaultLinkerdNamespace = "linkerd"
)

var (
	lcExtensionName           = strings.ToLower(extensionName)
	fullExtensionName         = fmt.Sprintf("linkerd-%s", lcExtensionName)
	defaultExtensionNamespace = fullExtensionName

	// special handling for Windows, on all other platforms these resolve to
	// os.Stdout and os.Stderr, thanks to https://github.com/mattn/go-colorable
	stdout = color.Output
	stderr = color.Error

	apiAddr               string // An empty value means "use the Kubernetes configuration"
	controlPlaneNamespace string
	kubeconfigPath        string
	kubeContext           string
	impersonate           string
	impersonateGroup      []string
	verbose               bool

	// These regexs are not as strict as they could be, but are a quick and dirty
	// sanity check against illegal characters.
	alphaNumDash = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
)

func main() {
	parser := &cobra.Command{
		Use: lcExtensionName,
		Short: fmt.Sprintf(
			"%s manages the %s extension of Linkerd service mesh",
			lcExtensionName, extensionName,
		),
		Long: fmt.Sprintf(
			"%s manages the %s extension of Linkerd service mesh",
			lcExtensionName, extensionName,
		),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// enable / disable logging
			if verbose {
				log.SetLevel(log.DebugLevel)
			} else {
				log.SetLevel(log.PanicLevel)
			}

			if !alphaNumDash.MatchString(controlPlaneNamespace) {
				return fmt.Errorf("%s is not a valid namespace", controlPlaneNamespace)
			}

			return nil
		},
	}

	parser.PersistentFlags().StringVarP(&controlPlaneNamespace, "linkerd-namespace", "L", defaultLinkerdNamespace, "Namespace in which Linkerd is installed")
	parser.PersistentFlags().StringVar(&kubeconfigPath, "kubeconfig", "", "Path to the kubeconfig file to use for CLI requests")
	parser.PersistentFlags().StringVar(&kubeContext, "context", "", "Name of the kubeconfig context to use")
	parser.PersistentFlags().StringVar(&impersonate, "as", "", "Username to impersonate for Kubernetes operations")
	parser.PersistentFlags().StringArrayVar(&impersonateGroup, "as-group", []string{}, "Group to impersonate for Kubernetes operations")
	parser.PersistentFlags().StringVar(&apiAddr, "api-addr", "", "Override kubeconfig and communicate directly with the control plane at host:port (mostly for testing)")
	parser.PersistentFlags().BoolVar(&verbose, "verbose", false, "Turn on debug logging")
	parser.AddCommand(newCmdInstall())
	parser.AddCommand(newCmdUninstall())
	parser.AddCommand(newCmdVersion())
	parser.AddCommand(newCmdCheck())

	// resource-aware completion flag configurations
	pkgcmd.ConfigureNamespaceFlagCompletion(
		parser, []string{"linkerd-namespace"},
		kubeconfigPath, impersonate, impersonateGroup, kubeContext,
	)

	pkgcmd.ConfigureKubeContextFlagCompletion(parser, kubeconfigPath)

	err := parser.Execute()

	if err != nil {
		os.Exit(1)
	}
}
