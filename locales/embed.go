package locales

import "embed"

// LocaleFS embeds all active JSON translation bundles so i18n initialization
// does not depend on the process working directory.
//
//go:embed active.*.json
var LocaleFS embed.FS
