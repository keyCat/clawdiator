// Command clawdiator blocks the access to Anthropic and OpenAI when the computer is in an unsupported country.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"clawdiator/internal/geocheck"
)

const appName = "clawdiator"

// version is the version of the build. The release build sets it with -ldflags "-X main.version=v1.2.3".
// It is a variable, because -X cannot change a constant. A build without the flag has the value "dev".
var version = "dev"

func main() {
	currentLanguage = detectLanguage()
	if len(os.Args) < 2 {
		fmt.Print(text(msgUsage))
		return
	}
	paths := getAppPaths()
	isRoot := os.Geteuid() == 0
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "install":
		err = newInstaller(paths).commandInstall(args)
	case "uninstall":
		err = newInstaller(paths).commandUninstall()
	case "update":
		err = newInstaller(paths).commandUpdate(args)
	case "update-info":
		// The service asks a new binary before the automatic update. The format must not change.
		fmt.Println(version, consentVersion)
		return
	case "status":
		err = commandStatus(paths, args, time.Now(), os.Stdout)
	case "check":
		err = commandCheck(paths, os.Stdout)
	case "pause":
		err = commandPause(paths, args, time.Now(), isRoot, os.Stdout)
	case "resume":
		err = commandResume(paths, isRoot, os.Stdout)
	case "hosts":
		err = commandHosts(paths.Hosts, args, os.Stdout, newInstaller(paths).commandRemoveBlock)
	case "service":
		err = commandService(paths)
	case "agent":
		err = commandAgent(paths, args)
	case "tray":
		err = commandTray(paths)
	case "version", "--version":
		fmt.Println(appName + " " + version)
		return
	case "help", "-h", "--help":
		fmt.Print(text(msgUsage))
		return
	default:
		fmt.Fprintf(os.Stderr, "%s\n\n%s", text(msgUnknownCommand, os.Args[1]), text(msgUsage))
		os.Exit(2)
	}
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, text(msgErrorPrefix), err)
		os.Exit(1)
	}
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), text(msgFlagsHeader, name))
		flags.PrintDefaults()
	}
	return flags
}

// commandHosts shows the change of the hosts file for a block of the providers. It only reads the hosts file.
// With --remove, it removes the clawdiator block without the service. This is the recovery after a wrong block.
func commandHosts(hostsPath string, args []string, stdout io.Writer, removeBlock func() error) error {
	flags := newFlagSet("hosts")
	isDryRun := flags.Bool("dry-run", false, text(msgFlagDryRun))
	isRemove := flags.Bool("remove", false, text(msgFlagRemove))
	providerNames := flags.String("providers", "anthropic,openai", text(msgFlagProviders))
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *isRemove {
		return removeBlock()
	}
	if !*isDryRun {
		return errorf(msgOnlyDryRun)
	}
	var providers []geocheck.Provider
	for _, name := range strings.Split(*providerNames, ",") {
		if strings.TrimSpace(name) == "" {
			continue
		}
		provider, err := geocheck.ParseProvider(name)
		if err != nil {
			return err
		}
		providers = append(providers, provider)
	}
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return err
	}
	hosts := getBlockedHosts(providers)
	updated, err := replaceHostsBlock(string(data), hosts)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, text(msgDryRunHeader))
	fmt.Fprintln(stdout, text(msgDryRunHostsFile, hostsPath, hostListDate))
	switch {
	case updated == string(data):
		fmt.Fprintln(stdout, text(msgDryRunNoChange))
		return nil
	case len(hosts) == 0:
		fmt.Fprintln(stdout, text(msgDryRunRemoveBlock))
	default:
		fmt.Fprintln(stdout, text(msgDryRunNewBlock))
		fmt.Fprintln(stdout)
		// Show the block of the new content, so that the output is the same as the real write.
		block := updated[strings.LastIndex(updated, hostsBeginMarker):]
		fmt.Fprintln(stdout, strings.ReplaceAll(block, "\r", ""))
	}
	fmt.Fprintln(stdout, text(msgDryRunFlush))
	for _, command := range getDNSFlushCommands(runtime.GOOS) {
		fmt.Fprintln(stdout, "  "+strings.Join(command, " "))
	}
	if newFirewall() != nil && slices.Contains(providers, geocheck.Anthropic) {
		fmt.Fprintln(stdout, text(msgDryRunFirewall, pfAnchor, anthropicRangesDate))
		fmt.Fprint(stdout, "  "+renderPFRules())
	}
	return nil
}
