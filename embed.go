package seedfall

import "embed"

//go:embed docs/*.md skills/*/SKILL.md go.mod
var Files embed.FS
