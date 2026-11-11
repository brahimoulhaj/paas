// Package brand is the single place the product name lives.
// Renaming the product is a two-line change here; everything else derives from it.
package brand

import "strings"

const (
	Name = "Berth" // UI title, CLI help, emails, notifications
	Slug = "berth" // binary, image, Docker network, data dir, env prefix
)

// EnvPrefix is the prefix of every environment variable the panel reads, e.g. "BERTH_".
func EnvPrefix() string { return strings.ToUpper(Slug) + "_" }

// DataDir is the default data directory, e.g. "/var/lib/berth".
func DataDir() string { return "/var/lib/" + Slug }

// Network is the Docker network every managed container joins.
func Network() string { return Slug }

// SessionCookie is the name of the browser session cookie.
func SessionCookie() string { return Slug + "_session" }
