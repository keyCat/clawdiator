# clawdiator

`clawdiator` blocks the access to Anthropic and OpenAI on a computer when the computer is in an unsupported country.
Each provider has a list of supported countries. A use from another country can get an account blocked.
The tool is for teams that agree to this block. It asks for the consent of the user before the installation.

The tool works on macOS now. Linux and Windows come in a later version.

## How the tool works

A service runs in the background as root. It finds the country of the public IP address and applies the lists of the providers.

- Every 30 seconds, the service sends 1 request to the Cloudflare trace. The reply gives the public IP address and the country.
- After each change of the network and every 60 minutes, it does a full check. The full check asks 3 location services and a local database of DB-IP.
- The service allows a provider only if each reply gives an allowed country. If the check fails, the service blocks.
- The block has 2 parts:
  - Entries in the hosts file for the sites of Anthropic and OpenAI.
  - Firewall rules of pf for the address ranges of Anthropic.
- The service blocks both providers at the start, at each change of the network and before each sleep. This pre-block stays until a full check passes.
- A notice tells you about each block and each unblock. An icon in the menu bar shows the state.

## Requirements

- macOS 12 Monterey or later. The tests ran on macOS 26 and macOS 27. Read "Test status".
- Administrator rights for the installation, the pause and the uninstallation.
- A Mac with Apple silicon uses the archive `darwin-arm64`. A Mac with an Intel processor uses the archive `darwin-amd64`.
  To see the type of your Mac, run `uname -m`. The result `arm64` means Apple silicon. The result `x86_64` means Intel.

## Install

### 1. Download the archive

Download the archive for your Mac and the checksum file. Change `arm64` to `amd64` for a Mac with an Intel processor.

```sh
curl -LO https://github.com/keyCat/clawdiator/releases/latest/download/clawdiator-darwin-arm64.tar.gz
curl -LO https://github.com/keyCat/clawdiator/releases/latest/download/SHA256SUMS
```

### 2. Check the archive

```sh
grep clawdiator-darwin-arm64.tar.gz SHA256SUMS | shasum -a 256 -c
```

Expected result: `clawdiator-darwin-arm64.tar.gz: OK`. If you see a different result, do not install the tool.

### 3. Unpack the archive

```sh
mkdir clawdiator
tar -xzf clawdiator-darwin-arm64.tar.gz -C clawdiator
cd clawdiator
```

The archive contains 2 binaries: `clawdiator` and `clawdiator-tray`. Keep them in the same folder. `install` copies both of them.

### 4. The binaries and Gatekeeper

The binaries have no signature of Apple. A download with `curl` gets no quarantine mark, so macOS starts the binaries.
A download with a browser gets the quarantine mark. Then macOS does not start the binaries. In that case, remove the mark:

```sh
xattr -d com.apple.quarantine clawdiator clawdiator-tray
```

These 2 cases are still to confirm in a test of the first release.

### 5. Install the service

```sh
sudo ./clawdiator install
```

The tool shows the consent text. Read it. Then type `yes`. The `install` command does these steps:

1. It copies the 2 binaries to `/Library/Application Support/clawdiator/bin`.
2. It writes the settings to `/Library/Application Support/clawdiator/config.json`.
3. It downloads the local country database from db-ip.com. If the download fails, the service tries again later.
4. It registers and starts the service. The service is the LaunchDaemon `local.clawdiator`.
5. It starts the notice agent and the tray icon in your session. Other users get them at their next login.
6. It makes the link `/usr/local/bin/clawdiator`, so you can run `clawdiator` from any folder.

To change a setting, run `install` again with the option. The other settings stay.

| Option | Default | Description |
|---|---|---|
| `--blocked-countries` | none | More country codes to block for both providers, separated by commas, for example `AF`. An empty value removes them. |
| `--geo-services` | `cloudflare,ipwho.is,geojs.io` | The location services of the full check, separated by commas. |
| `--geo-min-replies` | `2` | The minimum number of correct replies of the location services. |
| `--yes` | off | Agree to the consent text without the question. |

## Use

| Command | Action |
|---|---|
| `clawdiator status` | Shows the state of the service, the country, the block of each provider and the last lines of the log. `--verbose` shows the verbose log. |
| `clawdiator check` | Asks the service for a full check now. The service accepts 1 check in 5 seconds. |
| `sudo clawdiator pause --for 2h` | Stops the block for a time, for example `30m`, `4h` or `3d`. `--forever` gives a pause with no end. |
| `sudo clawdiator resume` | Stops the pause. Then the service checks the country again. |
| `sudo clawdiator update` | Installs the newest version from GitHub. |
| `clawdiator update --check` | Asks the service to check for a new version now. The command needs no sudo. |
| `sudo clawdiator update --auto-install on` | Turns the automatic updates on. Use `off` to turn them off. |
| `clawdiator version` | Shows the version. |
| `clawdiator help` | Shows the help. |

The menu of the tray icon also has "Check now", "Pause", the setting of the notices after a wake, and "Log".
"Log" opens the log or the verbose log in the Console app. The last line of the menu shows the version.
"Hide the icon" removes the icon until the next login. The notices continue. `clawdiator agent --show` shows the icon again.

The text of the tool is in English or in Russian, the same as the language of macOS. To select a language, set `CLAWDIATOR_LANG` to `en` or `ru`.
The log is always in English.

### Logs

The service writes 2 logs in `/Library/Application Support/clawdiator`:

- `clawdiator.log` has 1 line for each block, unblock, pre-block, pause and error.
- `clawdiator-debug.log` has the details of each step, with the time in milliseconds. `clawdiator status --verbose` shows its end.

CAUTION: The verbose log contains the public IP address of the computer. Do not publish a copy of the log.

## Updates

The service checks GitHub for a new version 5 minutes after its start, and then every 6 hours.
It does not install a new version without your permission.
The changes of each version are in the changelog: https://github.com/keyCat/clawdiator/blob/main/CHANGELOG.md
They are also on the release page: https://github.com/keyCat/clawdiator/releases

When a new version is available, you see these signs:

- A blue dot on the tray icon.
- A notice at once, at each login, and every 24 hours until you install the version.
- The items "Install vX…" and "Open the website…" in the tray menu. The website is https://ilya.zone/clawdiator/.
- A line in `clawdiator status`.

To install the new version, select "Install vX…" in the tray menu. A Terminal window opens and runs this command:

```sh
sudo clawdiator update
```

Type your password. The command downloads the version from GitHub and checks it with `SHA256SUMS`. Then it installs the version, and the settings stay.
If the consent text changed, the command shows the new text. Type `yes` to agree. After the update, a notice shows the new version.

The checksum finds a damaged download. It does not find a changed release, because `SHA256SUMS` comes from the same GitHub release.

To check now, select "Check for updates" in the tray menu, or run `clawdiator update --check`. The service accepts 1 such request in 5 seconds.
A notice or the command then shows the result.

### Automatic updates

CAUTION: If an automatic install fails after it stops the old service, the Mac has no service and no block.
Then run `sudo clawdiator update`. The error is in `/Library/Application Support/clawdiator/update.log`.

The automatic updates are off by default. To turn them on, select "Install updates automatically" in the tray menu. Then type your password.
You can also use this command. Use `off` to turn them off again.

```sh
sudo clawdiator update --auto-install on
```

With the automatic updates on, the service downloads and checks each new version. Then it installs the version without a question.
It never installs a version with a new consent text automatically. Such a version waits for you, the same as with the automatic updates off.
The output of the last automatic update is in `/Library/Application Support/clawdiator/update.log`.

A local build with the version `dev` does not check for updates.

## Uninstall

```sh
sudo clawdiator uninstall
```

The command stops the service, the notice agent and the tray icon. It removes the block from the hosts file and the firewall rules.
It also removes the app folder, the link and the files of the notices in the home folder of each user. It works without the network.

If a block stays and the service is gone, remove the block with this command:

```sh
sudo clawdiator hosts --remove
```

If the tool does not work at all, remove the firewall rules with this command.
Then edit `/private/etc/hosts` as root. Remove the line that starts with `# BEGIN clawdiator`, the line `# END clawdiator` and all lines between them.

```sh
sudo pfctl -a com.apple/clawdiator -F rules
```

## Privacy

The service sends the public IP address of the computer to these services, and to no other place:

- Cloudflare: 1 request every 30 seconds.
- ipwho.is and geojs.io: 1 request for each full check.
- db-ip.com: 1 download of the country database each month.
- github.com: 1 request every 6 hours for the newest version. The tool also downloads from github.com each new version that it installs.

The service sends no other data. It reads no traffic of the computer.

The local country database: IP Geolocation by DB-IP (https://db-ip.com), license CC BY 4.0.

## Limits

The tool is a safety net. It does not guarantee that the use follows the policies of Anthropic and OpenAI.

- With a VPN, the check sees the country of the VPN exit, not the physical location.
- A proxy, iCloud Private Relay, a virtual machine or a Docker container can go past the block.
- An app that goes past the hosts file waits for a timeout instead of an error. The firewall rules stop such an app only for Anthropic, because OpenAI uses shared addresses of Cloudflare.

## Build from the source

You need Go 1.26 and the Xcode command line tools. The tray binary uses cgo.
Use Go 1.26, because a binary from Go 1.27 does not start on macOS 12.

```sh
CGO_ENABLED=0 go build -o dist/clawdiator .
CGO_ENABLED=1 go build -tags tray -o dist/clawdiator-tray .
```

To build the release archives with the checks of a release, run this command. The archives go to `dist/release`.

```sh
scripts/build-release.sh v0.1.0
```

Before you push a tag, add the section of the version to `CHANGELOG.md`.
The workflow "Release" uses this section as the text of the release. Without the section, the workflow stops.
To see the text, run `scripts/release-notes.sh v0.1.0`.

To run the tests, run `go test ./...`. The tests do not change the system and send no requests to the internet.
To also send real requests to the location services, set `CLAWDIATOR_NETWORK_TEST=1`.

## License

MIT. Read [LICENSE](LICENSE). Each archive also contains `THIRD_PARTY_LICENSES.txt` with the licenses of Go and of the modules in the binaries.

## Test status

- macOS on a Mac with Apple silicon: tested from 2026-10-14 to 2026-10-20, with macOS 26 and macOS 27.
  The tests covered the service, the block, the pause, the notices and the tray icon.
  They also covered the firewall rules, sleep and wake, and the uninstallation.
- macOS on a Mac with an Intel processor: not tested.
- macOS 12 to macOS 25: not tested. Go 1.26 supports macOS 12.
- Linux and Windows: not ready.
