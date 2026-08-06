//go:build console_embed

package console

import "embed"

//go:embed all:dist
var DistFS embed.FS
