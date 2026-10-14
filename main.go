// Command clawdiator blocks the access to Anthropic and OpenAI when the computer is in an unsupported country.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"geocheck"
)

const appName = "clawdiator"

func main() {
	currentLanguage = detectLanguage()
	if len(os.Args) < 2 {
		fmt.Print(text(msgUsage))
		return
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "hosts":
		err = commandHosts(getHostsPath(), args, os.Stdout)
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
// The service writes the file. The command does not, until the real tests of the block are complete.
func commandHosts(hostsPath string, args []string, stdout io.Writer) error {
	flags := newFlagSet("hosts")
	isDryRun := flags.Bool("dry-run", false, text(msgFlagDryRun))
	providerNames := flags.String("providers", "anthropic,openai", text(msgFlagProviders))
	if err := flags.Parse(args); err != nil {
		return err
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
	return nil
}
