# Changelog

This file lists the changes of each version of clawdiator. The newest version is first.
The text of each version is also on its release page on GitHub: https://github.com/keyCat/clawdiator/releases

## v0.1.1 - 2026-10-29

Before you install this version, read these 2 notes:

- v0.1.0 has no update command. To install v0.1.1, do the steps of the section "Install" in the README again. The settings stay.
- The consent text has a new item about the check for a new version. So the installation shows the consent text again. Type `yes` to agree.

### Fixed

- After a VPN started again, the checks still used old connections without the VPN, so the block stayed.
  Now each request to a location service opens a new connection.

### Added

- The service checks GitHub for a new version every 6 hours. It does not install a new version without your permission.
- When a new version is available, the tray icon shows a blue dot. A notice comes at once, at each login and every 24 hours.
- The tray menu has 3 new items:
  - "Install vX…" opens Terminal with `sudo clawdiator update`.
  - "Open the website…" opens https://ilya.zone/clawdiator/.
  - "Check for updates" asks the service to check now.
- The command `sudo clawdiator update` installs the newest version. The command `clawdiator update --check` asks the service to check now.
- Automatic updates are off by default. To turn them on, select "Install updates automatically" in the tray menu.
  The service never installs a version with a new consent text automatically.
- The tray menu shows the version. The logs `clawdiator.log` and `agent.log` show the version at each start.

### Changed

- The checks are faster:
  - During a pre-block, each request of a check has a time limit of 1.5 seconds, not 3 seconds. The probe has the same limit.
  - During a pre-block, a failed check gives new checks after 1, 2 and 5 seconds. Then the checks come every 10 seconds.
  - After about 3 minutes of failed checks during a pre-block, the checks come every 5 minutes.
  - Without a pre-block, a failed check gives a new check after 10 seconds, not 30 seconds.
  - The service accepts 1 command `clawdiator check` in 5 seconds, not in 30 seconds.
- The tray menu has the submenu "Log" with "Show the log" and "Show the verbose log".

### Removed

- The item "Open settings…" of the tray menu.

## v0.1.0 - 2026-10-20

The first release for macOS 12 Monterey and later.

### Added

- A service that finds the country of the public IP address. It asks Cloudflare, ipwho.is and geojs.io. It also reads a local database of DB-IP.
- A block of Anthropic and OpenAI in an unsupported country. The block uses the hosts file, and the firewall rules of pf for Anthropic.
- A pre-block at the start, at each change of the network and before each sleep.
- Notices about each block and each unblock, and an icon in the menu bar.
- The commands `install`, `uninstall`, `status`, `check`, `pause`, `resume`, `hosts` and `version`.
