// templates.go — ready-made site lists for quick block setup.
//
// "open-turkey block create focus --template social-media" seeds a new block
// with a curated list instead of making the user type every domain. Templates
// are intentionally small and mainstream; users extend them with add-site.
package cli

import (
	"fmt"
	"sort"
	"strings"
)

// templates maps a template name to its seed domains.
var templates = map[string][]string{
	"social-media": {
		"facebook.com", "instagram.com", "x.com", "twitter.com", "tiktok.com",
		"reddit.com", "snapchat.com", "linkedin.com", "pinterest.com",
		"tumblr.com", "threads.net", "mastodon.social",
	},
	"news": {
		"cnn.com", "bbc.com", "nytimes.com", "theguardian.com", "foxnews.com",
		"reuters.com", "apnews.com", "washingtonpost.com", "news.google.com",
	},
	"video": {
		"youtube.com", "netflix.com", "twitch.tv", "hulu.com", "disneyplus.com",
		"primevideo.com", "vimeo.com", "dailymotion.com",
	},
	"shopping": {
		"amazon.com", "ebay.com", "etsy.com", "aliexpress.com", "walmart.com",
		"target.com", "temu.com", "shein.com",
	},
}

// templateDomains returns the seed domains for a template name, or an error
// listing the available templates.
func templateDomains(name string) ([]string, error) {
	domains, ok := templates[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("unknown template %q (available: %s)", name, availableTemplates())
	}
	// Return a copy so callers can't mutate the shared slice.
	out := make([]string, len(domains))
	copy(out, domains)
	return out, nil
}

func availableTemplates() string {
	names := make([]string, 0, len(templates))
	for n := range templates {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
