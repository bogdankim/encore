package catalog

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DetectCountry guesses the Apple Music storefront as an ISO alpha-2 code
// ("ca"). In order: the storefront Music's account uses, from the
// com.apple.itunescloud defaults; the region of the macOS locale; $LANG;
// "us". All `defaults` calls together get 1 s.
func DetectCountry() string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return detectCountry(ctx, runDefaults, os.Getenv("LANG"))
}

func runDefaults(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "/usr/bin/defaults", args...).Output()
	return string(out), err
}

// Some systems keep storefrontID at the top of the domain; others only
// inside the cached subscription status, keyed by a JSON blob. Both are
// read as single keys, and everything but the storefront id is dropped.
var (
	storefrontKeys = [][]string{
		{"read", "com.apple.itunescloud", "storefrontID"},
		{"read", "com.apple.itunescloud", "ICDefaultsKeyLastKnownSubscriptionStatusBaseCacheKey"},
	}
	storefrontRe = regexp.MustCompile(`storefrontID\W{0,8}(\d{6})`)
)

func detectCountry(ctx context.Context, run func(context.Context, ...string) (string, error), lang string) string {
	for i, args := range storefrontKeys {
		out, err := run(ctx, args...)
		if err != nil {
			continue
		}
		id := ""
		if i == 0 {
			id = leadingDigits(strings.TrimSpace(out)) // "143455" or "143455-6,32"
		} else if m := storefrontRe.FindStringSubmatch(out); m != nil {
			id = m[1]
		}
		if n, err := strconv.Atoi(id); err == nil {
			if cc, ok := storefronts[n]; ok {
				return cc
			}
		}
	}
	if out, err := run(ctx, "read", "-g", "AppleLocale"); err == nil {
		if cc := localeRegion(strings.TrimSpace(out)); cc != "" {
			return cc
		}
	}
	if cc := localeRegion(lang); cc != "" {
		return cc
	}
	return "us"
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// localeRegion returns the lowercase region of a locale id: "en_CA" → "ca",
// "zh-Hans_TW" → "tw", "en_US@rg=gbzzzz" → "gb" (the region override wins),
// "en_GB.UTF-8" → "gb". It returns "" when there is no region.
func localeRegion(s string) string {
	if i := strings.Index(s, "rg="); i >= 0 && len(s) >= i+5 {
		if cc := strings.ToLower(s[i+3 : i+5]); isAlpha2(cc) {
			return cc
		}
	}
	s, _, _ = strings.Cut(s, "@")
	s, _, _ = strings.Cut(s, ".")
	i := strings.LastIndexAny(s, "_-")
	if i < 0 {
		return ""
	}
	cc := strings.ToLower(s[i+1:])
	if !isAlpha2(cc) {
		return ""
	}
	return cc
}

func isAlpha2(s string) bool {
	return len(s) == 2 && s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z'
}

// storefronts maps Apple storefront ids to ISO alpha-2 country codes, from
// the list Apple published for iTunes affiliate links.
var storefronts = map[int]string{
	143441: "us", 143442: "fr", 143443: "de", 143444: "gb", 143445: "at",
	143446: "be", 143447: "fi", 143448: "gr", 143449: "ie", 143450: "it",
	143451: "lu", 143452: "nl", 143453: "pt", 143454: "es", 143455: "ca",
	143456: "se", 143457: "no", 143458: "dk", 143459: "ch", 143460: "au",
	143461: "nz", 143462: "jp", 143463: "hk", 143464: "sg", 143465: "cn",
	143466: "kr", 143467: "in", 143468: "mx", 143469: "ru", 143470: "tw",
	143471: "vn", 143472: "za", 143473: "my", 143474: "ph", 143475: "th",
	143476: "id", 143477: "pk", 143478: "pl", 143479: "sa", 143480: "tr",
	143481: "ae", 143482: "hu", 143483: "cl", 143484: "np", 143485: "pa",
	143486: "lk", 143487: "ro", 143488: "mv", 143489: "cz", 143490: "bd",
	143491: "il", 143492: "ua", 143493: "kw", 143494: "hr", 143495: "cr",
	143496: "sk", 143497: "lb", 143498: "qa", 143499: "si", 143500: "rs",
	143501: "co", 143502: "ve", 143503: "br", 143504: "gt", 143505: "ar",
	143506: "sv", 143507: "pe", 143508: "do", 143509: "ec", 143510: "hn",
	143511: "jm", 143512: "ni", 143513: "py", 143514: "uy", 143515: "mo",
	143516: "eg", 143517: "kz", 143518: "ee", 143519: "lv", 143520: "lt",
	143521: "mt", 143522: "li", 143523: "md", 143524: "am", 143525: "bw",
	143526: "bg", 143527: "ci", 143528: "jo", 143529: "ke", 143530: "mk",
	143531: "mg", 143532: "ml", 143533: "mu", 143534: "ne", 143535: "sn",
	143536: "tn", 143537: "ug", 143538: "ai", 143539: "bs", 143540: "ag",
	143541: "bb", 143542: "bm", 143543: "vg", 143544: "ky", 143545: "dm",
	143546: "gd", 143547: "ms", 143548: "kn", 143549: "lc", 143550: "vc",
	143551: "tt", 143552: "tc", 143553: "gy", 143554: "sr", 143555: "bz",
	143556: "bo", 143557: "cy", 143558: "is", 143559: "bh", 143560: "bn",
	143561: "ng", 143562: "om", 143563: "dz", 143564: "ao", 143565: "by",
	143566: "uz", 143568: "az", 143571: "ye", 143572: "tz", 143573: "gh",
}
