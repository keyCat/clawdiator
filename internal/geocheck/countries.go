package geocheck

import "strings"

// ListDate is the date of the snapshot of the official lists.
// Anthropic: https://www.anthropic.com/supported-countries
// OpenAI: https://platform.openai.com/docs/supported-countries
// A country that is not in the list of a tool is not supported for that tool.
// To update a list, replace the codes and change the date.
const ListDate = "2026-10-14"

// supportedCountries holds the ISO 3166-1 alpha-2 codes from the official lists.
var supportedCountries = map[Provider]map[string]bool{
	Anthropic: toCodeSet(
		"AD AE AG AL AM AO AR AT AU AZ BA BB BD BE BF BG BH BI BJ BN BO BR BS BT BW BZ CA CD CF CG CH CI " +
			"CL CM CO CR CV CY CZ DE DJ DK DM DO DZ EC EE EG ER ES ET FI FJ FM FR GA GB GD GE GH GM GN GQ GR " +
			"GT GW GY HN HR HT HU ID IE IL IN IQ IS IT JM JO JP KE KG KH KI KM KN KR KW KZ LA LB LC LI LK LR " +
			"LS LT LU LV LY MA MC MD ME MG MH MK ML MN MR MT MU MV MW MX MY MZ NA NE NG NI NL NO NP NR NZ OM " +
			"PA PE PG PH PK PL PS PT PW PY QA RO RS RW SA SB SC SD SE SG SI SK SL SM SN SO SR SS ST SV SZ TD " +
			"TG TH TJ TL TM TN TO TR TT TV TW TZ UA UG US UY UZ VA VC VN VU WS ZA ZM ZW",
	),
	OpenAI: toCodeSet(
		"AD AE AF AG AL AM AO AR AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BR BS BT BW BZ CA " +
			"CD CF CG CH CI CL CM CO CR CV CY CZ DE DJ DK DM DO DZ EC EE EG ER ES ET FI FJ FM FO FR GA GB GD " +
			"GE GF GH GL GM GN GP GQ GR GT GW GY HN HR HT HU ID IE IL IN IQ IS IT JM JO JP KE KG KH KI KM KN " +
			"KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MQ MR MT MU MV MW " +
			"MX MY MZ NA NC NE NG NI NL NO NP NR NZ OM PA PE PF PG PH PK PL PM PS PT PW PY QA RE RO RS RW SA " +
			"SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SZ TD TF TG TH TJ TL TM TN TO TR TT TV TW TZ " +
			"UA UG US UY UZ VA VC VN VU WF WS YE YT ZA ZM ZW",
	),
}

// territoryOwners maps a territory to its country.
// Anthropic also supports the territories and dependencies of a listed country.
// The OpenAI list names its territories, so this table is only for Anthropic.
var territoryOwners = map[string]string{
	"PR": "US", "GU": "US", "VI": "US", "AS": "US", "MP": "US", "UM": "US",
	"GI": "GB", "BM": "GB", "KY": "GB", "VG": "GB", "TC": "GB", "MS": "GB", "AI": "GB", "FK": "GB",
	"SH": "GB", "IO": "GB", "GS": "GB", "PN": "GB", "IM": "GB", "JE": "GB", "GG": "GB",
	"GF": "FR", "GP": "FR", "MQ": "FR", "RE": "FR", "YT": "FR", "PM": "FR", "BL": "FR", "MF": "FR",
	"NC": "FR", "PF": "FR", "WF": "FR", "TF": "FR",
	"AW": "NL", "CW": "NL", "SX": "NL", "BQ": "NL",
	"GL": "DK", "FO": "DK",
	"SJ": "NO", "BV": "NO",
	"CX": "AU", "CC": "AU", "NF": "AU", "HM": "AU",
	"CK": "NZ", "NU": "NZ", "TK": "NZ",
	"AX": "FI",
}

// territoryProviders holds the providers that use territoryOwners.
var territoryProviders = map[Provider]bool{Anthropic: true}

// excludedRegions holds the parts of the region names that are not supported in a supported country.
// Anthropic excludes the Crimea, Donetsk, Kherson, Luhansk and Zaporizhzhia regions of Ukraine.
// OpenAI writes "Ukraine (with certain exceptions)" and does not name them, so the tool uses the same regions.
// The location services write the names in different ways, so the tool compares parts of the names.
var excludedRegions = map[string][]string{
	"UA": {
		"crimea", "krym", "sevastopol", "donetsk", "kherson", "luhansk", "lugansk", "zaporiz", "zaporozh",
		// Some services give the names in Russian or in Ukrainian.
		// The Ukrainian adjective "Запорізька" has "з", not "ж", so it needs its own part.
		"крым", "крим", "севастопол", "донецк", "донецьк", "херсон", "луганск", "луганськ", "запорож", "запоріж", "запоріз",
	},
}

func toCodeSet(codes string) map[string]bool {
	set := map[string]bool{}
	for _, code := range strings.Fields(codes) {
		set[code] = true
	}
	return set
}
