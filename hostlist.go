package main

import "clawdiator/internal/geocheck"

// hostListDate is the date of the snapshot of the host lists.
// The hosts file has no wildcards, so the lists must name each host. To update a list, change the hosts and the date.
const hostListDate = "2026-10-14"

// providerHosts holds all sites of each provider. The block includes the documentation,
// the status pages and the marketing sites, because the Anthropic IP rule blocks them anyway.
var providerHosts = map[geocheck.Provider][]string{
	geocheck.Anthropic: {
		// The API.
		"api.anthropic.com", "mcp-proxy.anthropic.com",
		// The login.
		"claude.ai", "claude.com", "www.claude.com", "platform.claude.com", "console.anthropic.com",
		// The apps, with their update and telemetry hosts.
		"www.claude.ai", "claudeusercontent.com", "bridge.claudeusercontent.com", "downloads.claude.ai", "statsig.anthropic.com",
		// The other sites.
		"anthropic.com", "www.anthropic.com", "docs.anthropic.com", "docs.claude.com", "code.claude.com",
		"support.claude.com", "status.anthropic.com",
	},
	geocheck.OpenAI: {
		// The API.
		"api.openai.com",
		// The login.
		"auth.openai.com", "auth0.openai.com", "platform.openai.com",
		// The apps, with their telemetry hosts.
		"chatgpt.com", "www.chatgpt.com", "chat.openai.com", "chat.com", "ab.chatgpt.com", "sora.com", "sora.chatgpt.com",
		"oaistatic.com", "cdn.oaistatic.com", "oaiusercontent.com", "files.oaiusercontent.com",
		// The other sites.
		"openai.com", "www.openai.com", "cdn.openai.com", "help.openai.com", "status.openai.com",
		"developers.openai.com", "community.openai.com",
	},
}

// getBlockedHosts returns the hosts of the providers, in the order of geocheck.Providers.
func getBlockedHosts(providers []geocheck.Provider) []string {
	var hosts []string
	for _, provider := range geocheck.Providers() {
		for _, blocked := range providers {
			if blocked == provider {
				hosts = append(hosts, providerHosts[provider]...)
			}
		}
	}
	return hosts
}
